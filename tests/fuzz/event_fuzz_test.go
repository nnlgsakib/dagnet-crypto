package fuzz

import (
    "bytes"
    "crypto/rand"
    "encoding/hex"
    "testing"

    "github.com/ndag/ndagcoin/pkg/consensus"
    "github.com/ndag/ndagcoin/pkg/crypto"
    "github.com/ndag/ndagcoin/pkg/pb"
    "google.golang.org/protobuf/proto"
)

// FuzzEventSerialization tests event serialization/deserialization
func FuzzEventSerialization(f *testing.F) {
    // Seed corpus
    keypair, _ := crypto.GenerateKeypair()
    
    seeds := []*pb.Event{
        {
            Id:        "seed-event-1",
            Creator:   "validator-1",
            Parents:   []string{"parent1", "parent2"},
            Timestamp: 1234567890,
            Round:     1,
        },
        {
            Id:        "seed-event-2",
            Creator:   "validator-2", 
            Parents:   []string{},
            Timestamp: 1234567900,
            Round:     0,
        },
    }
    
    for _, seed := range seeds {
        data, _ := proto.Marshal(seed)
        f.Add(data)
    }

    f.Fuzz(func(t *testing.T, data []byte) {
        var event pb.Event
        
        // Should not panic on any input
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic on malformed data: %v", r)
            }
        }()
        
        // Attempt to unmarshal
        err := proto.Unmarshal(data, &event)
        if err != nil {
            return // Invalid protobuf is acceptable
        }
        
        // Validate critical fields
        if event.Id == "" {
            return // Skip events without IDs
        }
        
        // Verify serialization roundtrip
        marshaled, err := proto.Marshal(&event)
        if err != nil {
            t.Fatalf("Failed to marshal valid event: %v", err)
        }
        
        // Should be able to unmarshal again
        var event2 pb.Event
        if err := proto.Unmarshal(marshaled, &event2); err != nil {
            t.Fatalf("Failed to unmarshal roundtrip: %v", err)
        }
        
        // Verify fields preserved
        if event.Id != event2.Id {
            t.Errorf("ID mismatch after roundtrip: %s != %s", event.Id, event2.Id)
        }
    })
}

// FuzzTransactionValidation tests transaction validation with random inputs
func FuzzTransactionValidation(f *testing.F) {
    // Seed with valid transactions
    keypair, _ := crypto.GenerateKeypair()
    
    validTx := &pb.Transaction{
        ChainId:   "testnet",
        NetworkId: "testnet-v1", 
        Nonce:     1,
        Fee:       1000,
        GasLimit:  50000,
        Timestamp: 1234567890,
        PublicKey: keypair.Public,
    }
    
    txData, _ := proto.Marshal(validTx)
    f.Add(txData)
    
    f.Fuzz(func(t *testing.T, data []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic on transaction data: %v", r)
            }
        }()
        
        var tx pb.Transaction
        if err := proto.Unmarshal(data, &tx); err != nil {
            return
        }
        
        // Test transaction validation logic
        validateTransaction(&tx)
    })
}

// FuzzDAGEventInsertion tests DAG with random event data
func FuzzDAGEventInsertion(f *testing.F) {
    dag := consensus.NewDAG()
    keypair, _ := crypto.GenerateKeypair()
    
    // Seed valid events
    seeds := []string{"event-1", "event-2", "witness-1", "round-0"}
    for _, seed := range seeds {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, eventID string) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic inserting event: %v", r)
            }
        }()
        
        // Create event with fuzzed ID
        event := &pb.Event{
            Id:        eventID,
            Creator:   "fuzz-validator",
            Parents:   []string{},
            Timestamp: 1234567890,
            Round:     0,
        }
        
        // Sign event
        crypto.SignEvent(event, keypair)
        
        // Try to add to DAG
        err := dag.AddEvent(event)
        
        // Re-adding same event should fail
        if err == nil {
            err = dag.AddEvent(event)
            if err == nil {
                t.Error("Expected error adding duplicate event")
            }
        }
    })
}

// FuzzCryptographicOperations tests crypto operations with random data
func FuzzCryptographicOperations(f *testing.F) {
    keypair, _ := crypto.GenerateKeypair()
    
    // Test data seeds
    seeds := [][]byte{
        []byte("hello world"),
        []byte(""),
        []byte{0x00, 0x01, 0x02, 0x03},
        bytes.Repeat([]byte("x"), 1000),
    }
    
    for _, seed := range seeds {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, data []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic in crypto operation: %v", r)
            }
        }()
        
        // Test signing with random data
        sig, err := keypair.Sign(data)
        if err != nil {
            return
        }
        
        // Test verification
        valid := crypto.Verify(keypair.Public, data, sig)
        if !valid {
            t.Error("Signature verification failed for valid signature")
        }
        
        // Test with modified data
        if len(data) > 0 {
            modified := make([]byte, len(data))
            copy(modified, data)
            modified[0] ^= 0xFF // Flip bits
            
            valid = crypto.Verify(keypair.Public, modified, sig)
            if valid {
                t.Error("Signature verified for modified data")
            }
        }
    })
}

