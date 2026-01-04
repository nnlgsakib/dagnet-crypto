package integration

import (
    "context"
    "encoding/hex"
    "fmt"
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

// TestFullSystemIntegration tests the complete system end-to-end
func TestFullSystemIntegration(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    // Setup test configuration
    cfg := config.DefaultConfig()
    cfg.Storage.Path = t.TempDir()
    cfg.P2P.Enabled = false // Disable P2P for offline testing

    // Initialize storage
    store, err := storage.NewStore(cfg.Storage.Path)
    require.NoError(t, err, "Failed to initialize storage")
    defer store.Close()

    // Initialize consensus
    dag := consensus.NewDAG()
    consensusEngine := consensus.NewConsensusEngine(dag)
    go consensusEngine.Start(ctx)
    defer consensusEngine.Stop()

    // Initialize mempool
    mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
    go mp.Start(ctx)
    defer mp.Stop()

    // Initialize state machine
    stateMachine := state.NewStateMachine(store, consensusEngine)
    go stateMachine.Start(ctx)
    defer stateMachine.Stop()

    // Test 1: Genesis Block Creation
    t.Run("GenesisBlockCreation", func(t *testing.T) {
        genesisEvent := createGenesisEvent(t)
        err := dag.AddEvent(genesisEvent)
        require.NoError(t, err, "Failed to add genesis event")

        // Verify genesis was stored
        eventData, err := store.GetEvent(genesisEvent.Id)
        require.NoError(t, err, "Failed to retrieve genesis event")

        var retrieved pb.Event
        err = proto.Unmarshal(eventData, &retrieved)
        require.NoError(t, err, "Failed to unmarshal genesis event")

        assert.Equal(t, genesisEvent.Id, retrieved.Id, "Genesis event ID mismatch")
        assert.Equal(t, uint64(0), retrieved.Round, "Genesis round should be 0")
        assert.Len(t, retrieved.Parents, 0, "Genesis should have no parents")
    })

    // Test 2: Validator Registration
    t.Run("ValidatorRegistration", func(t *testing.T) {
        validatorKey, _ := crypto.GenerateKeypair()
        
        tx := &pb.Transaction{
            ChainId:   cfg.Network.ChainID,
            NetworkId: cfg.Network.NetworkID,
            Nonce:     1,
            Fee:       10000000,
            GasLimit:  5000000,
            Timestamp: time.Now().UnixNano(),
            Payload: &pb.Transaction_CreateValidator{
                CreateValidator: &pb.CreateValidatorPayload{
                    ValidatorAddress:   "validator-1",
                    PublicKey:          validatorKey.Public,
                    CommissionRate:     10000000,
                    MaxCommissionRate:  20000000,
                    CommissionChangeRate: 500000,
                    Moniker:            "test-validator",
                    Identity:           "",
                    Website:            "",
                    Details:            "Test validator",
                },
            },
            PublicKey: validatorKey.Public,
        }

        err := crypto.SignTransaction(tx, validatorKey)
        require.NoError(t, err, "Failed to sign validator transaction")

        // Execute transaction
        err = stateMachine.ExecuteTransaction(tx)
        require.NoError(t, err, "Failed to execute validator registration")

        // Verify validator was added to consensus
        validator, exists := consensusEngine.GetValidator("validator-1")
        assert.True(t, exists, "Validator should be registered")
        if exists {
            assert.Equal(t, "validator-1", validator.Address, "Validator address mismatch")
        }
    })

    // Test 3: Token Creation and Transfer
    t.Run("TokenCreationAndTransfer", func(t *testing.T) {
        adminKey, _ := crypto.GenerateKeypair()
        adminAddr := fmt.Sprintf("ndag%s", hex.EncodeToString(adminKey.Public[:8]))

        // Create account with balance
        account := &pb.Account{
            Address:   adminAddr,
            Balance:   10000000000, // 1000 NDAG
            Nonce:     0,
            PublicKey: adminKey.Public,
            Tokens:    make(map[string]uint64),
        }

        accountData, err := proto.Marshal(account)
        require.NoError(t, err, "Failed to marshal account")

        err = store.PutAccount(adminAddr, accountData)
        require.NoError(t, err, "Failed to store account")

        // Create token
        tx := &pb.Transaction{
            ChainId:   cfg.Network.ChainID,
            NetworkId: cfg.Network.NetworkID,
            Nonce:     1,
            Fee:       100000,
            GasLimit:  1000000,
            Timestamp: time.Now().UnixNano(),
            Payload: &pb.Transaction_CreateToken{
                CreateToken: &pb.CreateTokenPayload{
                    TokenId:      "test-token-1",
                    Name:         "Test Token",
                    Symbol:       "TST",
                    Decimals:     6,
                    InitialSupply: 1000000000,
                    AdminAddress: adminAddr,
                    Mintable:     true,
                    Burnable:     true,
                    Pausable:     false,
                    Blacklistable: false,
                },
            },
            PublicKey: adminKey.Public,
        }

        err = crypto.SignTransaction(tx, adminKey)
        require.NoError(t, err, "Failed to sign token creation")

        err = stateMachine.ExecuteTransaction(tx)
        require.NoError(t, err, "Failed to execute token creation")

        // Verify token was created
        tokenData, err := store.GetToken("test-token-1")
        require.NoError(t, err, "Token should exist")

        var token pb.Token
        err = proto.Unmarshal(tokenData, &token)
        require.NoError(t, err, "Failed to unmarshal token")

        assert.Equal(t, "test-token-1", token.TokenId, "Token ID mismatch")
        assert.Equal(t, uint64(1000000000), token.TotalSupply, "Token supply mismatch")

        // Transfer tokens
        recipient := "recipient-1"
        tx2 := &pb.Transaction{
            ChainId:   cfg.Network.ChainID,
            NetworkId: cfg.Network.NetworkID,
            Nonce:     2,
            Fee:       2000,
            GasLimit:  100000,
            Timestamp: time.Now().UnixNano(),
            Payload: &pb.Transaction_TransferToken{
                TransferToken: &pb.TransferTokenPayload{
                    TokenId: "test-token-1",
                    To:      recipient,
                    Amount:  1000000,
                },
            },
            PublicKey: adminKey.Public,
        }

        err = crypto.SignTransaction(tx2, adminKey)
        require.NoError(t, err, "Failed to sign token transfer")

        err = stateMachine.ExecuteTransaction(tx2)
        require.NoError(t, err, "Failed to execute token transfer")
    })

    // Test 4: Event Creation and Consensus
    t.Run("EventCreationAndConsensus", func(t *testing.T) {
        creatorKey, _ := crypto.GenerateKeypair()
        
        // Create transaction
        tx := &pb.Transaction{
            ChainId:   cfg.Network.ChainID,
            NetworkId: cfg.Network.NetworkID,
            Nonce:     1,
            Fee:       1000,
            GasLimit:  50000,
            Timestamp: time.Now().UnixNano(),
            Payload: &pb.Transaction_Transfer{
                Transfer: &pb.TransferPayload{
                    To:     "recipient-1",
                    Amount: 1000000,
                },
            },
            PublicKey: creatorKey.Public,
        }

        err := crypto.SignTransaction(tx, creatorKey)
        require.NoError(t, err, "Failed to sign transaction")

        // Create event with transaction
        event := &pb.Event{
            Id:        generateEventID(),
            Creator:   "node-1",
            Parents:   []string{}, // Simplified for test
            Timestamp: time.Now().UnixNano(),
            Round:     1,
            Transactions: []*pb.Transaction{tx},
            VrfProof:  []byte{},
            RewardClaim: &pb.RewardClaim{
                Validator: "node-1",
                Amount:    0,
                Epoch:     0,
                Proof:     []byte{},
            },
        }

        err = crypto.SignEvent(event, creatorKey)
        require.NoError(t, err, "Failed to sign event")

        // Add event to DAG
        err = dag.AddEvent(event)
        require.NoError(t, err, "Failed to add event to DAG")

        // Store event
        eventData, err := proto.Marshal(event)
        require.NoError(t, err, "Failed to marshal event")

        err = store.PutEvent(event.Id, eventData)
        require.NoError(t, err, "Failed to store event")

        // Verify event was stored
        retrievedData, err := store.GetEvent(event.Id)
        require.NoError(t, err, "Failed to retrieve event")
        assert.Equal(t, eventData, retrievedData, "Stored event data mismatch")

        // Test consensus finality
        err = consensusEngine.DetermineFame(1)
        require.NoError(t, err, "Failed to determine fame")

        finalized := consensusEngine.CheckFinality(event.Id)
        assert.True(t, finalized, "Event should be finalized")

        // Verify event metadata is updated
        assert.True(t, event.Metadata.IsFinalized, "Event should have finalized metadata")
    })

    // Test 5: Mempool Operations
    t.Run("MempoolOperations", func(t *testing.T) {
        // Clear mempool
        mp.Clear()

        // Generate multiple transactions
        numTxs := 100
        var txs []*pb.Transaction
        
        for i := 0; i < numTxs; i++ {
            txKey, _ := crypto.GenerateKeypair()
            
            tx := &pb.Transaction{
                ChainId:   cfg.Network.ChainID,
                NetworkId: cfg.Network.NetworkID,
                Nonce:     uint64(i + 1),
                Fee:       uint64(1000 + i), // Varying fees
                GasLimit:  50000,
                Timestamp: time.Now().UnixNano(),
                Payload: &pb.Transaction_Transfer{
                    Transfer: &pb.TransferPayload{
                        To:     fmt.Sprintf("recipient-%d", i),
                        Amount: uint64(1000000 + i),
                    },
                },
                PublicKey: txKey.Public,
            }

            err := crypto.SignTransaction(tx, txKey)
            require.NoError(t, err, "Failed to sign transaction %d", i)

            txs = append(txs, tx)
        }

        // Add transactions to mempool
        for i, tx := range txs {
            added := mp.Add(tx)
            assert.True(t, added, "Transaction %d should be added to mempool", i)
        }

        // Verify mempool size
        size := mp.Size()
        assert.Equal(t, numTxs, size, "Mempool size mismatch")

        // Get pending transactions (should be sorted by fee)
        pending := mp.GetPending(10)
        assert.Len(t, pending, 10, "Should get 10 pending transactions")

        // Verify they are sorted by fee (highest first)
        for i := 1; i < len(pending); i++ {
            assert.GreaterOrEqual(t, pending[i-1].Fee, pending[i].Fee, "Transactions should be sorted by fee")
        }

        // Remove transactions
        for _, tx := range txs {
            mp.Remove(tx.Id)
        }

        // Verify mempool is empty
        assert.Equal(t, 0, mp.Size(), "Mempool should be empty after removing all transactions")
    })

    // Test 6: State Management
    t.Run("StateManagement", func(t *testing.T) {
        // Create test accounts
        acc1Key, _ := crypto.GenerateKeypair()
        acc1Addr := fmt.Sprintf("ndag%s", hex.EncodeToString(acc1Key.Public[:8]))

        account := &pb.Account{
            Address:   acc1Addr,
            Balance:   1000000000,
            Nonce:     0,
            PublicKey: acc1Key.Public,
            Tokens:    make(map[string]uint64),
        }

        accountData, err := proto.Marshal(account)
        require.NoError(t, err, "Failed to marshal account")

        // Store account
        err = store.PutAccount(acc1Addr, accountData)
        require.NoError(t, err, "Failed to store account")

        // Retrieve account
        retrievedData, err := store.GetAccount(acc1Addr)
        require.NoError(t, err, "Failed to retrieve account")

        var retrieved pb.Account
        err = proto.Unmarshal(retrievedData, &retrieved)
        require.NoError(t, err, "Failed to unmarshal account")

        assert.Equal(t, acc1Addr, retrieved.Address, "Account address mismatch")
        assert.Equal(t, uint64(1000000000), retrieved.Balance, "Account balance mismatch")

        // Test token balances
        account.Tokens["token-1"] = 5000000
        accountData, err = proto.Marshal(account)
        require.NoError(t, err, "Failed to marshal account with tokens")

        err = store.PutAccount(acc1Addr, accountData)
        require.NoError(t, err, "Failed to update account")

        retrievedData, err = store.GetAccount(acc1Addr)
        require.NoError(t, err, "Failed to retrieve updated account")

        err = proto.Unmarshal(retrievedData, &retrieved)
        require.NoError(t, err, "Failed to unmarshal updated account")

        assert.Equal(t, uint64(5000000), retrieved.Tokens["token-1"], "Token balance mismatch")
    })
}

// TestConcurrency tests concurrent operations
func TestConcurrency(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
    defer cancel()

    cfg := config.DefaultConfig()
    cfg.Storage.Path = t.TempDir()

    store, err := storage.NewStore(cfg.Storage.Path)
    require.NoError(t, err)
    defer store.Close()

    dag := consensus.NewDAG()
    consensusEngine := consensus.NewConsensusEngine(dag)
    go consensusEngine.Start(ctx)
    defer consensusEngine.Stop()

    mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
    go mp.Start(ctx)
    defer mp.Stop()

    t.Run("ConcurrentEventInsertion", func(t *testing.T) {
        // Create multiple goroutines inserting events concurrently
        numGoroutines := 10
        eventsPerGoroutine := 50
        
        done := make(chan bool, numGoroutines)
        
        for i := 0; i < numGoroutines; i++ {
            go func(id int) {
                defer func() { done <- true }()
                
                keypair, _ := crypto.GenerateKeypair()
                
                for j := 0; j < eventsPerGoroutine; j++ {
                    event := createTestEvent(t, keypair, []string{}, 0)
                    // Errors are expected under concurrent load
                    _ = dag.AddEvent(event)
                }
            }(i)
        }
        
        // Wait for all goroutines to complete
        for i := 0; i < numGoroutines; i++ {
            <-done
        }
        
        // Verify some events were inserted
        eventCount := dag.EventsCount()
        assert.Greater(t, eventCount, 0, "Should have inserted some events")
        assert.LessOrEqual(t, eventCount, numGoroutines*eventsPerGoroutine, "Should not exceed maximum")
    })

    t.Run("ConcurrentMempoolOperations", func(t *testing.T) {
        mp.Clear()
        
        numGoroutines := 5
        txsPerGoroutine := 100
        
        done := make(chan bool, numGoroutines)
        
        for i := 0; i < numGoroutines; i++ {
            go func(id int) {
                defer func() { done <- true }()
                
                keypair, _ := crypto.GenerateKeypair()
                
                for j := 0; j < txsPerGoroutine; j++ {
                    tx := &pb.Transaction{
                        ChainId:   cfg.Network.ChainID,
                        NetworkId: cfg.Network.NetworkID,
                        Nonce:     uint64(j + 1),
                        Fee:       1000,
                        GasLimit:  50000,
                        Timestamp: time.Now().UnixNano(),
                        Payload: &pb.Transaction_Transfer{
                            Transfer: &pb.TransferPayload{
                                To:     fmt.Sprintf("recipient-%d", id),
                                Amount: uint64(1000000 + j),
                            },
                        },
                        PublicKey: keypair.Public,
                    }
                    
                    crypto.SignTransaction(tx, keypair)
                    mp.Add(tx)
                }
            }(i)
        }
        
        // Wait for all goroutines
        for i := 0; i < numGoroutines; i++ {
            <-done
        }
        
        // Verify mempool contains transactions
        size := mp.Size()
        assert.Greater(t, size, 0, "Mempool should contain transactions")
        assert.LessOrEqual(t, size, numGoroutines*txsPerGoroutine, "Should not exceed maximum")
    })
}

// TestFaultTolerance tests system's behavior under faults
func TestFaultTolerance(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
    defer cancel()

    cfg := config.DefaultConfig()
    cfg.Storage.Path = t.TempDir()

    store, err := storage.NewStore(cfg.Storage.Path)
    require.NoError(t, err)
    defer store.Close()

    dag := consensus.NewDAG()
    consensusEngine := consensus.NewConsensusEngine(dag)
    go consensusEngine.Start(ctx)
    defer consensusEngine.Stop()

    t.Run("InvalidEvents", func(t *testing.T) {
        // Test adding invalid events
        invalidEvents := []*pb.Event{
            {Id: "", Creator: "", Parents: []string{}, Timestamp: 0}, // Missing ID
            {Id: "event-1", Creator: "", Parents: []string{}, Timestamp: 0}, // Missing creator
            {Id: "event-2", Creator: "node-1", Parents: []string{"non-existent"}, Timestamp: 0}, // Invalid parent
        }
        
        for i, event := range invalidEvents {
            err := dag.AddEvent(event)
            assert.Error(t, err, "Should reject invalid event %d", i)
        }
    })

    t.Run("DuplicateTransaction", func(t *testing.T) {
        mp := mempool.NewMempool(cfg.Consensus.MaxGasPerRound)
        go mp.Start(ctx)
        defer mp.Stop()
        
        mp.Clear()
        
        keypair, _ := crypto.GenerateKeypair()
        
        // Create transaction
        tx := &pb.Transaction{
            ChainId:   cfg.Network.ChainID,
            NetworkId: cfg.Network.NetworkID,
            Nonce:     1,
            Fee:       1000,
            GasLimit:  50000,
            Timestamp: time.Now().UnixNano(),
            Payload: &pb.Transaction_Transfer{
                Transfer: &pb.TransferPayload{
                    To:     "recipient-1",
                    Amount: 1000000,
                },
            },
            PublicKey: keypair.Public,
        }
        
        crypto.SignTransaction(tx, keypair)
        
        // Add to mempool
        added := mp.Add(tx)
        assert.True(t, added, "Transaction should be added")
        
        // Try to add duplicate
        added = mp.Add(tx)
        assert.False(t, added, "Duplicate transaction should be rejected")
        
        // Verify only one transaction in mempool
        assert.Equal(t, 1, mp.Size(), "Mempool should contain only one transaction")
    })
}

// Helper functions
func createGenesisEvent(t *testing.T) *pb.Event {
    t.Helper()
    
    // Create genesis transaction
    genesisTx := &pb.Transaction{
        Id:        "genesis-tx-1",
        ChainId:   "ndag-test",
        NetworkId: "ndag-test-v1",
        Nonce:     0,
        Fee:       0,
        GasLimit:  1000000,
        Timestamp: time.Now().UnixNano(),
        Payload: &pb.Transaction_GenesisPayload{
            GenesisPayload: &pb.GenesisPayload{
                Treasury:      "genesis-treasury",
                InitialSupply: 100000000000000,
            },
        },
        PublicKey: []byte("genesis-key"),
        Signature: []byte("genesis-sig"),
    }
    
    // Create genesis event
    event := &pb.Event{
        Id:        "genesis-event-1",
        Creator:   "genesis",
        Parents:   []string{},
        Timestamp: time.Now().UnixNano(),
        Round:     0,
        Transactions: []*pb.Transaction{genesisTx},
        StateHash: []byte("genesis-state"),
        Metadata: &pb.EventMetadata{
            IsWitness:   true,
            IsFamous:    true,
            IsFinalized: true,
        },
    }
    
    return event
}

func createTestTransaction(t *testing.T, fromKey, toAddr string, amount uint64) *pb.Transaction {
    t.Helper()
    
    senderKey, _ := crypto.GenerateKeypair()
    
    return &pb.Transaction{
        ChainId:   "ndag-test",
        NetworkId: "ndag-test-v1",
        Nonce:     1,
        Fee:       1000,
        GasLimit:  50000,
        Timestamp: time.Now().UnixNano(),
        Payload: &pb.Transaction_Transfer{
            Transfer: &pb.TransferPayload{
                To:     toAddr,
                Amount: amount,
            },
        },
        PublicKey: senderKey.Public,
    }
}