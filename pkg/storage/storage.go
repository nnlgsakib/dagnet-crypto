package storage

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// DBEngine is the interface for storage engines
type DBEngine interface {
	Get(key []byte) ([]byte, error)
	Put(key []byte, value []byte) error
	Delete(key []byte) error
	Has(key []byte) (bool, error)
	NewBatch() Batch
	Close() error
	SearchRange(start, end []byte) (map[string][]byte, error)
}

// Batch is the interface for batch operations
type Batch interface {
	Put(key []byte, value []byte)
	Delete(key []byte)
	Write() error
	Reset()
}

// LevelDBEngine implements DBEngine using LevelDB
type LevelDBEngine struct {
	db *leveldb.DB
	mu sync.RWMutex
}

// NewLevelDBEngine creates a new LevelDB engine
func NewLevelDBEngine(path string) (*LevelDBEngine, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open LevelDB at %s", path)
	}

	return &LevelDBEngine{
		db: db,
	}, nil
}

// Get retrieves a value by key
func (e *LevelDBEngine) Get(key []byte) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	value, err := e.db.Get(key, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to get from database")
	}
	return value, nil
}

// Put stores a key-value pair
func (e *LevelDBEngine) Put(key []byte, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.db.Put(key, value, nil); err != nil {
		return errors.Wrap(err, "failed to put to database")
	}
	return nil
}

// Delete removes a key-value pair
func (e *LevelDBEngine) Delete(key []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.db.Delete(key, nil); err != nil {
		return errors.Wrap(err, "failed to delete from database")
	}
	return nil
}

// Has checks if a key exists
func (e *LevelDBEngine) Has(key []byte) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.db.Has(key, nil)
}

// NewBatch creates a new batch
func (e *LevelDBEngine) NewBatch() Batch {
	return &LevelDBBatch{
		batch: new(leveldb.Batch),
		db:    e.db,
	}
}

// Close closes the database
func (e *LevelDBEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.db.Close()
}

// SearchRange performs a range query
func (e *LevelDBEngine) SearchRange(start, end []byte) (map[string][]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make(map[string][]byte)
	iter := e.db.NewIterator(&util.Range{Start: start, Limit: end}, nil)
	defer iter.Release()

	for iter.Next() {
		key := make([]byte, len(iter.Key()))
		value := make([]byte, len(iter.Value()))
		copy(key, iter.Key())
		copy(value, iter.Value())
		result[string(key)] = value
	}

	if err := iter.Error(); err != nil {
		return nil, errors.Wrap(err, "iterator error")
	}

	return result, nil
}

// LevelDBBatch implements Batch for LevelDB
type LevelDBBatch struct {
	batch *leveldb.Batch
	db    *leveldb.DB
	mu    sync.Mutex
}

// Put adds a put operation to the batch
func (b *LevelDBBatch) Put(key []byte, value []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.batch.Put(key, value)
}

// Delete adds a delete operation to the batch
func (b *LevelDBBatch) Delete(key []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.batch.Delete(key)
}

// Write writes the batch to database
func (b *LevelDBBatch) Write() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.db.Write(b.batch, nil)
}

// Reset resets the batch
func (b *LevelDBBatch) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.batch.Reset()
}

// Store is the main storage interface for the blockchain
type Store struct {
	eventStore    *EventStore
	stateStore    *StateStore
	tokenStore    *TokenStore
	nftStore      *NFTStore
	validatorStore *ValidatorStore
	dbEngine      DBEngine
}

// NewStore creates a new storage instance
func NewStore(dbPath string) (*Store, error) {
	dbEngine, err := NewLevelDBEngine(dbPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create database engine at %s", dbPath)
	}

	return &Store{
		dbEngine:       dbEngine,
		eventStore:     NewEventStore(dbEngine),
		stateStore:     NewStateStore(dbEngine),
		tokenStore:     NewTokenStore(dbEngine),
		nftStore:       NewNFTStore(dbEngine),
		validatorStore: NewValidatorStore(dbEngine),
	}, nil
}

// GetEvent retrieves an event by ID
func (s *Store) GetEvent(eventID string) ([]byte, error) {
	key := []byte(fmt.Sprintf("event:%s", eventID))
	return s.dbEngine.Get(key)
}

