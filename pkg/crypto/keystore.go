package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/scrypt"
)

const (
	// KeyFileVersion is the current key file version
	KeyFileVersion = 1
	// DefaultKeyDir is the default directory for key files
	DefaultKeyDir = ".ndag/keys"
	// DefaultScryptN is the scrypt N parameter for key derivation
	DefaultScryptN = 1 << 15
	// DefaultScryptP is the scrypt P parameter for key derivation
	DefaultScryptP = 1
)

// KeyStore manages encrypted key storage
type KeyStore struct {
	dir      string
	keys     map[string]*KeyFile
	mu       sync.RWMutex
}

// KeyFile represents an encrypted key file
type KeyFile struct {
	Version      int       `json:"version"`
	Name         string    `json:"name"`
	Address      string    `json:"address"`
	PublicKey    string    `json:"public_key"`
	EncryptedKey string    `json:"encrypted_key"`
	Salt         string    `json:"salt"`
	Nonce        string    `json:"nonce"`
	CreatedAt    time.Time `json:"created_at"`
	ModifiedAt   time.Time `json:"modified_at"`
}

// NewKeyStore creates a new keystore
func NewKeyStore(dir string) (*KeyStore, error) {
	if dir == "" {
		dir = DefaultKeyDir
	}

	// Expand home directory
	if dir[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get home directory")
		}
		dir = filepath.Join(home, dir[1:])
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.Wrapf(err, "failed to create keystore directory: %s", dir)
	}

	ks := &KeyStore{
		dir:  dir,
		keys: make(map[string]*KeyFile),
	}

	// Load existing keys
	if err := ks.loadAll(); err != nil {
		log.Warn().Err(err).Msg("Failed to load some keys")
	}

	return ks, nil
}

// GenerateKey generates a new keypair and stores it encrypted
func (ks *KeyStore) GenerateKey(name, password string) (*Keypair, error) {
	if name == "" {
		return nil, errors.New("key name cannot be empty")
	}
	if password == "" {
		return nil, errors.New("password cannot be empty")
	}

	// Check if key already exists
	ks.mu.RLock()
	_, exists := ks.keys[name]
	ks.mu.RUnlock()
	if exists {
		return nil, errors.Errorf("key with name '%s' already exists", name)
	}

	// Generate new keypair
	keypair, err := GenerateKeypair()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate keypair")
	}

	// Encrypt private key
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, errors.Wrap(err, "failed to generate salt")
	}

	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.Wrap(err, "failed to generate nonce")
	}

	// Derive encryption key
	key, err := scrypt.Key([]byte(password), salt, DefaultScryptN, 8, 1, 32)
	if err != nil {
		return nil, errors.Wrap(err, "failed to derive encryption key")
	}

	// Encrypt private key (simplified - in production use authenticated encryption)
	encryptedKey, err := encrypt(key, nonce, keypair.Private)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encrypt private key")
	}

	// Create key file
	keyFile := &KeyFile{
		Version:      KeyFileVersion,
		Name:         name,
		Address:      fmt.Sprintf("ndag%s", hex.EncodeToString(keypair.Public[:8])),
		PublicKey:    hex.EncodeToString(keypair.Public),
		EncryptedKey: hex.EncodeToString(encryptedKey),
		Salt:         hex.EncodeToString(salt),
		Nonce:        hex.EncodeToString(nonce),
		CreatedAt:    time.Now(),
		ModifiedAt:   time.Now(),
	}

	// Save to disk
	if err := ks.saveKeyFile(keyFile); err != nil {
		return nil, errors.Wrap(err, "failed to save key file")
	}

	// Add to memory cache
	ks.mu.Lock()
	ks.keys[name] = keyFile
	ks.mu.Unlock()

	return keypair, nil
}

// ImportKey imports an existing private key
func (ks *KeyStore) ImportKey(name, password string, privateKey []byte) (*Keypair, error) {
	if name == "" {
		return nil, errors.New("key name cannot be empty")
	}
	if password == "" {
		return nil, errors.New("password cannot be empty")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.Errorf("invalid private key size: %d", len(privateKey))
	}

	// Check if key already exists
	ks.mu.RLock()
	_, exists := ks.keys[name]
	ks.mu.RUnlock()
	if exists {
		return nil, errors.Errorf("key with name '%s' already exists", name)
	}

	// Create keypair from private key
	keypair, err := KeypairFromPrivateKey(PrivateKey(privateKey))
	if err != nil {
		return nil, err
	}

	// Encrypt and save
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, errors.Wrap(err, "failed to generate salt")
	}

	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.Wrap(err, "failed to generate nonce")
	}

	key, err := scrypt.Key([]byte(password), salt, DefaultScryptN, 8, 1, 32)
	if err != nil {
		return nil, errors.Wrap(err, "failed to derive encryption key")
	}

	encryptedKey, err := encrypt(key, nonce, keypair.Private)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encrypt private key")
	}

	keyFile := &KeyFile{
		Version:      KeyFileVersion,
		Name:         name,
		Address:      fmt.Sprintf("ndag%s", hex.EncodeToString(keypair.Public[:8])),
		PublicKey:    hex.EncodeToString(keypair.Public),
		EncryptedKey: hex.EncodeToString(encryptedKey),
		Salt:         hex.EncodeToString(salt),
		Nonce:        hex.EncodeToString(nonce),
		CreatedAt:    time.Now(),
		ModifiedAt:   time.Now(),
	}

	if err := ks.saveKeyFile(keyFile); err != nil {
		return nil, errors.Wrap(err, "failed to save key file")
	}

	ks.mu.Lock()
	ks.keys[name] = keyFile
	ks.mu.Unlock()

	return keypair, nil
}

