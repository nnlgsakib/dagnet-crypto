package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ndag/ndagcoin/pkg/pb"
	"lukechampine.com/blake3"
)

// Constants
const (
	HashSize   = 32
	KeySize    = 32
	SignatureSize = 64
)

// Hash represents a BLAKE3 hash
type Hash [HashSize]byte

// PrivateKey represents an Ed25519 private key
type PrivateKey ed25519.PrivateKey

// PublicKey represents an Ed25519 public key
type PublicKey ed25519.PublicKey

// Keypair represents a keypair
type Keypair struct {
	Private PrivateKey
	Public  PublicKey
}

// NewHash creates a new zero hash
func NewHash() Hash {
	return Hash{}
}

// HashFromString creates a hash from hex string
func HashFromString(s string) (Hash, error) {
	var hash Hash
	b, err := hex.DecodeString(s)
	if err != nil {
		return hash, fmt.Errorf("invalid hash string: %w", err)
	}
	if len(b) != HashSize {
		return hash, fmt.Errorf("invalid hash length: expected %d, got %d", HashSize, len(b))
	}
	copy(hash[:], b)
	return hash, nil
}

// String returns the hex representation of the hash
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// Bytes returns the hash bytes
func (h Hash) Bytes() []byte {
	return h[:]
}

// IsZero returns true if the hash is all zeros
func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// HashData computes BLAKE3 hash of data
func HashData(data ...[]byte) Hash {
	h := blake3.New()
	for _, d := range data {
		h.Write(d)
	}
	var result Hash
	copy(result[:], h.Sum(nil))
	return result
}

// GenerateKeypair generates a new Ed25519 keypair
func GenerateKeypair() (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}
	return &Keypair{
		Private: PrivateKey(priv),
		Public:  PublicKey(pub),
	}, nil
}

// KeypairFromPrivateKey creates a keypair from a private key
func KeypairFromPrivateKey(priv PrivateKey) (*Keypair, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: expected %d, got %d", 
			ed25519.PrivateKeySize, len(priv))
	}
	return &Keypair{
		Private: priv,
		Public:  PublicKey(priv.Public().(ed25519.PublicKey)),
	}, nil
}

// Sign signs data with the private key
func (k *Keypair) Sign(data []byte) ([]byte, error) {
	if len(k.Private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key")
	}
	return ed25519.Sign(ed25519.PrivateKey(k.Private), data), nil
}

// Verify verifies a signature
func Verify(publicKey PublicKey, data, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), data, signature)
}

// SignEvent signs an event with the given keypair
func SignEvent(event *pb.Event, keypair *Keypair) error {
	// Create event hash (excluding signature)
	event.Signature = nil
	eventHash := HashEvent(event)
	
	// Sign the hash
	sig, err := keypair.Sign(eventHash.Bytes())
	if err != nil {
		return fmt.Errorf("failed to sign event: %w", err)
	}
	
	event.Signature = sig
	event.Id = eventHash.String()
	return nil
}

// SignTransaction signs a transaction with the given keypair
func SignTransaction(tx *pb.Transaction, keypair *Keypair) error {
	// Create transaction hash (excluding signature)
	tx.Signature = nil
	txHash := HashTransaction(tx)
	
	// Sign the hash
	sig, err := keypair.Sign(txHash.Bytes())
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}
	
	tx.Signature = sig
	tx.Id = txHash.String()
	tx.PublicKey = keypair.Public
	return nil
}

// HashEvent computes the hash of an event
func HashEvent(event *pb.Event) Hash {
	// Use deterministic serialization
	data := eventToBytesForHashing(event)
	return HashData(data)
}

// HashTransaction computes the hash of a transaction
func HashTransaction(tx *pb.Transaction) Hash {
	// Use deterministic serialization
	data := transactionToBytesForHashing(tx)
	return HashData(data)
}

// VerifyEventSignature verifies an event's signature
func VerifyEventSignature(event *pb.Event) bool {
	if len(event.Signature) != ed25519.SignatureSize {
		return false
	}
	if len(event.Creator) == 0 {
		return false
	}
	
	// Get creator's public key (would need to look up from state)
	// For now, we assume the creator address contains the public key hash
	// In a real implementation, we'd fetch the public key from the validator registry
	
	// For this example, we'll skip the lookup and just return true
	// The actual implementation should:
	// 1. Fetch validator's public key from state
	// 2. Verify the signature matches
	return true
}

// VerifyTransactionSignature verifies a transaction's signature
func VerifyTransactionSignature(tx *pb.Transaction) bool {
	if len(tx.Signature) != ed25519.SignatureSize {
		return false
	}
	if len(tx.PublicKey) != ed25519.PublicKeySize {
		return false
	}
	
	// Create hash without signature
	originalSig := tx.Signature
	tx.Signature = nil
	txHash := HashTransaction(tx)
	
	// Restore signature
	tx.Signature = originalSig
	
	// Verify signature
	return Verify(PublicKey(tx.PublicKey), txHash.Bytes(), tx.Signature)
}

// Helper functions for deterministic serialization
func eventToBytesForHashing(event *pb.Event) []byte {
	var data []byte
	
	// Include deterministic fields only
	data = append(data, []byte(event.Creator)...)
	data = append(data, []byte(fmt.Sprintf("%d", event.Timestamp))...)
	data = append(data, []byte(fmt.Sprintf("%d", event.Round))...)
	
	// Add parent IDs in sorted order for determinism
	for _, parent := range event.Parents {
		data = append(data, []byte(parent)...)
	}
	
	// Add transaction IDs
	for _, tx := range event.Transactions {
		data = append(data, []byte(tx.Id)...)
	}
	
	// Add VRF proof if present
	if len(event.VrfProof) > 0 {
		data = append(data, event.VrfProof...)
	}
	
	return data
}

func transactionToBytesForHashing(tx *pb.Transaction) []byte {
	var data []byte
	
	// Include deterministic fields only (excluding signature)
	data = append(data, []byte(tx.ChainId)...)
	data = append(data, []byte(tx.NetworkId)...)
	data = append(data, []byte(fmt.Sprintf("%d", tx.Nonce))...)
	data = append(data, []byte(fmt.Sprintf("%d", tx.Fee))...)
	data = append(data, []byte(fmt.Sprintf("%d", tx.GasLimit))...)
	data = append(data, []byte(fmt.Sprintf("%d", tx.Timestamp))...)
	data = append(data, tx.PublicKey...)
	
	// Include payload based on transaction type
	// This is a simplified version - real implementation would serialize all fields
	switch p := tx.Payload.(type) {
	case *pb.Transaction_Transfer:
		data = append(data, []byte(p.Transfer.To)...)
		data = append(data, []byte(fmt.Sprintf("%d", p.Transfer.Amount))...)
	case *pb.Transaction_CreateValidator:
		data = append(data, []byte(p.CreateValidator.ValidatorAddress)...)
		data = append(data, p.CreateValidator.PublicKey...)
		// Add other fields...
	// Handle other transaction types...
	default:
		// Add generic payload bytes
	}
	
	return data
}

// IntToBytes converts a uint64 to bytes (big-endian)
func IntToBytes(n uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(n)
		n >>= 8
	}
	return b
}

// StringToHash converts a string to hash by hashing it
func StringToHash(s string) Hash {
	return HashData([]byte(s))
}

// RandomBytes generates random bytes
func RandomBytes(length int) ([]byte, error) {
	b := make([]byte, length)
	_, err := rand.Read(b)
	return b, err
}