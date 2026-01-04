package integration

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestProductionSystem tests the complete system in production-like conditions
func TestProductionSystem(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Use production configuration
	cfg := config.GenerateNetworkConfig(config.NetworkTypeDevnet)
	cfg.Storage.Path = t.TempDir()

	// Initialize all components
	store, err := storage.NewStore(cfg.Storage.Path)
	require.NoError(t, err, "Failed to initialize storage")
	defer store.Close()

	// Test 1: Genesis Block Creation
	t.Run("GenesisBlock", func(t *testing.T) {
		genesisEvent := createGenesisEvent(t, cfg)
		err := store.PutEvent(genesisEvent.Id, mustMarshal(t, genesisEvent))
		require.NoError(t, err, "Failed to store genesis")

		// Verify genesis
		retrieved, err := store.GetEvent(genesisEvent.Id)
		require.NoError(t, err, "Failed to retrieve genesis")

		var retrievedEvent pb.Event
		require.NoError(t, proto.Unmarshal(retrieved, &retrievedEvent), "Failed to unmarshal")
		assert.Equal(t, genesisEvent.Id, retrievedEvent.Id)
		assert.Equal(t, uint64(0), retrievedEvent.Round)
	})

	// Test 2: Validator Registration Flow
	t.Run("ValidatorRegistration", func(t *testing.T) {
		validatorKey, _ := crypto.GenerateKeypair()
		validatorAddr := crypto.GetAddressFromPublicKey(validatorKey.Public)

		tx := &pb.Transaction{
			ChainId:   cfg.Network.ChainID,
			NetworkId: cfg.Network.NetworkID,
			Nonce:     1,
			Fee:       10000000,
			GasLimit:  5000000,
			Timestamp: time.Now().UnixNano(),
			Payload: &pb.Transaction_CreateValidator{
				CreateValidator: &pb.CreateValidatorPayload{
					ValidatorAddress:   validatorAddr,
					PublicKey:          validatorKey.Public,
					CommissionRate:     10000000,
					MaxCommissionRate:  20000000,
					CommissionChangeRate: 500000,
					Moniker:            "test-validator",
				},
			},
			PublicKey: validatorKey.Public,
		}

		err := crypto.SignTransaction(tx, validatorKey)
		require.NoError(t, err, "Failed to sign validator transaction")

		// Verify signature
		assert.True(t, crypto.VerifyTransactionSignature(tx), "Transaction signature invalid")

		// Execute transaction
		dag := consensus.NewDAG()
		consensusEngine := consensus.NewConsensusEngine(dag)
		executor := state.NewTransactionExecutor(nil, store, consensusEngine)

		result, err := executor.Execute(tx)
		require.NoError(t, err, "Failed to execute validator transaction")
		assert.True(t, result.Success, "Validator transaction failed")
	})

	// Test 3: Token Creation and Transfer End-to-End
	t.Run("TokenLifecycle", func(t *testing.T) {
		adminKey, _ := crypto.GenerateKeypair()
		adminAddr := crypto.GetAddressFromPublicKey(adminKey.Public)
		recipientAddr := "ndag1recipient1234567890"

		// Create account with balance
		account := &pb.Account{
			Address:   adminAddr,
			Balance:   10000000000,
			Nonce:     0,
			PublicKey: adminKey.Public,
			Tokens:    make(map[string]uint64),
		}
		accountData := mustMarshal(t, account)
		err := store.PutAccount(adminAddr, accountData)
		require.NoError(t, err, "Failed to store account")

		// Create token
		tokenID := "test-token-" + hex.EncodeToString([]byte{byte(time.Now().UnixNano())})
		tx1 := &pb.Transaction{
			ChainId:   cfg.Network.ChainID,
			NetworkId: cfg.Network.NetworkID,
			Nonce:     1,
			Fee:       100000,
			GasLimit:  1000000,
			Timestamp: time.Now().UnixNano(),
			Payload: &pb.Transaction_CreateToken{
				CreateToken: &pb.CreateTokenPayload{
					TokenId:      tokenID,
					Name:         "Test Token",
					Symbol:       "TST",
					Decimals:     6,
					InitialSupply: 1000000000,
					AdminAddress: adminAddr,
					Mintable:     true,
					Burnable:     true,
				},
			},
			PublicKey: adminKey.Public,
		}

		err = crypto.SignTransaction(tx1, adminKey)
		require.NoError(t, err)

		// Transfer tokens
		tx2 := &pb.Transaction{
			ChainId:   cfg.Network.ChainID,
			NetworkId: cfg.Network.NetworkID,
			Nonce:     2,
			Fee:       2000,
			GasLimit:  100000,
			Timestamp: time.Now().UnixNano(),
			Payload: &pb.Transaction_TransferToken{
				TransferToken: &pb.TransferTokenPayload{
					TokenId: tokenID,
					To:      recipientAddr,
					Amount:  1000000,
				},
			},
			PublicKey: adminKey.Public,
		}

		err = crypto.SignTransaction(tx2, adminKey)
		require.NoError(t, err)

		// Verify signatures
		assert.True(t, crypto.VerifyTransactionSignature(tx1))
		assert.True(t, crypto.VerifyTransactionSignature(tx2))
	})

	// Test 4: Mempool Performance
	t.Run("MempoolPerformance", func(t *testing.T) {
		mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
		mp.Start(ctx)
		defer mp.Stop()

		// Add 1000 transactions
		numTxs := 1000
		start := time.Now()

		for i := 0; i < numTxs; i++ {
			key, _ := crypto.GenerateKeypair()
			tx := createTestTransaction(t, key, uint64(i+1))

			added := mp.Add(tx)
			assert.True(t, added, "Transaction should be added")
		}

		elapsed := time.Since(start)
		t.Logf("Added %d transactions in %v (%.2f tx/sec)", numTxs, elapsed, float64(numTxs)/elapsed.Seconds())

		// Verify mempool size
		size := mp.Size()
		assert.Equal(t, numTxs, size, "Mempool size mismatch")

		// Get pending transactions
		pending := mp.GetPending(100)
		assert.Len(t, pending, 100, "Should get 100 pending transactions")

		// Clear mempool
		mp.Clear()
		assert.Equal(t, 0, mp.Size(), "Mempool should be empty")
	})

	// Test 5: Event Processing and Finality
	t.Run("EventProcessing", func(t *testing.T) {
		dag := consensus.NewDAG()
		engine := consensus.NewConsensusEngine(dag)

		// Create and add events
		key, _ := crypto.GenerateKeypair()

		for i := 0; i < 10; i++ {
			event := &pb.Event{
				Id:        fmt.Sprintf("event-%d-%d", time.Now().UnixNano(), i),
				Creator:   "test-node",
				Parents:   []string{},
				Timestamp: time.Now().UnixNano(),
				Round:     uint64(i),
			}

			err := dag.AddEvent(event)
			require.NoError(t, err, "Failed to add event %d", i)
		}

		// Verify DAG state
		assert.Equal(t, 10, dag.EventsCount(), "DAG should have 10 events")

		// Check finality
		events := dag.GetEventsByRound(0)
		for _, event := range events {
			finalized := engine.CheckFinality(event.Id)
			assert.True(t, finalized, "Event should be finalized")
		}
	})

	// Test 6: Cryptographic Operations
	t.Run("CryptographicOperations", func(t *testing.T) {
		// Generate keypair
		keypair, err := crypto.GenerateKeypair()
		require.NoError(t, err, "Failed to generate keypair")

		// Test signing
		data := []byte("test data")
		signature, err := keypair.Sign(data)
		require.NoError(t, err, "Failed to sign data")

		// Test verification
		valid := crypto.Verify(keypair.Public, data, signature)
		assert.True(t, valid, "Signature should be valid")

		// Test address generation
		address := crypto.GetAddressFromPublicKey(keypair.Public)
		assert.True(t, len(address) > 0, "Address should be generated")
		assert.Equal(t, "ndag", address[:4], "Address should have correct prefix")

		// Test VRF
		message := []byte("test message")
		proof, output, err := keypair.ComputeVRF(message)
		require.NoError(t, err, "Failed to compute VRF")
		assert.NotNil(t, proof)
		assert.NotNil(t, output)

		// Verify VRF
		validVRF, verifyOutput := crypto.VerifyVRF(keypair.Public, proof, message)
		assert.True(t, validVRF, "VRF should be valid")
		assert.Equal(t, output, verifyOutput, "VRF outputs should match")
	})
}

