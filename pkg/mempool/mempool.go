package mempool

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/ndag/ndagcoin/pkg/pb"
)

// Mempool manages pending transactions
type Mempool struct {
    txs       map[string]*MempoolTx
    queue     []string
    maxSize   int
    maxGas    uint64
    txChannel chan *pb.Transaction
    mu        sync.RWMutex
}

// MempoolTx wraps a transaction with metadata
type MempoolTx struct {
    Tx        *pb.Transaction
    Timestamp int64
    Priority  float64
    Size      int
}

// NewMempool creates a new mempool
func NewMempool(maxGas uint64) *Mempool {
    return &Mempool{
        txs:       make(map[string]*MempoolTx),
        queue:     make([]string, 0),
        maxSize:   10000, // Max transactions
        maxGas:    maxGas,
        txChannel: make(chan *pb.Transaction, 1000),
    }
}

// Add adds a transaction to the mempool
func (m *Mempool) Add(tx *pb.Transaction) bool {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Check duplicates
    if _, exists := m.txs[tx.Id]; exists {
        return false
    }

    // Check size limits
    if len(m.txs) >= m.maxSize {
        return false
    }

    // Add transaction
    mempoolTx := &MempoolTx{
        Tx:        tx,
        Timestamp: tx.Timestamp,
        Priority:  float64(tx.Fee),
        Size:      1, // Simplified size calculation
    }

    m.txs[tx.Id] = mempoolTx
    m.queue = append(m.queue, tx.Id)

    // Send to channel
    select {
    case m.txChannel <- tx:
    default:
    }

    return true
}

// Get retrieves a transaction
func (m *Mempool) Get(txID string) (*pb.Transaction, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    tx, exists := m.txs[txID]
    if !exists {
        return nil, false
    }
    return tx.Tx, true
}

// Remove removes a transaction
func (m *Mempool) Remove(txID string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    delete(m.txs, txID)

    // Remove from queue
    for i, id := range m.queue {
        if id == txID {
            m.queue = append(m.queue[:i], m.queue[i+1:]...)
            break
        }
    }
}

// GetPending returns pending transactions
func (m *Mempool) GetPending(maxTxs int) []*pb.Transaction {
    m.mu.RLock()
    defer m.mu.RUnlock()

    var txs []*pb.Transaction
    count := 0

    for _, txID := range m.queue {
        if count >= maxTxs {
            break
        }
        if tx, exists := m.txs[txID]; exists {
            txs = append(txs, tx.Tx)
            count++
        }
    }

    return txs
}

// GetTxChannel returns the transaction channel
func (m *Mempool) GetTxChannel() <-chan *pb.Transaction {
    return m.txChannel
}

// Size returns the number of transactions
func (m *Mempool) Size() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return len(m.txs)
}

// Clear removes all transactions
func (m *Mempool) Clear() {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.txs = make(map[string]*MempoolTx)
    m.queue = make([]string, 0)
}

// Start starts the mempool
func (m *Mempool) Start(ctx context.Context) {
    // Background cleanup
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                m.cleanup()
            }
        }
    }()
}

// Stop stops the mempool
func (m *Mempool) Stop() {
    close(m.txChannel)
}

// cleanup removes old transactions
func (m *Mempool) cleanup() {
    m.mu.Lock()
    defer m.mu.Unlock()

    now := time.Now().UnixNano()
    maxAge := int64(24 * time.Hour)

    for id, tx := range m.txs {
        if now-tx.Timestamp > maxAge {
            delete(m.txs, id)
        }
    }
}

// HandleIncomingTx processes incoming transactions
func (m *Mempool) HandleIncomingTx(tx *pb.Transaction) error {
    if !m.Add(tx) {
        return fmt.Errorf("failed to add transaction to mempool")
    }
    return nil
}
