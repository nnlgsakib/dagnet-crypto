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
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/network"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	version = "1.0.0"
	commit  = "unknown"
	date    = "unknown"

	rootCmd = &cobra.Command{
		Use:     "ndagd",
		Short:   "NDAG Coin node daemon",
		Long:    `Production-ready NDAG Coin node daemon with DAG consensus, native assets, and staking`,
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	}

	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Start the NDAG node",
		Long:  `Start the NDAG Coin node with the specified configuration`,
		RunE:  runStart,
	}

	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Initialize node configuration",
		Long:  `Initialize the node with configuration files and data directories`,
		RunE:  runInit,
	}

	validateCmd = &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration",
		Long:  `Validate the node configuration for correctness`,
		RunE:  runValidate,
	}
)

func init() {
	rootCmd.AddCommand(startCmd, initCmd, validateCmd)
	startCmd.Flags().StringP("config", "c", "config.toml", "Configuration file path")
	initCmd.Flags().StringP("network", "n", "devnet", "Network type (mainnet, testnet, devnet)")
	initCmd.Flags().StringP("output", "o", "config.toml", "Output configuration path")
}

func main() {
	// Initialize logging
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// Always show startup message
	log.Info().Str("app", "ndagd").Str("version", version).Msg("Starting NDAG Coin Node")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("Failed to execute command")
	}
}

func runStart(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	log.Info().
		Str("network", cfg.Network.NetworkID).
		Str("chain", cfg.Network.ChainID).
		Str("data_dir", cfg.GetFullDataDir()).
		Bool("validator", cfg.IsValidator()).
		Msg("Configuration loaded")

	// Create data directories
	if err := createDataDirectories(cfg); err != nil {
		return fmt.Errorf("failed to create data directories: %w", err)
	}

	// Setup context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go handleShutdown(sigChan, cancel)

	// Initialize and start node
	node, err := NewNode(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize node: %w", err)
	}

	log.Info().Msg("Node initialized successfully")

	if err := node.Start(); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	log.Info().Msg("✓ Node started successfully")
	log.Info().Msg("Node is ready to process transactions and events")
	log.Info().Msg("Press Ctrl+C to shutdown gracefully")

	// Wait for shutdown signal
	<-ctx.Done()

	log.Info().Msg("Initiating graceful shutdown...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := node.Stop(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Error during shutdown")
		return err
	}

	log.Info().Msg("✓ Node stopped successfully")
	return nil
}

func runInit(cmd *cobra.Command, args []string) error {
	network, _ := cmd.Flags().GetString("network")
	output, _ := cmd.Flags().GetString("output")

	// Parse network type
	var netType config.NetworkType
	switch network {
	case "mainnet":
		netType = config.NetworkTypeMainnet
	case "testnet":
		netType = config.NetworkTypeTestnet
	default:
		netType = config.NetworkTypeDevnet
	}

	log.Info().Str("network", network).Msg("Initializing node configuration")

	// Generate configuration
	cfg := config.GenerateNetworkConfig(netType)

	// Create data directories
	if err := createDataDirectories(cfg); err != nil {
		return fmt.Errorf("failed to create data directories: %w", err)
	}

	// Save configuration
	if err := config.SaveConfig(cfg, output); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	log.Info().
		Str("config", output).
		Str("network", cfg.Network.NetworkID).
		Str("chain", cfg.Network.ChainID).
		Str("data_dir", cfg.GetFullDataDir()).
		Msg("✓ Node initialized successfully")

	// Show next steps
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Review configuration: %s\n", output)
	fmt.Printf("  2. Customize settings as needed\n")
	fmt.Printf("  3. Start the node: ndagd start\n")
	fmt.Printf("  4. Use ndagctl for interactions\n")

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
		return fmt.Errorf("validation failed: %w", err)
	}

	log.Info().Msg("✓ Configuration is valid")

	// Show configuration summary
	fmt.Println("\nConfiguration Summary:")
	fmt.Printf("  Network ID: %s\n", cfg.Network.NetworkID)
	fmt.Printf("  Chain ID: %s\n", cfg.Network.ChainID)
	fmt.Printf("  Data Directory: %s\n", cfg.GetFullDataDir())
	fmt.Printf("  Validator Mode: %v\n", cfg.IsValidator())
	fmt.Printf("  P2P Enabled: %v\n", cfg.P2P.Enabled)
	fmt.Printf("  API Enabled: %v\n", cfg.API.Enabled)

	return nil
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
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
		log.Debug().Str("dir", dir).Msg("Created directory")
	}

	return nil
}

func handleShutdown(sigChan chan os.Signal, cancel context.CancelFunc) {
	sig := <-sigChan
	log.Info().Str("signal", sig.String()).Msg("Shutdown signal received")
	cancel()
}