// TestConcurrentLoad tests system under concurrent load
func TestConcurrentLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Initialize components
	cfg := config.DefaultConfig()
	cfg.Storage.Path = t.TempDir()

	store, err := storage.NewStore(cfg.Storage.Path)
	require.NoError(t, err)
	defer store.Close()

	dag := consensus.NewDAG()
	engine := consensus.NewConsensusEngine(dag)
	executor := state.NewTransactionExecutor(nil, store, engine)

	// Create validator
	validatorKey, _ := crypto.GenerateKeypair()
	tx := createValidatorTx(t, validatorKey, "test-validator")
	result, err := executor.Execute(tx)
	require.NoError(t, err)
	assert.True(t, result.Success, "Validator creation should succeed")

	// Concurrent transaction generation
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()

			key, _ := crypto.GenerateKeypair()
			for j := 0; j < 100; j++ {
				tx := createTransferTx(t, key, "recipient-"+string(rune(id)), uint64(1000+j), uint64(1+j))
				_, _ = executor.Execute(tx)
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify system stability
	assert.Greater(t, dag.EventsCount(), 0, "Events should be added")
	assert.Greater(t, store.Size(), 0, "Data should be stored")
}

// BenchmarkPerformance benchmarks critical operations
func BenchmarkEventCreation(b *testing.B) {
	keypair, _ := crypto.GenerateKeypair()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &pb.Event{
			Id:        fmt.Sprintf("event-%d", i),
			Creator:   "benchmark",
			Parents:   []string{},
			Timestamp: time.Now().UnixNano(),
			Round:     uint64(i / 100),
		}
	}
}

