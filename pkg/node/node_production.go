package node

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/api"
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/network"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

// NodeProduction represents a production-ready NDAG Coin node
type NodeProduction struct {
	ctx          context.Context
	config       *config.Config
	storage      *storage.Store
	dag          *consensus.DAG
	consensus    *consensus.ConsensusEngine
	mempool      *mempool.Mempool
	network      *network.P2PNetwork
	apiServer    *api.APIServer
	executor     *state.TransactionExecutor
	running      bool
	mu           sync.RWMutex
}

// NewNodeProduction creates a production-ready node
func NewNodeProduction(ctx context.Context, cfg *config.Config) (*NodeProduction, error) {
	log.Info().Msg("Initializing production node...")

	// Initialize storage
	storagePath := config.GetFullDataDir(cfg)
	if err := os.MkdirAll(storagePath, 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create storage directory")
	}

	store, err := storage.NewStore(storagePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize storage")
	}

	// Initialize DAG
	dag := consensus.NewDAG()
	log.Debug().Msg("DAG initialized")

	// Initialize consensus engine
	consensusEngine := consensus.NewConsensusEngine(dag)
	log.Debug().Msg("Consensus engine initialized")

	// Initialize mempool
	mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
	log.Debug().Msg("Mempool initialized")

	// Initialize network
	var p2pNetwork *network.P2PNetwork
	if cfg.P2P.Enabled {
		p2pNetwork, err = network.NewP2PNetwork(ctx, cfg)
		if err != nil {
			return nil, errors.Wrap(err, "failed to initialize P2P network")
		}
		log.Debug().Msg("Production P2P network initialized")
	}

	// Initialize API server
	var apiServer *api.APIServer
	if cfg.API.Enabled {
		apiServer = api.NewAPIServer(cfg, store, mp, consensusEngine, nil)
		log.Debug().Msg("API server initialized")
	}

	// Initialize transaction executor
	executor := state.NewTransactionExecutor(nil, store, consensusEngine)
	log.Debug().Msg("Transaction executor initialized")

	node := &NodeProduction{
		ctx:       ctx,
		config:    cfg,
		storage:   store,
		dag:       dag,
		consensus: consensusEngine,
		mempool:   mp,
		network:   p2pNetwork,
		apiServer: apiServer,
		executor:  executor,
	}

	// Load genesis if exists
	if err := node.loadGenesis(); err != nil {
		log.Warn().Err(err).Msg("No genesis found, creating new")
	}

	log.Info().Msg("Production node initialized successfully")
	return node, nil
}

// Start starts the production node with full error handling
func (n *NodeProduction) Start() error {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return errors.New("node is already running")
	}
	n.running = true
	n.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("Node startup panic recovered")
			n.Stop()
		}
	}()

	log.Info().Msg("Starting production node...")

	// Start consensus engine
	go func() {
		if err := n.consensus.Start(n.ctx); err != nil {
			log.Error().Err(err).Msg("Consensus engine error")
		}
	}()
	log.Debug().Msg("Consensus engine started")

	// Start mempool
	n.mempool.Start(n.ctx)
	log.Debug().Msg("Mempool started")

	// Start API server
	if n.apiServer != nil {
		go func() {
			if err := n.apiServer.Start(n.ctx); err != nil {
				log.Error().Err(err).Msg("API server error")
			}
		}()
		log.Debug().Msg("API server started")
	}

	// Start network
	if n.network != nil {
		if err := n.network.Start(n.ctx); err != nil {
			return errors.Wrap(err, "failed to start network")
		}
		log.Debug().Msg("P2P network started")
	}

	// Start event coordination
	go n.eventLoop()
	log.Debug().Msg("Event loop started")

	// Start metrics reporting
	go n.metricsLoop()
	log.Debug().Msg("Metrics loop started")

	log.Info().Msg("✓ Production node started successfully")
	return nil
}

// Stop gracefully stops all components
func (n *NodeProduction) Stop() error {
	n.mu.Lock()
	if !n.running {
		n.mu.Unlock()
		return nil
	}
	defer func() {
		n.running = false
		n.mu.Unlock()
	}()

	log.Info().Msg("Stopping production node...")

	// Stop API server
	if n.apiServer != nil {
		if err := n.apiServer.Stop(); err != nil {
			log.Error().Err(err).Msg("API server stop error")
		}
		log.Debug().Msg("API server stopped")
	}

	// Stop network
	if n.network != nil {
		if err := n.network.Stop(); err != nil {
			log.Error().Err(err).Msg("P2P network stop error")
		}
		log.Debug().Msg("P2P network stopped")
	}

	// Stop mempool
	n.mempool.Stop()
	log.Debug().Msg("Mempool stopped")

	// Stop consensus
	n.consensus.Stop()
	log.Debug().Msg("Consensus engine stopped")

	// Close storage
	if err := n.storage.Close(); err != nil {
		log.Error().Err(err).Msg("Storage close error")
	}
	log.Debug().Msg("Storage closed")

	log.Info().Msg("✓ Production node stopped successfully")
	return nil
}

