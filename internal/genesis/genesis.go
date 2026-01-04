package genesis

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/ndag/ndagcoin/pkg/pb"
)

type NetworkType string

const (
	NetworkTypeMainnet NetworkType = "mainnet"
	NetworkTypeTestnet NetworkType = "testnet"
	NetworkTypeDevnet  NetworkType = "devnet"
)

type GenesisConfig struct {
	Network        NetworkType
	ChainID        string
	NetworkID      string
	InitialSupply  uint64
	GenesisTime    int64
	TreasuryAddr   string
	Validators     []ValidatorInfo
	TokenRegistry  map[string]*pb.Token
	AccountStates  map[string]*AccountInfo
}

type ValidatorInfo struct {
	Address            string
	PublicKey          []byte
	Stake              uint64
	CommissionRate     uint64
	MaxCommissionRate  uint64
	CommissionChangeRate uint64
	Moniker            string
	Identity           string
	Website            string
	Details            string
}

type AccountInfo struct {
	Address   string
	Balance   uint64
	Nonce     uint64
	PublicKey []byte
}

// GenerateGenesis creates a genesis block based on network type
func GenerateGenesis(config GenesisConfig) (*pb.Event, error) {
	// Validate configuration
	if err := validateGenesisConfig(config); err != nil {
		return nil, fmt.Errorf("invalid genesis config: %w", err)
	}

	// Create treasury account
	treasuryAccount := &pb.Account{
		Address:   config.TreasuryAddr,
		Balance:   config.InitialSupply,
		Nonce:     0,
		PublicKey: []byte{},
	}

	// Create validator set
	validatorSet := &pb.ValidatorSet{
		Validators: []*pb.Validator{},
		TotalStake: 0,
	}

	for _, valInfo := range config.Validators {
		validator := &pb.Validator{
			Address:              valInfo.Address,
			PublicKey:            valInfo.PublicKey,
			Stake:                valInfo.Stake,
			CommissionRate:       valInfo.CommissionRate,
			MaxCommissionRate:    valInfo.MaxCommissionRate,
			CommissionChangeRate: valInfo.CommissionChangeRate,
			Moniker:              valInfo.Moniker,
			Identity:             valInfo.Identity,
			Website:              valInfo.Website,
			Details:              valInfo.Details,
			Jailed:               false,
			VotingPower:          valInfo.Stake,
		}
		validatorSet.Validators = append(validatorSet.Validators, validator)
		validatorSet.TotalStake += valInfo.Stake
	}

	// Set quorum threshold (2/3 of total stake)
	validatorSet.QuorumThreshold = (validatorSet.TotalStake * 2) / 3

	// Create account states
	accountStates := &pb.AccountStates{
		Accounts: []*pb.Account{treasuryAccount},
	}

	// Add other accounts
	for _, accInfo := range config.AccountStates {
		account := &pb.Account{
			Address:   accInfo.Address,
			Balance:   accInfo.Balance,
			Nonce:     accInfo.Nonce,
			PublicKey: accInfo.PublicKey,
		}
		accountStates.Accounts = append(accountStates.Accounts, account)
	}

	// Create token registry if not provided
	if config.TokenRegistry == nil {
		config.TokenRegistry = make(map[string]*pb.Token)
	}

	// Calculate state hash
	stateHash := calculateGenesisStateHash(accountStates, validatorSet, config.TokenRegistry)

	// Create genesis transaction
	genesisTx := &pb.Transaction{
		Id:        generateGenesisTxID(config.NetworkID, config.GenesisTime),
		ChainId:   config.ChainID,
		NetworkId: config.NetworkID,
		Nonce:     0,
		Fee:       0,
		GasLimit:  10000000,
		Timestamp: config.GenesisTime,
		Payload: &pb.Transaction_GenesisPayload{
			GenesisPayload: &pb.GenesisPayload{
				Treasury:      config.TreasuryAddr,
				InitialSupply: config.InitialSupply,
				ValidatorSet:  validatorSet,
				AccountStates: accountStates,
				TokenRegistry: &pb.TokenRegistry{
					Tokens: config.TokenRegistry,
				},
				StateHash: stateHash,
			},
		},
	}

	// Create genesis event
	genesisEvent := &pb.Event{
		Id:           generateGenesisEventID(config.NetworkID, config.GenesisTime),
		Creator:      "genesis",
		Parents:      []string{},
		Timestamp:    config.GenesisTime,
		Round:        0,
		Signature:    []byte{},
		Transactions: []*pb.Transaction{genesisTx},
		VrfProof:     []byte{},
		RewardClaim: &pb.RewardClaim{
			Validator: "genesis",
			Amount:    0,
			Epoch:     0,
			Proof:     []byte{},
		},
		StateHash: stateHash,
		Metadata: &pb.EventMetadata{
			IsWitness:    true,
			IsFamous:     true,
			IsFinalized:  true,
			RoundReceived: 0,
		},
	}

	return genesisEvent, nil
}