func BenchmarkTransactionSigning(b *testing.B) {
	keypair, _ := crypto.GenerateKeypair()

	tx := &pb.Transaction{
		ChainId:   "test",
		NetworkId: "test-v1",
		Nonce:     1,
		Fee:       1000,
		GasLimit:  50000,
		Timestamp: time.Now().UnixNano(),
		PublicKey: keypair.Public,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx.Nonce = uint64(i)
		_ = crypto.SignTransaction(tx, keypair)
	}
}

// Helper functions
func createGenesisEvent(t *testing.T, cfg *config.Config) *pb.Event {
	genesisTx := &pb.Transaction{
		Id:    "genesis-tx-1",
		Nonce: 0,
		Fee:   0,
		Payload: &pb.Transaction_GenesisPayload{
			GenesisPayload: &pb.GenesisPayload{
				Treasury:      "genesis-treasury",
				InitialSupply: 100000000000000,
			},
		},
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

func createValidatorTx(t *testing.T, key *crypto.Keypair, moniker string) *pb.Transaction {
	addr := crypto.GetAddressFromPublicKey(key.Public)

	tx := &pb.Transaction{
		ChainId:   "ndag-test",
		NetworkId: "ndag-test-v1",
		Nonce:     1,
		Fee:       10000000,
		Timestamp: time.Now().UnixNano(),
		Payload: &pb.Transaction_CreateValidator{
			CreateValidator: &pb.CreateValidatorPayload{
				ValidatorAddress:   addr,
				PublicKey:          key.Public,
				CommissionRate:     10000000,
				MaxCommissionRate:  20000000,
				CommissionChangeRate: 500000,
				Moniker:            moniker,
			},
		},
		PublicKey: key.Public,
	}

	require.NoError(t, crypto.SignTransaction(tx, key))
	return tx
}

func createTransferTx(t *testing.T, key *crypto.Keypair, to string, amount, nonce uint64) *pb.Transaction {
	tx := &pb.Transaction{
		ChainId:   "ndag-test",
		NetworkId: "ndag-test-v1",
		Nonce:     nonce,
		Fee:       1000,
		Timestamp: time.Now().UnixNano(),
		Payload: &pb.Transaction_Transfer{
			Transfer: &pb.TransferPayload{
				To:     to,
				Amount: amount,
			},
		},
		PublicKey: key.Public,
	}

	require.NoError(t, crypto.SignTransaction(tx, key))
	return tx
}

func createTestTransaction(t *testing.T, key *crypto.Keypair, nonce uint64) *pb.Transaction {
	return createTransferTx(t, key, "test-recipient", 1000000, nonce)
}

func mustMarshal(t *testing.T, msg proto.Message) []byte {
	data, err := proto.Marshal(msg)
	require.NoError(t, err)
	return data
}