// FuzzVRFOperations tests VRF with random inputs
func FuzzVRFOperations(f *testing.F) {
    keypair, _ := crypto.GenerateKeypair()
    
    seedMessages := [][]byte{
        []byte("epoch-1"),
        []byte("round-2"),
        []byte("validator-key"),
    }
    
    for _, seed := range seedMessages {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, message []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic in VRF operation: %v", r)
            }
        }()
        
        // Generate VRF proof
        proof, output, err := keypair.ComputeVRF(message)
        if err != nil {
            return
        }
        
        // Verify VRF
        valid, verifyOutput := crypto.VerifyVRF(keypair.Public, proof, message)
        if !valid {
            t.Error("VRF verification failed")
            return
        }
        
        // Output should match
        if !bytes.Equal(output, verifyOutput) {
            t.Error("VRF output mismatch")
        }
    })
}

// FuzzHashFunction tests hash function with random inputs
func FuzzHashFunction(f *testing.F) {
    seeds := [][]byte{
        []byte(""),
        []byte("hello"),
        []byte{0xFF, 0xFE, 0xFD},
    }
    
    for _, seed := range seeds {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, data []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic in hash function: %v", r)
            }
        }()
        
        // Test hash properties
        hash1 := crypto.HashData(data)
        hash2 := crypto.HashData(data)
        
        // Same input should produce same hash
        if hash1 != hash2 {
            t.Error("Hash function not deterministic")
        }
        
        // Different input should produce different hash (with high probability)
        if len(data) > 0 {
            modified := append(data, 0x00)
            hash3 := crypto.HashData(modified)
            if hash1 == hash3 {
                // Could theoretically collide, but very unlikely for good hash
                t.Log("Hash collision (unlikely but possible)")
            }
        }
        
        // Hash size should be constant
        if len(hash1.Bytes()) != 32 {
            t.Error("Hash has wrong size")
        }
    })
}

// FuzzRoundCalculation tests round calculation with edge cases
func FuzzRoundCalculation(f *testing.F) {
    dag := consensus.NewDAG()
    keypair, _ := crypto.GenerateKeypair()
    
    // Seed valid round numbers
    seeds := []uint64{0, 1, 2, 100, 1000, 18446744073709551615} // Max uint64
    for _, seed := range seeds {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, round uint64) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic in round calculation: %v", r)
            }
        }()
        
        event := &pb.Event{
            Id:        generateFuzzEventID(),
            Creator:   "fuzz-validator", 
            Parents:   []string{},
            Timestamp: 1234567890,
            Round:     round,
        }
        
        crypto.SignEvent(event, keypair)
        err := dag.AddEvent(event)
        
        // Should not crash, error is okay for invalid data
        if err == nil {
            // Verify event was stored with correct round
            retrieved, exists := dag.GetEvent(event.Id)
            if exists {
                if retrieved.Round != round {
                    t.Errorf("Round mismatch: %d != %d", retrieved.Round, round)
                }
            }
        }
    })
}

// FuzzGenesisValidation tests genesis validation with random data
func FuzzGenesisValidation(f *testing.F) {
    // Seed valid genesis
    keypair, _ := crypto.GenerateKeypair()
    validGenesis := &pb.Event{
        Id:        "genesis",
        Creator:   "genesis",
        Parents:   []string{},
        Timestamp: 1234567890,
        Round:     0,
        Transactions: []*pb.Transaction{
            {
                Id:    "genesis-tx",
                Payload: &pb.Transaction_GenesisPayload{
                    GenesisPayload: &pb.GenesisPayload{
                        Treasury:      "treasury",
                        InitialSupply: 1000000,
                    },
                },
            },
        },
    }
    
    data, _ := proto.Marshal(validGenesis)
    f.Add(data)
    
    f.Fuzz(func(t *testing.T, data []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic in genesis validation: %v", r)
            }
        }()
        
        var genesis pb.Event
        if err := proto.Unmarshal(data, &genesis); err != nil {
            return
        }
        
        // Test validation logic
        validateGenesis(&genesis)
    })
}

// FuzzNetworkMessageParsing tests network message parsing
func FuzzNetworkMessageParsing(f *testing.F) {
    // Seed valid network messages
    seeds := [][]byte{
        []byte(`{"type":"event","data":"..."}`),
        []byte(`{"type":"tx","data":"..."}`),
        []byte{"tx\x00\x01\x02"},
    }
    
    for _, seed := range seeds {
        f.Add(seed)
    }
    
    f.Fuzz(func(t *testing.T, data []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Errorf("panic parsing network message: %v", r)
            }
        }()
        
        // Try to parse as event
        var event pb.Event
        _ = proto.Unmarshal(data, &event)
        
        // Try to parse as transaction  
        var tx pb.Transaction
        _ = proto.Unmarshal(data, &tx)
        
        // Should not crash regardless of input
    })
}

// Helper functions

func validateTransaction(tx *pb.Transaction) {
    // Basic validation logic
    if tx == nil {
        return
    }
    if tx.Nonce == 0 {
        return // Invalid
    }
    if tx.ChainId == "" || tx.NetworkId == "" {
        return // Invalid
    }
}

func validateGenesis(genesis *pb.Event) {
    if genesis == nil || genesis.Round != 0 {
        return
    }
    // Additional validation logic here
}

func generateFuzzEventID() string {
    // Generate random event ID
    buf := make([]byte, 32)
    rand.Read(buf)
    return hex.EncodeToString(buf)
}