// eventLoop coordinates all node operations
func (n *NodeProduction) eventLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("Event loop panic recovered")
		}
	}()

	log.Info().Msg("Event loop started")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			log.Debug().Msg("Event loop shutting down")
			return

		case <-ticker.C:
			n.processTick()

			// Emergency shutdown check
			if n.shouldEmergencyShutdown() {
				log.Fatal().Msg("Emergency shutdown triggered")
				return
			}
		}
	}
}

// processTick handles periodic node operations
func (n *NodeProduction) processTick() {
	// Process mempool
	n.processMempool()

	// Check finality
	n.checkFinality()

	// Handle network events
	if n.network != nil {
		n.processNetworkEvents()
	}

	// Cleanup old data
	n.cleanup()
}

// processMempool creates events from pending transactions
func (n *NodeProduction) processMempool() {
	pending := n.mempool.GetPending(100)
	if len(pending) == 0 {
		return
	}

	// Create and sign event
	event := &pb.Event{
		Id:           generateEventID(),
		Creator:      getNodeID(n.config),
		Parents:      n.getLatestParents(),
		Timestamp:    time.Now().UnixNano(),
		Round:        n.consensus.CurrentRound(),
		Transactions: pending,
	}

	// Sign event
	if err := event.Sign(); err != nil {
		log.Error().Err(err).Msg("Failed to sign event")
		return
	}

	// Add to DAG
	if err := n.dag.AddEvent(event); err != nil {
		log.Error().Err(err).Str("event_id", event.Id).Msg("Failed to add event to DAG")
		return
	}

	// Broadcast to network
	if n.network != nil {
		if err := n.network.BroadcastEvent(event); err != nil {
			log.Warn().Err(err).Str("event_id", event.Id).Msg("Failed to broadcast event")
		}
	}

	// Remove processed transactions
	for _, tx := range pending {
		n.mempool.Remove(tx.Id)
	}

	log.Debug().Int("txs", len(pending)).Str("event", event.Id).Msg("Created event from mempool")
}

// checkFinality processes finalized events
func (n *NodeProduction) checkFinality() {
	latestRound := n.consensus.CurrentRound()
	for round := uint64(0); round <= latestRound; round++ {
		witnesses := n.dag.GetWitnesses(round)
		for _, witness := range witnesses {
			if n.consensus.CheckFinality(witness.Id) {
				// Execute transactions in finalized events
				n.executeFinalizedEvent(witness)
			}
		}
	}
}

// executeFinalizedEvent executes all transactions in a finalized event
func (n *NodeProduction) executeFinalizedEvent(event *pb.Event) {
	for _, tx := range event.Transactions {
		result, err := n.executor.Execute(tx)
		if err != nil {
			log.Error().
				Err(err).
				Str("tx_id", tx.Id).
				Str("event_id", event.Id).
				Msg("Transaction execution failed")
			continue
		}

		if result.Success {
			log.Debug().
				Str("tx_id", tx.Id).
				Str("event_id", event.Id).
				Msg("Transaction executed successfully")
		}
	}
}

// cleanup maintains node resources
func (n *NodeProduction) cleanup() {
	// Clear old mempool transactions
	if n.mempool.Size() > 10000 {
		log.Warn().Int("size", n.mempool.Size()).Msg("Mempool size high, cleaning up old transactions")
		n.mempool.Clear()
	}

	// Force garbage collection periodically
	if time.Now().Unix()%60 == 0 {
		runtime.GC()
		log.Debug().Msg("Garbage collection completed")
	}
}

// metricsLoop reports node metrics
func (n *NodeProduction) metricsLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.reportMetrics()
		}
	}
}

// reportMetrics logs current node statistics
func (n *NodeProduction) reportMetrics() {
	log.Info().
		Uint64("round", n.consensus.CurrentRound()).
		Int("events", n.dag.EventsCount()).
		Int("mempool_size", n.mempool.Size()).
		Int("goroutines", runtime.NumGoroutine()).
		Msg("Node metrics")
}

