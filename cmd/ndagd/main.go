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
    "github.com/ndag/ndagcoin/pkg/crypto"
    "github.com/ndag/ndagcoin/pkg/mempool"
    "github.com/ndag/ndagcoin/pkg/network"
    "github.com/ndag/ndagcoin/pkg/state"
    "github.com/ndag/ndagcoin/pkg/storage"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    "github.com/spf13/cobra"
)

var (
    version = "0.1.0"
    commit  = "unknown"
    date    = "unknown"
)

func main() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

var rootCmd = &cobra.Command{
    Use:   "ndagd",
    Short: "NDAG Coin node daemon",
    Long:  `NDAG Coin is a production-grade DAG blockchain with native assets, staking, and fast finality.`,
    Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
}

var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start the NDAG node",
    Long:  `Start the NDAG Coin node daemon with the specified configuration.`,
    RunE:  runStart,
}

var initCmd = &cobra.Command{
    Use:   "init",
    Short: "Initialize the node with configuration",
    Long:  `Initialize the node configuration and data directories.`,
    RunE:  runInit,
}

var validateCmd = &cobra.Command{
    Use:   "validate",
    Short: "Validate the configuration file",
    Long:  `Validate the configuration file for correctness.`,
    RunE:  runValidate,
}

func init() {
    rootCmd.AddCommand(startCmd)
    rootCmd.AddCommand(initCmd)
    rootCmd.AddCommand(validateCmd)

    startCmd.Flags().StringP("config", "c", "config.toml", "Path to configuration file")
    
    initCmd.Flags().StringP("network", "n", "devnet", "Network type (mainnet, testnet, devnet)")
    initCmd.Flags().StringP("output", "o", "config.toml", "Output configuration file path")
    initCmd.Flags().StringP("data-dir", "d", "", "Data directory path")
}

type Node struct {
    config     *config.Config
    storage    *storage.Store
    consensus  *consensus.ConsensusEngine
    mempool    *mempool.Mempool
    network    *network.P2PNetwork
    apiServer  *api.Server
    state      *state.StateMachine
    crypto     *crypto.Keypair
    ctx        context.Context
    cancel     context.CancelFunc
}

func runStart(cmd *cobra.Command, args []string) error {
    configPath, _ := cmd.Flags().GetString("config")
    
    // Load configuration
    cfg, err := config.LoadConfig(configPath)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    
    if err := cfg.Validate(); err != nil {
        return fmt.Errorf("invalid configuration: %w", err)
    }
    
    fmt.Printf("Starting NDAG Coin node version %s\n", version)
    fmt.Printf("Network: %s (%s)\n", cfg.Network.NetworkID, cfg.Network.ChainID)
    
    // Create context
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Initialize node components
    node, err := initializeNode(ctx, cfg)
    if err != nil {
        return fmt.Errorf("failed to initialize node: %w", err)
    }
    node.cancel = cancel
    
    // Setup signal handling
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Start node
    if err := node.Start(); err != nil {
        return fmt.Errorf("failed to start node: %w", err)
    }
    
    fmt.Println("Node started successfully")
    
    // Wait for shutdown signal
    select {
    case sig := <-sigChan:
        fmt.Printf("Received signal: %v\n", sig)
    case <-ctx.Done():
        fmt.Println("Context cancelled")
    }
    
    // Graceful shutdown
    fmt.Println("Shutting down node...")
    if err := node.Stop(); err != nil {
        return fmt.Errorf("error during shutdown: %w", err)
    }
    
    fmt.Println("Node stopped successfully")
    return nil
}

func runInit(cmd *cobra.Command, args []string) error {
    network, _ := cmd.Flags().GetString("network")
    output, _ := cmd.Flags().GetString("output")
    dataDir, _ := cmd.Flags().GetString("data-dir")
    
    // Determine network type
    var netType config.NetworkType
    switch network {
    case "mainnet":
        netType = config.NetworkTypeMainnet
    case "testnet":
        netType = config.NetworkTypeTestnet
    default:
        netType = config.NetworkTypeDevnet
    }
    
    // Generate config for network
    cfg := config.GenerateNetworkConfig(netType)
    
    // Override data directory if specified
    if dataDir != "" {
        cfg.Network.DataDir = dataDir
    }
    
    // Create data directories
    dataPath := cfg.GetFullDataDir()
    if err := os.MkdirAll(dataPath, 0755); err != nil {
        return fmt.Errorf("failed to create data directory: %w", err)
    }
    
    if err := os.MkdirAll(filepath.Join(dataPath, cfg.Storage.Path), 0755); err != nil {
        return fmt.Errorf("failed to create storage directory: %w", err)
    }
    
    // Create directories for logs, etc.
    if err := os.MkdirAll(filepath.Join(dataPath, "logs"), 0755); err != nil {
        return fmt.Errorf("failed to create logs directory: %w", err)
    }
    
    // Save configuration
    if err := config.SaveConfig(cfg, output); err != nil {
        return fmt.Errorf("failed to save config: %w", err)
    }
    
    fmt.Printf("Node initialized successfully!\n")
    fmt.Printf("Network: %s (%s)\n", cfg.Network.NetworkID, cfg.Network.ChainID)
    fmt.Printf("Data directory: %s\n", dataPath)
    fmt.Printf("Configuration saved to: %s\n", output)
    
    if netType == config.NetworkTypeDevnet {
        fmt.Println("\nTo create a genesis file:")
        fmt.Println("  ndagctl genesis create --network devnet")
        
        fmt.Println("\nTo start the node:")
        fmt.Println("  ndagd start -c", output)
    }
    
    return nil
}