// GetTransaction retrieves a transaction by ID
func (s *Store) GetTransaction(txID string) ([]byte, error) {
	key := []byte(fmt.Sprintf("tx:%s", txID))
	return s.dbEngine.Get(key)
}

// PutEvent stores an event
func (s *Store) PutEvent(eventID string, data []byte) error {
	key := []byte(fmt.Sprintf("event:%s", eventID))
	return s.dbEngine.Put(key, data)
}

// PutTransaction stores a transaction
func (s *Store) PutTransaction(txID string, data []byte) error {
	key := []byte(fmt.Sprintf("tx:%s", txID))
	return s.dbEngine.Put(key, data)
}

// EventStore manages event storage
type EventStore struct {
	db DBEngine
}

// NewEventStore creates a new event store
func NewEventStore(db DBEngine) *EventStore {
	return &EventStore{db: db}
}

// Put stores an event
func (s *EventStore) Put(eventID []byte, data []byte) error {
	key := append([]byte("event:"), eventID...)
	return s.db.Put(key, data)
}

// Get retrieves an event
func (s *EventStore) Get(eventID []byte) ([]byte, error) {
	key := append([]byte("event:"), eventID...)
	return s.db.Get(key)
}

// Has checks if an event exists
func (s *EventStore) Has(eventID []byte) (bool, error) {
	key := append([]byte("event:"), eventID...)
	return s.db.Has(key)
}

// StateStore manages blockchain state storage
type StateStore struct {
	db DBEngine
}

// NewStateStore creates a new state store
func NewStateStore(db DBEngine) *StateStore {
	return &StateStore{db: db}
}

// GetAccount retrieves an account
func (s *StateStore) GetAccount(address string) ([]byte, error) {
	key := []byte(fmt.Sprintf("account:%s", address))
	return s.db.Get(key)
}

// PutAccount stores an account
func (s *StateStore) PutAccount(address string, data []byte) error {
	key := []byte(fmt.Sprintf("account:%s", address))
	return s.db.Put(key, data)
}

// TokenStore manages token storage
type TokenStore struct {
	db DBEngine
}

// NewTokenStore creates a new token store
func NewTokenStore(db DBEngine) *TokenStore {
	return &TokenStore{db: db}
}

// GetToken retrieves a token by ID
func (s *TokenStore) GetToken(tokenID string) ([]byte, error) {
	key := []byte(fmt.Sprintf("token:%s", tokenID))
	return s.db.Get(key)
}

// PutToken stores a token
func (s *TokenStore) PutToken(tokenID string, data []byte) error {
	key := []byte(fmt.Sprintf("token:%s", tokenID))
	return s.db.Put(key, data)
}

// NFTStore manages NFT storage
type NFTStore struct {
	db DBEngine
}

// NewNFTStore creates a new NFT store
func NewNFTStore(db DBEngine) *NFTStore {
	return &NFTStore{db: db}
}

// GetNFT retrieves an NFT
func (s *NFTStore) GetNFT(collectionID, nftID string) ([]byte, error) {
	key := []byte(fmt.Sprintf("nft:%s:%s", collectionID, nftID))
	return s.db.Get(key)
}

// PutNFT stores an NFT
func (s *NFTStore) PutNFT(collectionID, nftID string, data []byte) error {
	key := []byte(fmt.Sprintf("nft:%s:%s", collectionID, nftID))
	return s.db.Put(key, data)
}

// ValidatorStore manages validator storage
type ValidatorStore struct {
	db DBEngine
}

// NewValidatorStore creates a new validator store
func NewValidatorStore(db DBEngine) *ValidatorStore {
	return &ValidatorStore{db: db}
}

// GetValidator retrieves a validator
func (s *ValidatorStore) GetValidator(address string) ([]byte, error) {
	key := []byte(fmt.Sprintf("validator:%s", address))
	return s.db.Get(key)
}

// PutValidator stores a validator
func (s *ValidatorStore) PutValidator(address string, data []byte) error {
	key := []byte(fmt.Sprintf("validator:%s", address))
	return s.db.Put(key, data)
}

// Close closes all stores
func (s *Store) Close() error {
	if s.dbEngine != nil {
		return s.dbEngine.Close()
	}
	return nil
}

// Storage errors
var (
	ErrNotFound = errors.New("key not found")
	ErrKeyExists = errors.New("key already exists")
	ErrInvalidKey = errors.New("invalid key format")
)