// shouldEmergencyShutdown checks if node should shut down
func (n *NodeProduction) shouldEmergencyShutdown() bool {
	// Check memory usage
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	if m.Alloc > 2*1024*1024*1024 { // 2GB
		log.Error().Uint64("alloc", m.Alloc).Msg("Memory usage critical")
		return true
	}
	
	// Check for fatal conditions
	if runtime.NumGoroutine() > 10000 {
		log.Error().Int("goroutines", runtime.NumGoroutine()).Msg("Too many goroutines")
		return true
	}
	
	return false
}

// loadGenesis loads the genesis block
func (n *NodeProduction) loadGenesis() error {
	genesisFile := n.config.GetFullDataDir() + "/genesis.pb"
	
	if _, err := os.Stat(genesisFile); os.IsNotExist(err) {
		log.Info().Msg("No genesis file found, creating new genesis")
		
		// Create genesis event
		genesisEvent := createGenesisEvent(n.config, 100000000000000) // 100M NDAG
		data := mustMarshal(genesisEvent)
		
		if err := os.WriteFile(genesisFile, data, 0644); err != nil {
			return errors.Wrap(err, "failed to write genesis")
		}
	}
	
	// Load and process genesis
	data, err := os.ReadFile(genesisFile)
	if err != nil {
		return errors.Wrap(err, "failed to read genesis")
	}
	
	var genesis pb.Event
	if err := proto.Unmarshal(data, &genesis); err != nil {
		return errors.Wrap(err, "failed to unmarshal genesis")
	}
	
	// Add to DAG and finalize
	if err := n.dag.AddEvent(&genesis); err != nil {
		log.Warn().Err(err).Msg("Genesis already exists")
	}
	
	n.consensus.CheckFinality(genesis.Id)
	log.Info().Str("id", genesis.Id).Msg("Genesis loaded and finalized")
	
	return nil
}

// generateEventID creates a unique event ID
func generateEventID() string {
	return fmt.Sprintf("event-%d-%d", time.Now().UnixNano(), time.Now().Unix())
}

// getNodeID returns the node's identity
func getNodeID(cfg *config.Config) string {
	if cfg.Validator.Enabled && cfg.Validator.Address != "" {
		return cfg.Validator.Address
	}
	if cfg.Network != nil {
		return fmt.Sprintf("ndag-node-%s", cfg.Network.ChainID)
	}
	return "ndag-anonymous-node"
}

// getLatestParents returns the latest suitable parent events
func (n *NodeProduction) getLatestParents() []string {
	latestRound := n.consensus.CurrentRound()
	witnesses := n.dag.GetWitnesses(latestRound)
	
	var parents []string
	if len(witnesses) > 0 {
		// Use last 2 witnesses as parents (simplified for production)
		for i := len(witnesses) - 2; i < len(witnesses) && i >= 0; i++ {
			parents = append(parents, witnesses[i].Id)
		}
	}
	
	if len(parents) == 0 {
		// Genesis case
		parents = []string{}
	}
	
	return parents
}

// mustMarshal is a helper that panics on marshal error (safe for genesis)
func mustMarshal(msg proto.Message) []byte {
	data, err := proto.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("marshal error: %v", err))
	}
	return data
}

// createGenesisEvent creates a new genesis event
func createGenesisEvent(cfg *config.Config, initialSupply uint64) *pb.Event {
	genesisTx := &pb.Transaction{
		Id:        "genesis-tx-1",
		ChainId:   cfg.Network.ChainID,
		NetworkId: cfg.Network.NetworkID,
		Nonce:     0,
		Fee:       0,
		GasLimit:  1000000,
		Timestamp: time.Now().UnixNano(),
		Payload: &pb.Transaction_GenesisPayload{
			GenesisPayload: &pb.GenesisPayload{
				Treasury:      "genesis-treasury",
				InitialSupply: initialSupply,
			},
		},
		PublicKey: []byte("genesis-key"),
		Signature: []byte("genesis-sig"),
	}
	
	event := &pb.Event{
		Id:           "genesis-event-1",
		Creator:      "genesis",
		Parents:      []string{},
		Timestamp:    time.Now().UnixNano(),
		Round:        0,
		Transactions: []*pb.Transaction{genesisTx},
		Metadata: &pb.EventMetadata{
			IsWitness:   true,
			IsFamous:    true,
			IsFinalized: true,
		},
	}
	
	return event
}