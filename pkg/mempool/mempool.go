package mempool

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/rs/zerolog/log"
)

// Mempool manages pending transactions using a priority queue
type Mempool struct {
	txs       map[string]*MempoolTx
	queue     txPriorityQueue
	maxSize   int
	maxGas    uint64
	txChannel chan *pb.Transaction
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// MempoolTx wraps a transaction with metadata
type MempoolTx struct {
	Tx        *pb.Transaction
	Priority  float64
	Timestamp int64
	Size      int
	Index     int // for heap
}

// txPriorityQueue implements heap.Interface for transaction prioritization
type txPriorityQueue []*MempoolTx

func (pq txPriorityQueue) Len() int { return len(pq) }

func (pq txPriorityQueue) Less(i, j int) bool {
	// Higher priority first, then earlier timestamp if equal priority
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	return pq[i].Timestamp < pq[j].Timestamp
}

func (pq txPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *txPriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*MempoolTx)
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *txPriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.Index = -1
	*pq = old[0 : n-1]
	return item
}

// NewMempool creates a new mempool
func NewMempool(maxGas uint64) *Mempool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Mempool{
		txs:       make(map[string]*MempoolTx),
		queue:     make(txPriorityQueue, 0),
		maxSize:   10000, // Max 10,000 transactions
		maxGas:    maxGas,
		txChannel: make(chan *pb.Transaction, 1000),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Add adds a transaction to the mempool
func (m *Mempool) Add(tx *pb.Transaction) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check duplicates
	if _, exists := m.txs[tx.Id]; exists {
		log.Debug().Str("tx_id", tx.Id).Msg("Transaction already in mempool")
		return false
	}

	// Check size limits
	if len(m.txs) >= m.maxSize {
		log.Warn().Int("size", len(m.txs)).Msg("Mempool is full")
		return false
	}

	// Calculate priority based on fee (higher fee = higher priority)
	priority := float64(tx.Fee) / float64(tx.GasLimit)

	// Create mempool transaction
	mempoolTx := &MempoolTx{
		Tx:        tx,
		Priority:  priority,
		Timestamp: tx.Timestamp,
		Size:      len(tx.String()),
	}

	// Add to map and priority queue
	m.txs[tx.Id] = mempoolTx
	heap.Push(&m.queue, mempoolTx)

	// Send to channel for async processing
	select {
	case m.txChannel <- tx:
	default:
		// Channel full, transaction still added but notification dropped
		log.Warn().Str("tx_id", tx.Id).Msg("Transaction channel full")
	}

	log.Debug().Str("tx_id", tx.Id).Float64("priority", priority).Msg("Transaction added to mempool")
	return true
}

// Get retrieves a transaction by ID
func (m *Mempool) Get(txID string) (*pb.Transaction, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tx, exists := m.txs[txID]; exists {
		return tx.Tx, true
	}
	return nil, false
}

// Remove removes a transaction from the mempool
func (m *Mempool) Remove(txID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tx, exists := m.txs[txID]; exists {
		// Remove from heap (mark as removed)
		tx.Priority = -1 // Mark as removed (negative priority)
		delete(m.txs, txID)
		log.Debug().Str("tx_id", txID).Msg("Transaction removed from mempool")
	}
}

// GetPending returns the highest-priority transactions up to maxTxs
func (m *Mempool) GetPending(maxTxs int) []*pb.Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var txs []*pb.Transaction
	currentGas := uint64(0)

	// We need to copy the queue since we might modify it
	tempQueue := make(txPriorityQueue, len(m.queue))
	copy(tempQueue, m.queue)
	heap.Init(&tempQueue)

	for tempQueue.Len() > 0 && len(txs) < maxTxs {
		tx := heap.Pop(&tempQueue).(*MempoolTx)

		// Skip removed transactions
		if tx.Priority < 0 {
			continue
		}

		// Check gas limit
		currentGas += tx.Tx.GasLimit
		if currentGas > m.maxGas {
			break
		}

		txs = append(txs, tx.Tx)
	}

	log.Debug().Int("count", len(txs)).Msg("Retrieved pending transactions")
	return txs
}

// GetTxChannel returns the transaction channel
func (m *Mempool) GetTxChannel() <-chan *pb.Transaction {
	return m.txChannel
}

// Size returns the number of transactions in the mempool
func (m *Mempool) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.txs)
}

// Clear removes all transactions from the mempool
func (m *Mempool) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.txs = make(map[string]*MempoolTx)
	m.queue = make(txPriorityQueue, 0)
	log.Info().Msg("Mempool cleared")
}

// Start starts the mempool background tasks
func (m *Mempool) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Start cleanup goroutine
	go m.cleanupLoop()

	log.Info().Msg("Mempool started")
}

// Stop stops the mempool
func (m *Mempool) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	m.Clear()
	close(m.txChannel)
	log.Info().Msg("Mempool stopped")
}

// cleanupLoop periodically removes old transactions
func (m *Mempool) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}

// cleanup removes old transactions (older than 24 hours)
func (m *Mempool) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UnixNano()
	maxAge := int64(24 * time.Hour) // 24 hours in nanoseconds
	removed := 0

	for txID, tx := range m.txs {
		if now-tx.Timestamp > maxAge {
			delete(m.txs, txID)
			removed++
		}
	}

	if removed > 0 {
		log.Info().Int("removed", removed).Msg("Cleaned up old transactions")

		// Rebuild priority queue
		newQueue := make(txPriorityQueue, 0, len(m.txs))
		for _, tx := range m.txs {
			newQueue = append(newQueue, tx)
		}
		heap.Init(&newQueue)
		m.queue = newQueue
	}
}

// GetTotalGas returns the total gas of all transactions in the mempool
func (m *Mempool) GetTotalGas() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalGas uint64
	for _, tx := range m.txs {
		totalGas += tx.Tx.GasLimit
	}
	return totalGas
}

// GetTotalFees returns the total fees of all transactions in the mempool
func (m *Mempool) GetTotalFees() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalFees uint64
	for _, tx := range m.txs {
		totalFees += tx.Tx.Fee
	}
	return totalFees
}

// Has checks if a transaction exists in the mempool
func (m *Mempool) Has(txID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.txs[txID]
	return exists
}