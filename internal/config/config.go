package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ndag/ndagcoin/internal/utils"
	"github.com/pelletier/go-toml"
)

// NetworkType represents the network type
type NetworkType string

const (
	NetworkTypeMainnet NetworkType = "mainnet"
	NetworkTypeTestnet NetworkType = "testnet"
	NetworkTypeDevnet  NetworkType = "devnet"
)

// Config represents the node configuration
type Config struct {
	Network NetworkConfig `toml:"network"`
	P2P     P2PConfig     `toml:"p2p"`
	Consensus ConsensusConfig `toml:"consensus"`
	API     APIConfig     `toml:"api"`
	Storage StorageConfig `toml:"storage"`
	Logging LoggingConfig `toml:"logging"`
	Validator ValidatorConfig `toml:"validator"`
	Genesis GenesisConfig `toml:"genesis"`
}

// NetworkConfig contains network settings
type NetworkConfig struct {
	NetworkID   string      `toml:"network_id"`
	ChainID     string      `toml:"chain_id"`
	DataDir     string      `toml:"data_dir"`
}

// P2PConfig contains P2P networking settings
type P2PConfig struct {
	Enabled         bool     `toml:"enabled"`
	ListenAddress   string   `toml:"listen_address"`
	BootstrapPeers  []string `toml:"bootstrap_peers"`
	MaxPeers        int      `toml:"max_peers"`
	MinPeers        int      `toml:"min_peers"`
	ConnectionTimeout time.Duration `toml:"connection_timeout"`
	EnableNATTraversal bool     `toml:"enable_nat_traversal"`
	EnableRelay      bool     `toml:"enable_relay"`
	EnableMetrics    bool     `toml:"enable_metrics"`
}

// ConsensusConfig contains consensus settings
type ConsensusConfig struct {
	Enabled           bool          `toml:"enabled"`
	RoundDuration     time.Duration `toml:"round_duration"`
	EpochDuration     time.Duration `toml:"epoch_duration"`
	MinValidatorStake uint64        `toml:"min_validator_stake"`
	MaxValidatorStake uint64        `toml:"max_validator_stake"`
	MinGasPrice       uint64        `toml:"min_gas_price"`
	MaxGasPerRound    uint64        `toml:"max_gas_per_round"`
	TargetGasPerRound uint64        `toml:"target_gas_per_round"`
	SlashingEnabled   bool          `toml:"slashing_enabled"`
}

// APIConfig contains API settings
type APIConfig struct {
	Enabled        bool     `toml:"enabled"`
	ListenAddress  string   `toml:"listen_address"`
	EnableREST     bool     `toml:"enable_rest"`
	RESTListenAddress string `toml:"rest_listen_address"`
	EnableGRPC     bool     `toml:"enable_grpc"`
	GRPCListenAddress string `toml:"grpc_listen_address"`
	CORSOrigins    []string `toml:"cors_origins"`
	EnableMetrics  bool     `toml:"enable_metrics"`
}

// StorageConfig contains storage settings
type StorageConfig struct {
	Backend            string        `toml:"backend"`
	Path               string        `toml:"path"`
	EnablePruning      bool          `toml:"enable_pruning"`
	PruningKeepRecent  time.Duration `toml:"pruning_keep_recent"`
	PruningInterval    time.Duration `toml:"pruning_interval"`
	CompactionInterval time.Duration `toml:"compaction_interval"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level      string `toml:"level"`
	Format     string `toml:"format"`
	File       string `toml:"file"`
	MaxSize    int    `toml:"max_size"`
	MaxBackups int    `toml:"max_backups"`
	MaxAge     int    `toml:"max_age"`
	Compress   bool   `toml:"compress"`
}

// ValidatorConfig contains validator settings
type ValidatorConfig struct {
	Enabled       bool   `toml:"enabled"`
	Address       string `toml:"address"`
	CommissionRate uint64 `toml:"commission_rate"`
	KeyFile       string `toml:"key_file"`
	MinSelfStake  uint64 `toml:"min_self_stake"`
}

// GenesisConfig contains genesis settings
type GenesisConfig struct {
	GenesisFile       string `toml:"genesis_file"`
	InitialSupply     uint64 `toml:"initial_supply"`
	GenesisTime       time.Time `toml:"genesis_time"`
	ChainID           string `toml:"chain_id"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Network: NetworkConfig{
			NetworkID: "ndag-devnet",
			ChainID:   "ndag-devnet-v1",
			DataDir:   ".ndag",
		},
		P2P: P2PConfig{
			Enabled:         true,
			ListenAddress:   "/ip4/0.0.0.0/tcp/9000",
			BootstrapPeers:  []string{},
			MaxPeers:        100,
			MinPeers:        5,
			ConnectionTimeout: 30 * time.Second,
			EnableNATTraversal: true,
			EnableRelay:      true,
			EnableMetrics:    true,
		},
		Consensus: ConsensusConfig{
			Enabled:           true,
			RoundDuration:     time.Second,
			EpochDuration:     24 * time.Hour,
			MinValidatorStake: 1000000, // 1 NDAG
			MaxValidatorStake: 1000000000, // 1000 NDAG
			MinGasPrice:       1000,
			MaxGasPerRound:    10000000,
			TargetGasPerRound: 8000000,
			SlashingEnabled:   true,
		},
		API: APIConfig{
			Enabled:           true,
			ListenAddress:     ":9090",
			EnableREST:        true,
			RESTListenAddress: ":9091",
			EnableGRPC:        true,
			GRPCListenAddress: ":9092",
			CORSOrigins:       []string{"*"},
			EnableMetrics:     true,
		},
		Storage: StorageConfig{
			Backend:            "leveldb",
			Path:               "data",
			EnablePruning:      true,
			PruningKeepRecent:  24 * time.Hour,
			PruningInterval:    time.Hour,
			CompactionInterval: 2 * time.Hour,
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			File:       "",
			MaxSize:    100,
			MaxBackups: 5,
			MaxAge:     7,
			Compress:   true,
		},
		Validator: ValidatorConfig{
			Enabled:        false,
			Address:        "",
			CommissionRate: 10000000, // 10%
			KeyFile:        "",
			MinSelfStake:   100000000, // 100 NDAG
		},
		Genesis: GenesisConfig{
			GenesisFile:   "genesis.pb",
			InitialSupply: 100000000000000, // 100M NDAG
			ChainID:       "ndag-devnet-v1",
		},
	}
}

