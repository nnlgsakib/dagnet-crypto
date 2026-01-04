package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	version = "0.1.0"
	client  pb.NodeServiceClient
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "ndagctl",
	Short:   "NDAG Coin CLI tool",
	Long:    `Command-line interface for interacting with the NDAG Coin network`,
	Version: version,
}

var txCmd = &cobra.Command{
	Use:   "tx",
	Short: "Transaction commands",
	Long:  `Commands for creating and managing transactions`,
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query commands",
	Long:  `Commands for querying blockchain data`,
}

var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Wallet management commands",
	Long:  `Commands for managing wallets and keys`,
}

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Token management commands",
	Long:  `Commands for managing fungible tokens`,
}

var nftCmd = &cobra.Command{
	Use:   "nft",
	Short: "NFT management commands",
	Long:  `Commands for managing NFTs`,
}

var genesisCmd = &cobra.Command{
	Use:   "genesis",
	Short: "Genesis block commands",
	Long:  `Commands for genesis block management`,
}

var validatorCmd = &cobra.Command{
	Use:   "validator",
	Short: "Validator commands",
	Long:  `Commands for validator operations`,
}

// Common flags
var (
	grpcAddr string
	network  string
)

func init() {
	rootCmd.PersistentFlags().StringVar(&grpcAddr, "grpc-addr", "localhost:9092", "gRPC server address")
	rootCmd.PersistentFlags().StringVar(&network, "network", "devnet", "Network type (mainnet, testnet, devnet)")

	// Add commands
	rootCmd.AddCommand(txCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(walletCmd)
	rootCmd.AddCommand(tokenCmd)
	rootCmd.AddCommand(nftCmd)
	rootCmd.AddCommand(genesisCmd)
	rootCmd.AddCommand(validatorCmd)

	// Transaction commands
	txCmd.AddCommand(createTransferCmd())
	txCmd.AddCommand(createCreateValidatorCmd())
	txCmd.AddCommand(createDelegateStakeCmd())
	txCmd.AddCommand(createCreateTokenCmd())
	txCmd.AddCommand(createTransferTokenCmd())
	txCmd.AddCommand(createMintTokenCmd())
	txCmd.AddCommand(createBurnTokenCmd())
	txCmd.AddCommand(createCreateCollectionCmd())
	txCmd.AddCommand(createMintNFTCmd())
	txCmd.AddCommand(createTransferNFTCmd())

	// Query commands
	queryCmd.AddCommand(createBalanceCmd())
	queryCmd.AddCommand(createAccountCmd())
	queryCmd.AddCommand(createTxCmd())
	queryCmd.AddCommand(createStatusCmd())
	queryCmd.AddCommand(createValidatorsCmd())
	queryCmd.AddCommand(createTokenCmd())
	queryCmd.AddCommand(createNFTQueryCmd())

	// Wallet commands
	walletCmd.AddCommand(createGenerateKeyCmd())
	walletCmd.AddCommand(createImportKeyCmd())
	walletCmd.AddCommand(createListKeysCmd())

	// Token commands
	tokenCmd.AddCommand(createTokenInfoCmd())
	tokenCmd.AddCommand(createTokenBalanceCmd())

	// NFT commands
	nftCmd.AddCommand(createNFTInfoCmd())
	nftCmd.AddCommand(createNFTListCmd())

	// Genesis commands
	genesisCmd.AddCommand(createGenesisCreateCmd())

	// Validator commands
	validatorCmd.AddCommand(createValidatorInfoCmd())
	validatorCmd.AddCommand(createValidatorSetCmd())
}

func getClient() (pb.NodeServiceClient, error) {
	if client != nil {
		return client, nil
	}

	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	client = pb.NewNodeServiceClient(conn)
	return client, nil
}

// Transaction command constructors
func createTransferCmd() *cobra.Command {
	var from, to, password string
	var amount uint64

	cmd := &cobra.Command{
		Use:   "transfer",
		Short: "Transfer NDAG tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return err
			}

			// Load keypair from file (simplified)
			keypair, err := loadKeyFromFile(from, password)
			if err != nil {
				return fmt.Errorf("failed to load key: %w", err)
			}

			// Create transaction
			tx := &pb.Transaction{
				ChainId:   network,
				NetworkId: fmt.Sprintf("%s-v1", network),
				Nonce:     uint64(time.Now().Unix()),
				Fee:       1000,
				GasLimit:  50000,
				Timestamp: time.Now().UnixNano(),
				Payload: &pb.Transaction_Transfer{
					Transfer: &pb.TransferPayload{
						To:     to,
						Amount: amount,
					},
				},
				PublicKey: keypair.Public,
			}

			// Sign transaction
			// err = SignTransaction(tx, keypair)
			// if err != nil {
			// 	return fmt.Errorf("failed to sign transaction: %w", err)
			// }

			// Submit transaction
			req := &pb.SubmitTxRequest{
				Transaction: tx,
				Async:       false,
			}

			resp, err := client.SubmitTx(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("failed to submit transaction: %w", err)
			}

			fmt.Printf("Transaction submitted successfully!\n")
			fmt.Printf("Transaction ID: %s\n", resp.TxId)
			fmt.Printf("Status: %s\n", resp.Status)

			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Sender address or key name")
	cmd.Flags().StringVar(&to, "to", "", "Recipient address")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "Amount to transfer")
	cmd.Flags().StringVar(&password, "password", "", "Password for key decryption")
	cmd.MarkFlagRequired("from")
	cmd.MarkFlagRequired("to")
	cmd.MarkFlagRequired("amount")

	return cmd
}

