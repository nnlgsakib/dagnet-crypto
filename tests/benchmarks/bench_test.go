package benchmarks

import (
	"testing"
	"time"

	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/pb"
)

// Benchmark event creation and validation
func BenchmarkEventCreation(b *testing.B) {
	keypair, _ := crypto.GenerateKeypair()
	parents := []string{"parent1", "parent2"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := &pb.Event{
			Id:        generateEventID(),
			Creator:   "benchmark-validator",
			Parents:   parents,
			Timestamp: time.Now().UnixNano(),
			Round:     uint64(i),
		}
		crypto.SignEvent(event, keypair)
	}
}

// Benchmark transaction signing
func BenchmarkTransactionSigning(b *testing.B) {
	keypair, _ := crypto.GenerateKeypair()

	tx := &pb.Transaction{
		ChainId:   "benchmark",
		NetworkId: "benchmark-v1",
		Nonce:     1,
		Fee:       1000,
		GasLimit:  50000,
		Timestamp: time.Now().UnixNano(),
		PublicKey: keypair.Public,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx.Nonce = uint64(i)
		crypto.SignTransaction(tx, keypair)
	}
}

// Benchmark DAG operations
func BenchmarkDAGInsertion(b *testing.B) {
	dag := consensus.NewDAG()
	keypair, _ := crypto.GenerateKeypair()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := createBenchEvent(b, keypair, []string{}, uint64(i/10))
		dag.AddEvent(event)
	}
}

// Benchmark ancestors query
func BenchmarkAncestorsQuery(b *testing.B) {
	dag := consensus.NewDAG()
	keypair, _ := crypto.GenerateKeypair()

	// Build a large DAG first
	var parentID string
	for i := 0; i < 1000; i++ {
		parents := []string{}
		if parentID != "" {
			parents = []string{parentID}
		}
		event := createBenchEvent(b, keypair, parents, uint64(i/100))
		dag.AddEvent(event)
		parentID = event.Id
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dag.GetAncestors(parentID)
	}
}

// Benchmark consensus finality
func BenchmarkFinalityDetermination(b *testing.B) {
	dag := consensus.NewDAG()
	engine := consensus.NewConsensusEngine(dag)

	keypair1, _ := crypto.GenerateKeypair()
	keypair2, _ := crypto.GenerateKeypair()

	// Add validators
	engine.AddValidator(&consensus.Validator{
		Address:   "bench-validator-1",
		PublicKey: keypair1.Public,
		Stake:     1000,
		IsActive:  true,
	})
	engine.AddValidator(&consensus.Validator{
		Address:   "bench-validator-2",
		PublicKey: keypair2.Public,
		Stake:     2000,
		IsActive:  true,
	})

	// Create events
	event1 := createBenchEvent(b, keypair1, []string{}, 0)
	event2 := createBenchEvent(b, keypair2, []string{}, 0)
	dag.AddEvent(event1)
	dag.AddEvent(event2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.DetermineFame(0)
	}
}

// Benchmark VRF operations
func BenchmarkVRFGeneration(b *testing.B) {
	keypair, _ := crypto.GenerateKeypair()
	message := []byte("benchmark message")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		keypair.ComputeVRF(message)
	}
}

// Benchmark transaction throughput simulation
func BenchmarkTransactionThroughput(b *testing.B) {
	dag := consensus.NewDAG()
	keypair, _ := crypto.GenerateKeypair()

	// Create transactions and events
	var txs []*pb.Transaction
	for i := 0; i < 100; i++ {
		tx := &pb.Transaction{
			ChainId:   "benchmark",
			NetworkId: "benchmark-v1",
			Nonce:     uint64(i),
			Fee:       1000,
			GasLimit:  50000,
			Timestamp: time.Now().UnixNano(),
			Payload: &pb.Transaction_Transfer{
				Transfer: &pb.TransferPayload{
					To:     "recipient",
					Amount: uint64(i * 1000),
				},
			},
			PublicKey: keypair.Public,
		}
		crypto.SignTransaction(tx, keypair)
		txs = append(txs, tx)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := &pb.Event{
			Id:           generateBenchEventID(),
			Creator:      "bench-validator",
			Parents:      []string{},
			Timestamp:    time.Now().UnixNano(),
			Round:        0,
			Transactions: []*pb.Transaction{txs[i%len(txs)]},
		}
		crypto.SignEvent(event, keypair)
		dag.AddEvent(event)
	}
}

// Helper functions
func createBenchEvent(b *testing.B, keypair *crypto.Keypair, parents []string, round uint64) *pb.Event {
	event := &pb.Event{
		Id:        generateBenchEventID(),
		Creator:   "bench-validator",
		Parents:   parents,
		Timestamp: time.Now().UnixNano(),
		Round:     round,
	}
	crypto.SignEvent(event, keypair)
	return event
}

func generateBenchEventID() string {
	return fmt.Sprintf("bench-%d-%d", time.Now().UnixNano(), rand.Int63())
}