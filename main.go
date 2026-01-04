//go:build ignore
// +build ignore

// Package main provides a production-ready entry point for development
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/api"
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/network"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	version = "1.0.0"
	appName = "ndagd"
)

func main() {
	// Initialize logging
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	log.Info().Str("app", appName).Str("version", version).Msg("Starting NDAG Coin")

	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	log.Info().
		Str("network", cfg.Network.NetworkID).
		Str("chain", cfg.Network.ChainID).
		Str("data_dir", cfg.GetFullDataDir()).
		Msg("Configuration loaded")

	// Create data directories
	if err := createDataDirectories(cfg); err != nil {
		log.Fatal().Err(err).Msg("Failed to create data directories")
	}

	// Initialize context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go handleSignals(sigChan, cancel)

	// Initialize node
	node, err := initializeNode(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize node")
	}

	log.Info().Msg("Node initialized successfully")

	// Start node
	if err := node.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start node")
	}

	log.Info().Msg("Node started successfully")

	// Run until shutdown
	<-ctx.Done()

	log.Info().Msg("Shutting down node...")
	
	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := node.Stop(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Error during shutdown")
	} else {
		log.Info().Msg("Node stopped successfully")
	}
}

// Node represents the complete NDAG coin node
type Node struct {
	config    *config.Config
	storage   *storage.Store
	consensus *consensus.ConsensusEngine
	mempool   *mempool.Mempool
	network   *network.P2PNetwork
	api       *api.APIServer
	state     *state.StateMachine
	executor  *state.TransactionExecutor
}

// initializeNode creates and configures all node components
func initializeNode(ctx context.Context, cfg *config.Config) (*Node, error) {
	log.Info().Msg("Initializing node components...")

	// Initialize storage
	storagePath := filepath.Join(cfg.GetFullDataDir(), cfg.Storage.Path)
	store, err := storage.NewStore(storagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	log.Debug().Str("path", storagePath).Msg("Storage initialized")

	// Initialize consensus DAG
	dag := consensus.NewDAG()

	// Initialize consensus engine
	consensusEngine := consensus.NewConsensusEngine(dag)
	log.Debug().Msg("Consensus engine initialized")

	// Initialize mempool
	mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
	log.Debug().Msg("Mempool initialized")

	// Initialize state machine
	stateMachine := state.NewStateMachine(store, consensusEngine)
	log.Debug().Msg("State machine initialized")

	// Initialize transaction executor
	executor := state.NewTransactionExecutor(stateMachine, store, consensusEngine)
	log.Debug().Msg("Transaction executor initialized")

	// Initialize network (if enabled)
	var p2pNetwork *network.P2PNetwork
	if cfg.P2P.Enabled {
		p2pNetwork, err = network.NewP2PNetwork(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize P2P network: %w", err)
		}
		log.Debug().Msg("P2P network initialized")
	} else {
		log.Warn().Msg("P2P network disabled")
	}

	// Initialize API server (if enabled)
	var apiServer *api.APIServer
	if cfg.API.Enabled {
		apiServer = api.NewAPIServer(cfg, store, mp, consensusEngine, stateMachine)
		log.Debug().Msg("API server initialized")
	} else {
		log.Warn().Msg("API server disabled")
	}

	node := &Node{
		config:    cfg,
		storage:   store,
		consensus: consensusEngine,
		mempool:   mp,
		network:   p2pNetwork,
		api:       apiServer,
		state:     stateMachine,
		executor:  executor,
	}

	return node, nil
}

// Start starts all node components
func (n *Node) Start() error {
	log.Info().Msg("Starting node components...")

	// Start consensus
	go func() {
		if err := n.consensus.Start(context.Background()); err != nil {
			log.Error().Err(err).Msg("Consensus engine error")
		}
	}()
	log.Debug().Msg("Consensus engine started")

	// Start mempool
	go func() {
		if err := n.mempool.Start(context.Background()); err != nil {
			log.Error().Err(err).Msg("Mempool error")
		}
	}()
	log.Debug().Msg("Mempool started")

	// Start API server
	if n.api != nil {
		go func() {
			if err := n.api.Start(context.Background()); err != nil {
				log.Error().Err(err).Msg("API server error")
			}
		}()
		log.Debug().Msg("API server started")
	}

	// Start P2P network
	if n.network != nil {
		go func() {
			if err := n.network.Start(context.Background()); err != nil {
				log.Error().Err(err).Msg("P2P network error")
			}
		}()
		log.Debug().Msg("P2P network started")
	}

	// Start main event loop
	go n.eventLoop()
	log.Debug().Msg("Event loop started")

	return nil
}

// Stop gracefully shuts down all components
func (n *Node) Stop(ctx context.Context) error {
	log.Info().Msg("Stopping node components...")

	// Stop API server
	if n.api != nil {
		if err := n.api.Stop(); err != nil {
			log.Error().Err(err).Msg("Error stopping API server")
		}
		log.Debug().Msg("API server stopped")
	}

	// Stop P2P network
	if n.network != nil {
		if err := n.network.Stop(); err != nil {
			log.Error().Err(err).Msg("Error stopping P2P network")
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
		log.Error().Err(err).Msg("Error closing storage")
	}
	log.Debug().Msg("Storage closed")

	return nil
}

// eventLoop handles main event coordination
func (n *Node) eventLoop() {
	log.Info().Msg("Event loop started")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.config.Context().Done():
			log.Info().Msg("Event loop shutting down")
			return
			
		case <-ticker.C:
			// Periodic tasks
			n.processMempool()
			n.checkFinality()
			n.broadcastEvents()
			
		case event := <-n.network.GetEventChannel():
			// Handle new events from network
			if err := n.consensus.dag.AddEvent(event); err != nil {
				log.Warn().Err(err).Str("event_id", event.Id).Msg("Failed to add network event")
			} else {
				log.Debug().Str("event_id", event.Id).Msg("Added network event")
			}
			
		case tx := <-n.mempool.GetTxChannel():
			// Handle new transactions
			log.Debug().Str("tx_id", tx.Id).Msg("Processing mempool transaction")
			
		case finality := <-n.consensus.GetFinalityChannel():
			// Handle finalized events
			log.Debug().
				Str("event_id", finality.EventID).
				Uint64("round", finality.Round).
				Bool("famous", finality.Famous).
				Msg("Event finalized")
			
			// Execute transactions in finalized events
			if event, exists := n.consensus.dag.GetEvent(finality.EventID); exists {
				for _, tx := range event.Transactions {
					result, err := n.executor.Execute(tx)
					if err != nil {
						log.Error().Err(err).Str("tx_id", tx.Id).Msg("Transaction execution failed")
					} else if result.Success {
						log.Debug().Str("tx_id", tx.Id).Msg("Transaction executed successfully")
					}
				}
			}
		}
	}
}