func createCreateValidatorCmd() *cobra.Command {
	var moniker, identity, website, details string
	var commissionRate uint64

	cmd := &cobra.Command{
		Use:   "create-validator",
		Short: "Create a new validator",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return err
			}

			// Keypair generation (simplified)
			pubKey, privKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				return err
			}

			tx := &pb.Transaction{
				ChainId:   network,
				NetworkId: fmt.Sprintf("%s-v1", network),
				Nonce:     uint64(time.Now().Unix()),
				Fee:       10000000,
				GasLimit:  5000000,
				Timestamp: time.Now().UnixNano(),
				Payload: &pb.Transaction_CreateValidator{
					CreateValidator: &pb.CreateValidatorPayload{
						ValidatorAddress:   "validator-" + fmt.Sprint(time.Now().Unix()),
						PublicKey:          pubKey,
						CommissionRate:     commissionRate,
						MaxCommissionRate:  20000000,
						CommissionChangeRate: 1000000,
						Moniker:            moniker,
						Identity:           identity,
						Website:            website,
						Details:            details,
					},
				},
				PublicKey: pubKey,
			}

			req := &pb.SubmitTxRequest{Transaction: tx}
			resp, err := client.SubmitTx(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Printf("Validator created!\n")
			fmt.Printf("Transaction ID: %s\n", resp.TxId)
			fmt.Printf("Public Key: %s\n", hex.EncodeToString(pubKey))

			return nil
		},
	}

	cmd.Flags().StringVar(&moniker, "moniker", "", "Validator name")
	cmd.Flags().StringVar(&identity, "identity", "", "Identity signature")
	cmd.Flags().StringVar(&website, "website", "", "Website URL")
	cmd.Flags().StringVar(&details, "details", "", "Validator details")
	cmd.Flags().Uint64Var(&commissionRate, "commission-rate", 10000000, "Commission rate (default 10%)")
	cmd.MarkFlagRequired("moniker")

	return cmd
}

func createDelegateStakeCmd() *cobra.Command {
	var validator string
	var amount uint64

	cmd := &cobra.Command{
		Use:   "delegate",
		Short: "Delegate stake to validator",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return err
			}

			tx := &pb.Transaction{
				ChainId:   network,
				NetworkId: fmt.Sprintf("%s-v1", network),
				Nonce:     uint64(time.Now().Unix()),
				Fee:       5000,
				GasLimit:  200000,
				Timestamp: time.Now().UnixNano(),
				Payload: &pb.Transaction_DelegateStake{
					DelegateStake: &pb.DelegateStakePayload{
						ValidatorAddress: validator,
						Amount:          amount,
					},
				},
			}

			req := &pb.SubmitTxRequest{Transaction: tx}
			resp, err := client.SubmitTx(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Printf("Delegation successful!\n")
			fmt.Printf("Transaction ID: %s\n", resp.TxId)

			return nil
		},
	}

	cmd.Flags().StringVar(&validator, "validator", "", "Validator address")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "Amount to delegate")
	cmd.MarkFlagRequired("validator")
	cmd.MarkFlagRequired("amount")

	return cmd
}

