package node

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/network"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Node represents a complete NDAG Coin node
type Node struct {
	ctx       context.Context
	config    *config.Config
	storage   *storage.Store
	dag       *consensus.DAG
	consensus *consensus.ConsensusEngine
	mempool   *mempool.Mempool
	network   *network.P2PNetwork
	executor  *state.TransactionExecutor
}

// NewNode creates a new NDAG Coin node
func NewNode(ctx context.Context, cfg *config.Config) (*Node, error) {
	log.Info().Msg("Initializing NDAG Coin node...")

	// Initialize storage
	storagePath := filepath.Join(cfg.GetFullDataDir(), cfg.Storage.Path)
	store, err := storage.NewStore(storagePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize storage")
	}

	// Initialize DAG
	dag := consensus.NewDAG()

	// Initialize consensus engine
	consensusEngine := consensus.NewConsensusEngine(dag)

	// Initialize mempool
	mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)

	// Initialize network
	var p2pNetwork *network.P2PNetwork
	if cfg.P2P.Enabled {
		p2pNetwork, err = network.NewP2PNetwork(ctx, cfg)
		if err != nil {
			return nil, errors.Wrap(err, "failed to initialize P2P network")
		}
	}

	// Initialize transaction executor
	executor := state.NewTransactionExecutor(nil, store, consensusEngine)

	node := &Node{
		ctx:       ctx,
		config:    cfg,
		storage:   store,
		dag:       dag,
		consensus: consensusEngine,
		mempool:   mp,
		network:   p2pNetwork,
		executor:  executor,
	}

	log.Info().Msg("Node components initialized successfully")
	return node, nil
}

// Start starts all node components
func (n *Node) Start() error {
	log.Info().Msg("Starting node components...")

	// Start consensus engine
	go func() {
		n.consensus.Start(n.ctx)
	}()

	// Start mempool
	n.mempool.Start(n.ctx)

	// Start P2P network
	if n.network != nil {
		if err := n.network.Start(n.ctx); err != nil {
			return errors.Wrap(err, "failed to start P2P network")
		}
	}

	// Start main event loop
	go n.eventLoop()

	log.Info().Msg("Node started successfully")
	return nil
}

// Stop gracefully stops all node components
func (n *Node) Stop() error {
	log.Info().Msg("Stopping node components...")

	// Stop P2P network
	if n.network != nil {
		if err := n.network.Stop(); err != nil {
			log.Error().Err(err).Msg("Error stopping network")
		}
	}

	// Stop mempool
	n.mempool.Stop()

	// Stop consensus
	n.consensus.Stop()

	// Close storage
	if err := n.storage.Close(); err != nil {
		log.Error().Err(err).Msg("Error closing storage")
	}

	log.Info().Msg("Node stopped")
	return nil
}

// eventLoop handles main event processing
func (n *Node) eventLoop() {
	log.Info().Msg("Event loop started")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			log.Info().Msg("Event loop shutting down")
			return

		case <-ticker.C:
			// Process mempool transactions
			n.processMempool()

			// Check for finality
			n.checkFinality()

			// Broadcast events
			n.broadcastEvents()
		}
	}
}

// processMempool creates events from pending transactions
func (n *Node) processMempool() {
	pending := n.mempool.GetPending(100)
	if len(pending) == 0 {
		return
	}

	log.Debug().Int("count", len(pending)).Msg("Processing mempool transactions")

	// Create event with transactions
	event := &pb.Event{
		Id:        generateEventID(),
		Creator:   getNodeCreator(n.config),
		Timestamp: time.Now().UnixNano(),
		Round:     n.consensus.CurrentRound(),
		Transactions: pending,
	}

	// Add to DAG
	if err := n.dag.AddEvent(event); err != nil {
		log.Error().Err(err).Msg("Failed to add event to DAG")
		return
	}

	// Check finality
	n.consensus.CheckFinality(event.Id)

	// Remove processed transactions
	for _, tx := range pending {
		n.mempool.Remove(tx.Id)
	}
}

// checkFinality processes finality events
func (n *Node) checkFinality() {
	latestRound := n.consensus.CurrentRound()

	for round := uint64(0); round <= latestRound; round++ {
		witnesses := n.dag.GetWitnesses(round)

		for _, witness := range witnesses {
			n.consensus.CheckFinality(witness.Id)
		}
	}
}

// broadcastEvents broadcasts events to the network
func (n *Node) broadcastEvents() {
	if n.network == nil {
		return
	}

	// In production, would broadcast newly created events
	// For now, just log periodically
	log.Debug().Int("event_count", n.dag.EventsCount()).Msg("Network status")
}

// generateEventID creates a unique event ID
func generateEventID() string {
	return fmt.Sprintf("event-%d", time.Now().UnixNano())
}

// getNodeCreator returns the creator identifier for this node
func getNodeCreator(cfg *config.Config) string {
	if cfg.Validator.Enabled && cfg.Validator.Address != "" {
		return cfg.Validator.Address
	}
	return "anonymous-node"
}

// loadConfig loads configuration from file or creates default
func loadConfig() (*config.Config, error) {
	const configPath = "config.toml"

	// Check if config exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Info().Msg("No config file found, creating default devnet config")
		cfg := config.GenerateNetworkConfig(config.NetworkTypeDevnet)

		if err := config.SaveConfig(cfg, configPath); err != nil {
			return nil, err
		}
		log.Info().Str("path", configPath).Msg("Default configuration created")
	}

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	log.Info().
		Str("network_id", cfg.Network.NetworkID).
		Str("chain_id", cfg.Network.ChainID).
		Str("data_dir", cfg.GetFullDataDir()).
		Msg("Configuration loaded")

	return cfg, nil
}

// createDataDirectories creates all required data directories
func createDataDirectories(cfg *config.Config) error {
	dataDir := cfg.GetFullDataDir()
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, cfg.Storage.Path),
		filepath.Join(dataDir, "logs"),
		filepath.Join(dataDir, "keystore"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return errors.Wrapf(err, "failed to create directory: %s", dir)
		}
	}

	log.Debug().Str("data_dir", dataDir).Msg("Data directories created")
	return nil
}

// handleSignals sets up signal handling for graceful shutdown
func handleSignals(sigChan chan os.Signal, cancel context.CancelFunc) {
	sig := <-sigChan
	log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
	cancel()
}