func runValidate(cmd *cobra.Command, args []string) error {
    if len(args) == 0 {
        return fmt.Errorf("configuration file path required")
    }
    
    configPath := args[0]
    cfg, err := config.LoadConfig(configPath)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    
    if err := cfg.Validate(); err != nil {
        return fmt.Errorf("invalid configuration: %w", err)
    }
    
    fmt.Println("Configuration is valid!")
    fmt.Printf("Network: %s (%s)\n", cfg.Network.NetworkID, cfg.Network.ChainID)
    fmt.Printf("Validator mode: %v\n", cfg.IsValidator())
    fmt.Printf("API enabled: %v\n", cfg.API.Enabled)
    fmt.Printf("P2P enabled: %v\n", cfg.P2P.Enabled)
    
    return nil
}

func initializeNode(ctx context.Context, cfg *config.Config) (*Node, error) {
    node := &Node{
        config: cfg,
        ctx:    ctx,
    }
    
    // Initialize storage
    storagePath := filepath.Join(cfg.GetFullDataDir(), cfg.Storage.Path)
    store, err := storage.NewStore(storagePath)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize storage: %w", err)
    }
    node.storage = store
    
    // Initialize consensus DAG
    dag := consensus.NewDAG()
    
    // Initialize consensus engine
    consensusEngine := consensus.NewConsensusEngine(dag)
    node.consensus = consensusEngine
    
    // Initialize mempool
    mempool := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
    node.mempool = mempool
    
    // Initialize state machine
    stateMachine := state.NewStateMachine(store, consensusEngine)
    node.state = stateMachine
    
    // Initialize crypto (keypair for validator if enabled)
    if cfg.IsValidator() {
        keypair, err := loadValidatorKey(cfg.Validator.KeyFile)
        if err != nil {
            return nil, fmt.Errorf("failed to load validator key: %w", err)
        }
        node.crypto = keypair
    }
    
    // Initialize network (if enabled)
    if cfg.P2P.Enabled {
        p2pNetwork, err := network.NewP2PNetwork(ctx, cfg)
        if err != nil {
            return nil, fmt.Errorf("failed to initialize P2P network: %w", err)
        }
        node.network = p2pNetwork
    }
    
    // Initialize API server (if enabled)
    if cfg.API.Enabled {
        apiServer := api.NewServer(cfg)
        node.apiServer = apiServer
    }
    
    return node, nil
}

func (n *Node) Start() error {
    // Start storage engine
    if n.storage == nil {
        return fmt.Errorf("storage not initialized")
    }
    
    fmt.Println("Starting storage engine...")
    
    // Start consensus engine
    fmt.Println("Starting consensus engine...")
    go n.consensus.Start(n.ctx)
    
    // Start mempool
    fmt.Println("Starting mempool...")
    go n.mempool.Start(n.ctx)
    
    // Start state machine
    fmt.Println("Starting state machine...")
    go n.state.Start(n.ctx)
    
    // Start P2P network (if enabled)
    if n.network != nil {
        fmt.Println("Starting P2P network...")
        if err := n.network.Start(n.ctx); err != nil {
            return fmt.Errorf("failed to start network: %w", err)
        }
    }
    
    // Start API server (if enabled)
    if n.apiServer != nil {
        fmt.Println("Starting API server...")
        go func() {
            if err := n.apiServer.Start(n.ctx); err != nil {
                fmt.Printf("API server error: %v\n", err)
            }
        }()
    }
    
    // Start main event loop
    fmt.Println("Node running...")
    go n.eventLoop()
    
    return nil
}

func (n *Node) Stop() error {
    // Cancel context
    if n.cancel != nil {
        n.cancel()
    }
    
    // Stop API server
    if n.apiServer != nil {
        n.apiServer.Stop()
    }
    
    // Stop network
    if n.network != nil {
        n.network.Stop()
    }
    
    // Stop consensus
    if n.consensus != nil {
        n.consensus.Stop()
    }
    
    // Stop mempool
    if n.mempool != nil {
        n.mempool.Stop()
    }
    
    // Close storage
    if n.storage != nil {
        n.storage.Close()
    }
    
    return nil
}

func (n *Node) eventLoop() {
    fmt.Println("Event loop started")
    
    for {
        select {
        case <-n.ctx.Done():
            fmt.Println("Event loop shutting down")
            return
            
        // Handle new events from network
        case event := <-n.network.GetEventChannel():
            fmt.Printf("Received new event: %s\n", event.Id)
            if err := n.consensus.dag.AddEvent(event); err != nil {
                fmt.Printf("Failed to add event: %v\n", err)
            } else {
                // Check for finality
                n.consensus.CheckFinality(event.Id)
            }
        
        // Handle finality events
        case finalityEvent := <-n.consensus.GetFinalityChannel():
            fmt.Printf("Event finalized: %s (round: %d, famous: %v)\n",
                finalityEvent.EventID, finalityEvent.Round, finalityEvent.Famous)
            
            // Execute transactions
            if event, exists := n.consensus.dag.GetEvent(finalityEvent.EventID); exists {
                for _, tx := range event.Transactions {
                    if err := n.state.ExecuteTransaction(tx); err != nil {
                        fmt.Printf("Failed to execute transaction: %v\n", err)
                    }
                }
            }
        
        // Handle mempool transactions
        case tx := <-n.mempool.GetTxChannel():
            fmt.Printf("Processing mempool transaction: %s\n", tx.Id)
            // Add transaction to next event creation
        }
    }
}

func loadValidatorKey(keyFile string) (*crypto.Keypair, error) {
    if keyFile == "" {
        return nil, fmt.Errorf("validator key file not specified")
    }
    
    // TODO: Implement secure key loading from file
    // For now, generate a temporary key
    return crypto.GenerateKeypair()
}

func filepath.Join(elem ...string) string {
    return elem[0] // Simplified
}