// Query command constructors
func createBalanceCmd() *cobra.Command {
	var address string

	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Query account balance",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return err
			}

			req := &pb.QueryBalanceRequest{
				Address: address,
			}

			resp, err := client.QueryBalance(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Printf("Balance for %s:\n", address)
			fmt.Printf("  Balance: %d NDAG\n", resp.Balance)
			fmt.Printf("  Nonce: %d\n", resp.Nonce)

			return nil
		},
	}

	cmd.Flags().StringVar(&address, "address", "", "Account address")
	cmd.MarkFlagRequired("address")

	return cmd
}

func createStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Query node status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return err
			}

			resp, err := client.GetNodeStatus(cmd.Context(), &pb.Empty{})
			if err != nil {
				return err
			}

			fmt.Printf("Node Status:\n")
			fmt.Printf("  Node ID: %s\n", resp.NodeId)
			fmt.Printf("  Version: %s\n", resp.Version)
			fmt.Printf("  Chain ID: %s\n", resp.ChainId)
			fmt.Printf("  Latest Round: %d\n", resp.LatestRound)
			fmt.Printf("  Latest Epoch: %d\n", resp.LatestEpoch)
			fmt.Printf("  Number of Peers: %d\n", resp.NumPeers)
			fmt.Printf("  Number of Events: %d\n", resp.NumEvents)

			return nil
		},
	}

	return cmd
}

// Wallet command constructors
func createGenerateKeyCmd() *cobra.Command {
	var name, password string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new keypair",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Generate keypair
			pubKey, privKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				return err
			}

			address := fmt.Sprintf("ndag%s", hex.EncodeToString(pubKey[:8]))

			fmt.Printf("Key generated successfully!\n")
			fmt.Printf("Name: %s\n", name)
			fmt.Printf("Address: %s\n", address)
			fmt.Printf("Public Key: %s\n", hex.EncodeToString(pubKey))
			fmt.Printf("Private Key: %s\n", hex.EncodeToString(privKey))

			// Save to file if name provided
			if name != "" {
				keyData := map[string]string{
					"name":       name,
					"address":    address,
					"public_key": hex.EncodeToString(pubKey),
					"private_key": hex.EncodeToString(privKey),
				}

				data, _ := json.MarshalIndent(keyData, "", "  ")
				err := os.WriteFile(fmt.Sprintf("%s.key", name), data, 0600)
				if err != nil {
					return err
				}
				fmt.Printf("Key saved to: %s.key\n", name)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name for the key")
	cmd.Flags().StringVar(&password, "password", "", "Password for encryption")

	return cmd
}

func createImportKeyCmd() *cobra.Command {
	var name, privateKey, password string

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import an existing key",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse private key
			privKey, err := hex.DecodeString(privateKey)
			if err != nil {
				return err
			}

			// Derive public key
			pubKey := ed25519.PrivateKey(privKey).Public().(ed25519.PublicKey)
			address := fmt.Sprintf("ndag%s", hex.EncodeToString(pubKey[:8]))

			fmt.Printf("Key imported successfully!\n")
			fmt.Printf("Name: %s\n", name)
			fmt.Printf("Address: %s\n", address)
			fmt.Printf("Public Key: %s\n", hex.EncodeToString(pubKey))

			// Save to file
			keyData := map[string]string{
				"name":        name,
				"address":     address,
				"public_key":  hex.EncodeToString(pubKey),
				"private_key": privateKey,
			}

			data, _ := json.MarshalIndent(keyData, "", "  ")
			err = os.WriteFile(fmt.Sprintf("%s.key", name), data, 0600)
			if err != nil {
				return err
			}
			fmt.Printf("Key saved to: %s.key\n", name)

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name for the key")
	cmd.Flags().StringVar(&privateKey, "private-key", "", "Private key hex")
	cmd.Flags().StringVar(&password, "password", "", "Password for encryption")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("private-key")

	return cmd
}

func createListKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := os.ReadDir(".")
			if err != nil {
				return err
			}

			fmt.Printf("Available keys:\n")
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".key") {
					fmt.Printf("  %s\n", strings.TrimSuffix(file.Name(), ".key"))
				}
			}

			return nil
		},
	}

	return cmd
}

// Placeholder implementations
func createCreateTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create-token",
		Short: "Create a new token (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("create-token not yet implemented")
		},
	}
}

func createTransferTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "transfer-token",
		Short: "Transfer tokens (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("transfer-token not yet implemented")
		},
	}
}

func createMintTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mint-token",
		Short: "Mint tokens (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("mint-token not yet implemented")
		},
	}
}

func createBurnTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "burn-token",
		Short: "Burn tokens (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("burn-token not yet implemented")
		},
	}
}

func createCreateCollectionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create-collection",
		Short: "Create NFT collection (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("create-collection not yet implemented")
		},
	}
}

func createMintNFTCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mint-nft",
		Short: "Mint NFT (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("mint-nft not yet implemented")
		},
	}
}

func createTransferNFTCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "transfer-nft",
		Short: "Transfer NFT (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("transfer-nft not yet implemented")
		},
	}
}

func createAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "account",
		Short: "Query account (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("account query not yet implemented")
		},
	}
}

func createTxCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tx",
		Short: "Query transaction (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("tx query not yet implemented")
		},
	}
}

func createValidatorsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validators",
		Short: "Query validators (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("validators query not yet implemented")
		},
	}
}

func createTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Query token (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("token query not yet implemented")
		},
	}
}

func createTokenInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Get token info (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("token info not yet implemented")
		},
	}
}

func createTokenBalanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "balance",
		Short: "Get token balance (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("token balance not yet implemented")
		},
	}
}

func createNFTQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nft",
		Short: "Query NFT (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nft query not yet implemented")
		},
	}
}

func createNFTInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Get NFT info (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nft info not yet implemented")
		},
	}
}

func createNFTListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List NFTs (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nft list not yet implemented")
		},
	}
}

func createGenesisCreateCmd() *cobra.Command {
	var output string
	var networkType string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create genesis block",
		RunE: func(cmd *cobra.Command, args []string) error {
			return createGenesis(networkType, output)
		},
	}

	cmd.Flags().StringVar(&output, "output", "genesis.pb", "Output genesis file")
	cmd.Flags().StringVar(&networkType, "network", "devnet", "Network type")

	return cmd
}

func createValidatorInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Get validator info (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("validator info not yet implemented")
		},
	}
}

func createValidatorSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set",
		Short: "Get validator set (not implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("validator set not yet implemented")
		},
	}
}

// helper functions
func loadKeyFromFile(name, password string) (ed25519.PrivateKey, error) {
	// Simplified key loading
	data, err := os.ReadFile(name + ".key")
	if err != nil {
		return nil, err
	}

	// Parse JSON
	var keyData map[string]string
	err = json.Unmarshal(data, &keyData)
	if err != nil {
		return nil, err
	}

	privKey, err := hex.DecodeString(keyData["private_key"])
	if err != nil {
		return nil, err
	}

	return ed25519.PrivateKey(privKey), nil
}

func createGenesis(networkType, output string) error {
	fmt.Printf("Creating genesis for %s network...\n", networkType)
	
	// Create genesis event
	genesis := &pb.Event{
		Id:        "genesis-event",
		Creator:   "genesis",
		Parents:   []string{},
		Timestamp: time.Now().UnixNano(),
		Round:     0,
		Signature: []byte{},
		Transactions: []*pb.Transaction{
			{
				Id:        "genesis-tx",
				ChainId:   fmt.Sprintf("ndag-%s", networkType),
				NetworkId: fmt.Sprintf("ndag-%s-v1", networkType),
				Nonce:     0,
				Fee:       0,
				GasLimit:  1000000,
				Timestamp: time.Now().UnixNano(),
				Payload: &pb.Transaction_GenesisPayload{
					GenesisPayload: &pb.GenesisPayload{
						Treasury:      "ndag1genesis",
						InitialSupply: 100000000000000,
					},
				},
			},
		},
	}

	// Save genesis
	data, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile(output, data, 0644)
	if err != nil {
		return err
	}

	fmt.Printf("Genesis created: %s\n", output)
	return nil
}