// GetKey retrieves a keypair by name
func (ks *KeyStore) GetKey(name, password string) (*Keypair, error) {
	// Load key file
	keyFile, err := ks.loadKeyFile(name)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load key '%s'", name)
	}

	// Decode encrypted key
	encryptedKey, err := hex.DecodeString(keyFile.EncryptedKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode encrypted key")
	}

	salt, err := hex.DecodeString(keyFile.Salt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode salt")
	}

	nonce, err := hex.DecodeString(keyFile.Nonce)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode nonce")
	}

	// Derive decryption key
	key, err := scrypt.Key([]byte(password), salt, DefaultScryptN, 8, 1, 32)
	if err != nil {
		return nil, errors.Wrap(err, "failed to derive decryption key")
	}

	// Decrypt private key
	privateKey, err := decrypt(key, nonce, encryptedKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decrypt private key")
	}

	return KeypairFromPrivateKey(PrivateKey(privateKey))
}

// ListKeys returns all key names
func (ks *KeyStore) ListKeys() []string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	names := make([]string, 0, len(ks.keys))
	for name := range ks.keys {
		names = append(names, name)
	}
	return names
}

// DeleteKey deletes a key
func (ks *KeyStore) DeleteKey(name string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	if _, exists := ks.keys[name]; !exists {
		return errors.Errorf("key '%s' not found", name)
	}

	filePath := filepath.Join(ks.dir, fmt.Sprintf("%s.key", name))
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to delete key file")
	}

	delete(ks.keys, name)
	return nil
}

// loadAll loads all keys from disk
func (ks *KeyStore) loadAll() error {
	files, err := ioutil.ReadDir(ks.dir)
	if err != nil {
		return errors.Wrap(err, "failed to read keystore directory")
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()

	for _, file := range files {
		if filepath.Ext(file.Name()) != ".key" {
			continue
		}

		name := file.Name()[:len(file.Name())-4]
		keyFile, err := ks.loadKeyFile(name)
		if err != nil {
			log.Error().Err(err).Str("key", name).Msg("Failed to load key")
			continue
		}

		ks.keys[name] = keyFile
	}

	return nil
}

// loadKeyFile loads a single key file
func (ks *KeyStore) loadKeyFile(name string) (*KeyFile, error) {
	filePath := filepath.Join(ks.dir, fmt.Sprintf("%s.key", name))

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read key file: %s", filePath)
	}

	var keyFile KeyFile
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal key file")
	}

	return &keyFile, nil
}

// saveKeyFile saves a key file to disk
func (ks *KeyStore) saveKeyFile(keyFile *KeyFile) error {
	filePath := filepath.Join(ks.dir, fmt.Sprintf("%s.key", keyFile.Name))

	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal key file")
	}

	if err := ioutil.WriteFile(filePath, data, 0600); err != nil {
		return errors.Wrap(err, "failed to write key file")
	}

	return nil
}

// Simple symmetric encryption (replace with proper implementation in production)
func encrypt(key, nonce, plaintext []byte) ([]byte, error) {
	ciphertext := make([]byte, len(plaintext))
	for i := range plaintext {
		ciphertext[i] = plaintext[i] ^ key[i%len(key)] ^ nonce[i%len(nonce)]
	}
	return ciphertext, nil
}

func decrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	plaintext := make([]byte, len(ciphertext))
	for i := range ciphertext {
		plaintext[i] = ciphertext[i] ^ key[i%len(key)] ^ nonce[i%len(nonce)]
	}
	return plaintext, nil
}

// GetKeyInfo returns key information (without private key)
func (ks *KeyStore) GetKeyInfo(name string) (*KeyInfo, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	keyFile, exists := ks.keys[name]
	if !exists {
		return nil, errors.Errorf("key '%s' not found", name)
	}

	pubKey, err := hex.DecodeString(keyFile.PublicKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode public key")
	}

	return &KeyInfo{
		Name:       name,
		Address:    keyFile.Address,
		PublicKey:  pubKey,
		CreatedAt:  keyFile.CreatedAt,
		IsActive:   true,
	}, nil
}

// KeyInfo represents public key information
type KeyInfo struct {
	Name      string
	Address   string
	PublicKey []byte
	CreatedAt time.Time
	IsActive  bool
}