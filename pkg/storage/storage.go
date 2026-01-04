package storage

import (
	"fmt"
	"sync"

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
		return nil, fmt.Errorf("failed to open LevelDB: %w", err)
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
		return nil, fmt.Errorf("failed to get from database: %w", err)
	}
	return value, nil
}

// Put stores a key-value pair
func (e *LevelDBEngine) Put(key []byte, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if err := e.db.Put(key, value, nil); err != nil {
		return fmt.Errorf("failed to put to database: %w", err)
	}
	return nil
}

// Delete removes a key-value pair
func (e *LevelDBEngine) Delete(key []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if err := e.db.Delete(key, nil); err != nil {
		return fmt.Errorf("failed to delete from database: %w", err)
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
		return nil, fmt.Errorf("iterator error: %w", err)
	}
	
	return result, nil
}

// Store is the main storage interface
type Store struct {
	eventStore *EventStore
	stateStore *StateStore
	dbEngine   DBEngine
}

// NewStore creates a new storage instance
func NewStore(dbPath string) (*Store, error) {
	dbEngine, err := NewLevelDBEngine(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create database engine: %w", err)
	}
	
	return &Store{
		dbEngine:   dbEngine,
		eventStore: NewEventStore(dbEngine),
		stateStore: NewStateStore(dbEngine),
	}, nil
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

// Delete removes an event
func (s *EventStore) Delete(eventID []byte) error {
	key := append([]byte("event:"), eventID...)
	return s.db.Delete(key)
}

// Has checks if an event exists
func (s *EventStore) Has(eventID []byte) (bool, error) {
	key := append([]byte("event:"), eventID...)
	return s.db.Has(key)
}

// StateStore manages state storage
type StateStore struct {
	db DBEngine
}

// NewStateStore creates a new state store
func NewStateStore(db DBEngine) *StateStore {
	return &StateStore{db: db}
}

// Put stores state data
func (s *StateStore) Put(key []byte, value []byte) error {
	fullKey := append([]byte("state:"), key...)
	return s.db.Put(fullKey, value)
}

// Get retrieves state data
func (s *StateStore) Get(key []byte) ([]byte, error) {
	fullKey := append([]byte("state:"), key...)
	return s.db.Get(fullKey)
}

// Delete removes state data
func (s *StateStore) Delete(key []byte) error {
	fullKey := append([]byte("state:"), key...)
	return s.db.Delete(fullKey)
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
	ErrNotFound = fmt.Errorf("key not found")
)