func (n *Node) processMempool() {
	// Get pending transactions
	pending := n.mempool.GetPending(100)
	
	if len(pending) > 0 {
		log.Debug().Int("count", len(pending)).Msg("Processing mempool transactions")
		
		// Create and broadcast event with transactions
		event := &pb.Event{
			Id:        generateEventID(),
			Creator:   "node",
			Timestamp: time.Now().UnixNano(),
			Round:     n.consensus.CurrentRound(),
			Transactions: pending,
		}
		
		// Sign and add event
		if err := event.Sign(); err != nil {
			log.Error().Err(err).Msg("Failed to sign event")
			return
		}
		
		if err := n.consensus.dag.AddEvent(event); err != nil {
			log.Error().Err(err).Msg("Failed to add event")
			return
		}
		
		// Broadcast to network
		if n.network != nil {
			if err := n.network.BroadcastEvent(event); err != nil {
				log.Warn().Err(err).Msg("Failed to broadcast event")
			}
		}
		
		// Remove processed transactions from mempool
		for _, tx := range pending {
			n.mempool.Remove(tx.Id)
		}
	}
}

func (n *Node) checkFinality() {
	// Check for finalized events
	latestRound := n.consensus.CurrentRound()
	
	for round := uint64(0); round <= latestRound; round++ {
		witnesses := n.consensus.dag.GetWitnesses(round)
		
		for _, witness := range witnesses {
			n.consensus.CheckFinality(witness.Id)
		}
	}
}

func (n *Node) broadcastEvents() {
	// Broadcast newly created events if we're a validator
	if !n.config.IsValidator() {
		return
	}
	
	// In production, would broadcast events created by this validator
}

// Helper functions

func loadConfig() (*config.Config, error) {
	configPath := "config.toml"
	
	// If config doesn't exist, create default devnet config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Info().Msg("No config file found, creating default devnet config")
		cfg := config.GenerateNetworkConfig(config.NetworkTypeDevnet)
		
		// Save config
		if err := config.SaveConfig(cfg, configPath); err != nil {
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
		
		return cfg, nil
	}
	
	// Load existing config
	return config.LoadConfig(configPath)
}

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
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		log.Debug().Str("dir", dir).Msg("Created directory")
	}
	
	return nil
}

func handleSignals(sigChan chan os.Signal, cancel context.CancelFunc) {
	<-sigChan
	log.Info().Msg("Received shutdown signal")
	cancel()
}

func generateEventID() string {
	return fmt.Sprintf("event-%d-%d", time.Now().UnixNano(), time.Now().Unix())
}

// Temporary stub for config context method
func (cfg *config.Config) Context() context.Context {
	return context.Background()
}