// CreateDevnetGenesis creates a development network genesis
func CreateDevnetGenesis() (*pb.Event, error) {
	// Create a single validator for devnet
	pubKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}

	config := GenesisConfig{
		Network:       NetworkTypeDevnet,
		ChainID:       "ndag-devnet-v1",
		NetworkID:     "ndag-devnet",
		InitialSupply: 100000000000000, // 100M NDAG
		GenesisTime:   time.Now().UnixNano(),
		TreasuryAddr:  "ndag1devnet000000000000000000000000000000",
		Validators: []ValidatorInfo{
			{
				Address:              fmt.Sprintf("ndag%s", hex.EncodeToString(pubKey[:8])),
				PublicKey:            pubKey,
				Stake:                10000000, // 10 NDAG
				CommissionRate:       10000000, // 10%
				MaxCommissionRate:    20000000, // 20%
				CommissionChangeRate: 500000,   // 0.5%
				Moniker:              "devnet-validator-1",
				Identity:             "", // Optional identity
				Website:              "",
				Details:              "Development network validator",
			},
		},
		AccountStates: make(map[string]*AccountInfo),
		TokenRegistry: make(map[string]*pb.Token),
	}

	// Add some test accounts
	for i := 0; i < 5; i++ {
		pubKey, _, _ := ed25519.GenerateKey(nil)
		addr := fmt.Sprintf("ndag%s", hex.EncodeToString(pubKey[:8]))
		config.AccountStates[addr] = &AccountInfo{
			Address:   addr,
			Balance:   10000000000, // 10K NDAG
			Nonce:     0,
			PublicKey: pubKey,
		}
	}

	return GenerateGenesis(config)
}

// CreateTestnetGenesis creates a testnet genesis
func CreateTestnetGenesis(validators []ValidatorInfo) (*pb.Event, error) {
	config := GenesisConfig{
		Network:       NetworkTypeTestnet,
		ChainID:       "ndag-testnet-v1",
		NetworkID:     "ndag-testnet",
		InitialSupply: 100000000000000, // 100M NDAG
		GenesisTime:   time.Now().UnixNano(),
		TreasuryAddr:  "ndag1testnet00000000000000000000000000000",
		Validators:    validators,
		AccountStates: make(map[string]*AccountInfo),
		TokenRegistry: make(map[string]*pb.Token),
	}

	return GenerateGenesis(config)
}

// CreateMainnetGenesis creates a mainnet genesis
func CreateMainnetGenesis(validators []ValidatorInfo) (*pb.Event, error) {
	config := GenesisConfig{
		Network:       NetworkTypeMainnet,
		ChainID:       "ndag-mainnet-v1",
		NetworkID:     "ndag-mainnet",
		InitialSupply: 100000000000000, // 100M NDAG
		GenesisTime:   time.Now().UnixNano(),
		TreasuryAddr:  "ndag1mainnet00000000000000000000000000000",
		Validators:    validators,
		AccountStates: make(map[string]*AccountInfo),
		TokenRegistry: make(map[string]*pb.Token),
	}

	return GenerateGenesis(config)
}

// LoadGenesisFromFile loads genesis from a file
func LoadGenesisFromFile(path string) (*pb.Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var genesis pb.Event
	err = json.Unmarshal(data, &genesis)
	if err != nil {
		return nil, err
	}

	return &genesis, nil
}

// SaveGenesisToFile saves genesis to a file
func SaveGenesisToFile(genesis *pb.Event, path string) error {
	data, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// Helper functions

func validateGenesisConfig(config GenesisConfig) error {
	if config.ChainID == "" {
		return fmt.Errorf("chain_id cannot be empty")
	}
	if config.NetworkID == "" {
		return fmt.Errorf("network_id cannot be empty")
	}
	if config.InitialSupply == 0 {
		return fmt.Errorf("initial_supply must be greater than 0")
	}
	if config.TreasuryAddr == "" {
		return fmt.Errorf("treasury_addr cannot be empty")
	}
	if config.GenesisTime <= 0 {
		return fmt.Errorf("genesis_time must be positive")
	}
	return nil
}

func calculateGenesisStateHash(
	accountStates *pb.AccountStates,
	validatorSet *pb.ValidatorSet,
	tokenRegistry map[string]*pb.Token,
) []byte {
	// Simple hash calculation - in production, use a proper Merkle tree
	data := fmt.Sprintf("%v|%v|%v", accountStates, validatorSet, tokenRegistry)
	return []byte(data)[:32] // Simplified
}

func generateGenesisTxID(networkID string, timestamp int64) string {
	data := fmt.Sprintf("genesis-tx-%s-%d", networkID, timestamp)
	return hashData([]byte(data))
}

func generateGenesisEventID(networkID string, timestamp int64) string {
	data := fmt.Sprintf("genesis-event-%s-%d", networkID, timestamp)
	return hashData([]byte(data))
}

func hashData(data []byte) string {
	// Use a simple hash for now
	hash := make([]byte, 32)
	for i := 0; i < len(data) && i < 32; i++ {
		hash[i] = data[i]
	}
	return hex.EncodeToString(hash)
}

// ValidateGenesis validates a genesis event
func ValidateGenesis(genesis *pb.Event) error {
	if genesis.Id == "" {
		return fmt.Errorf("genesis event missing ID")
	}
	if genesis.Round != 0 {
		return fmt.Errorf("genesis round must be 0")
	}
	if len(genesis.Transactions) != 1 {
		return fmt.Errorf("genesis must have exactly 1 transaction")
	}
	
	genesisTx := genesis.Transactions[0]
	payload, ok := genesisTx.Payload.(*pb.Transaction_GenesisPayload)
	if !ok {
		return fmt.Errorf("genesis transaction must have GenesisPayload")
	}
	
	if payload.GenesisPayload.InitialSupply == 0 {
		return fmt.Errorf("genesis supply must be > 0")
	}
	
	if payload.GenesisPayload.ValidatorSet == nil {
		return fmt.Errorf("genesis missing validator set")
	}
	
	if payload.GenesisPayload.AccountStates == nil {
		return fmt.Errorf("genesis missing account states")
	}
	
	return nil
}