// LoadConfig loads configuration from file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := DefaultConfig()
	if err := toml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return config, nil
}

// SaveConfig saves configuration to file
func SaveConfig(config *Config, path string) error {
	data, err := toml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Network validation
	if c.Network.NetworkID == "" {
		return fmt.Errorf("network_id cannot be empty")
	}
	if c.Network.ChainID == "" {
		return fmt.Errorf("chain_id cannot be empty")
	}

	// consensus validation
	if c.Consensus.MinGasPrice == 0 {
		return fmt.Errorf("min_gas_price must be greater than 0")
	}
	if c.Consensus.MaxGasPerRound == 0 {
		return fmt.Errorf("max_gas_per_round must be greater than 0")
	}

	// Storage validation
	if c.Storage.Path == "" {
		return fmt.Errorf("storage path cannot be empty")
	}

	return nil
}

// GetNetworkType returns the network type based on chain_id
func (c *Config) GetNetworkType() NetworkType {
	switch c.Network.ChainID {
	case "ndag-mainnet-v1":
		return NetworkTypeMainnet
	case "ndag-testnet-v1":
		return NetworkTypeTestnet
	default:
		return NetworkTypeDevnet
	}
}

// GetFullDataDir returns the full data directory path
func (c *Config) GetFullDataDir() string {
	if filepath.IsAbs(c.Network.DataDir) {
		return c.Network.DataDir
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, c.Network.DataDir)
}

// IsValidator returns true if this node is configured as a validator
func (c *Config) IsValidator() bool {
	return c.Validator.Enabled && c.Validator.Address != ""
}

// Config files for different networks
const (
	ConfigFileMainnet = "configs/mainnet.toml"
	ConfigFileTestnet = "configs/testnet.toml"
	ConfigFileDevnet  = "configs/devnet.toml"
)

// GenerateNetworkConfig generates configuration for a specific network
func GenerateNetworkConfig(networkType NetworkType) *Config {
	config := DefaultConfig()
	
	switch networkType {
	case NetworkTypeMainnet:
		config.Network.NetworkID = "ndag-mainnet"
		config.Network.ChainID = "ndag-mainnet-v1"
		config.Network.DataDir = ".ndag/mainnet"
		config.Consensus.RoundDuration = time.Second
		config.Consensus.EpochDuration = 7 * 24 * time.Hour // 1 week
		config.Consensus.MinValidatorStake = 100000000000  // 100 NDAG
		config.Consensus.MinGasPrice = 10000
		config.P2P.BootstrapPeers = []string{
			"/dns/bootstrap.ndag.network/tcp/9000/p2p/12D3KooW",
		}
		
	case NetworkTypeTestnet:
		config.Network.NetworkID = "ndag-testnet"
		config.Network.ChainID = "ndag-testnet-v1"
		config.Network.DataDir = ".ndag/testnet"
		config.Consensus.RoundDuration = time.Second
		config.Consensus.EpochDuration = 24 * time.Hour
		config.Consensus.MinValidatorStake = 10000000  // 10 NDAG
		config.Consensus.MinGasPrice = 1000
		config.P2P.BootstrapPeers = []string{
			"/dns/bootstrap-test.ndag.network/tcp/9000/p2p/12D3KooW",
		}
		
	case NetworkTypeDevnet:
		config.Network.NetworkID = "ndag-devnet"
		config.Network.ChainID = "ndag-devnet-v1"
		config.Network.DataDir = ".ndag/devnet"
		config.Consensus.RoundDuration = time.Second
		config.Consensus.EpochDuration = time.Hour
		config.Consensus.MinValidatorStake = 1000000  // 1 NDAG
		config.Consensus.MinGasPrice = 100
		config.P2P.BootstrapPeers = []string{}
	}
	
	return config
}