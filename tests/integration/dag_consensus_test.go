package integration

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGConsensusIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create DAG
	dag := consensus.NewDAG()
	
	// Create consensus engine
	engine := consensus.NewConsensusEngine(dag)
	go engine.Start(ctx)
	defer engine.Stop()

	// Test 1: Add initial witnesses
	t.Run("AddWitnessEvents", func(t *testing.T) {
		keypair, err := crypto.GenerateKeypair()
		require.NoError(t, err)
		
		event := createTestEvent(t, keypair, []string{}, 0)
		
		err = dag.AddEvent(event)
		assert.NoError(t, err)
		
		// Check if it's a witness
		witnesses := dag.GetWitnesses(0)
		assert.Len(t, witnesses, 1)
		assert.Equal(t, event.Id, witnesses[0].Id)
	})

	// Test 2: Build DAG with multiple validators
	t.Run("MultiValidatorDAG", func(t *testing.T) {
		validators := createTestValidators(t, 3)
		
		// Round 0: Genesis witnesses
		var round0Events []*pb.Event
		for _, keypair := range validators {
			event := createTestEvent(t, keypair, []string{}, 0)
			err := dag.AddEvent(event)
			require.NoError(t, err)
			round0Events = append(round0Events, event)
		}
		
		// Validate witnesses
		witnesses := dag.GetWitnesses(0)
		assert.Len(t, witnesses, 3)
		
		// Round 1: Events referencing round 0
		for _, keypair := range validators {
			// Use all round 0 events as parents
			var parentIDs []string
			for _, evt := range round0Events {
				parentIDs = append(parentIDs, evt.Id)
			}
			
			event := createTestEvent(t, keypair, parentIDs, 1)
			err := dag.AddEvent(event)
			require.NoError(t, err)
		}
		
		assert.Equal(t, 6, dag.EventsCount())
	})

	// Test 3: Finality consensus
	t.Run("FinalityConsensus", func(t *testing.T) {
		// Clear DAG for fresh test
		dag = consensus.NewDAG()
		engine = consensus.NewConsensusEngine(dag)
		
		// Create chain of events
		keypair, _ := crypto.GenerateKeypair()
		
		event1 := createTestEvent(t, keypair, []string{}, 0)
		err := dag.AddEvent(event1)
		require.NoError(t, err)
		
		// Add second round
		event2 := createTestEvent(t, keypair, []string{event1.Id}, 1)
		err = dag.AddEvent(event2)
		require.NoError(t, err)
		
		// Determine fame
		err = engine.DetermineFame(0)
		assert.NoError(t, err)
		
		// Check finality
		finalized := engine.CheckFinality(event1.Id)
		assert.True(t, finalized)
	})

	// Test 4: Complex DAG with forks
	t.Run("ComplexDAGWithForks", func(t *testing.T) {
		dag = consensus.NewDAG()
		engine = consensus.NewConsensusEngine(dag)
		
		validators := createTestValidators(t, 4)
		
		// Create complex DAG structure
		events := createComplexDAG(t, dag, validators, 3)
		assert.Len(t, events, 12) // 4 validators * 3 rounds
		
		// Test ancestor queries
		latestEvent := events[len(events)-1]
		ancestors := dag.GetAncestors(latestEvent.Id)
		assert.Greater(t, len(ancestors), 0)
		
		// Test descendant queries
		genesisEvent := events[0]
		descendants := dag.GetDescendants(genesisEvent.Id)
		assert.Greater(t, len(descendants), 0)
	})

	// Test 5: Transaction inclusion
	t.Run("TransactionInclusion", func(t *testing.T) {
		dag = consensus.NewDAG()
		engine = consensus.NewConsensusEngine(dag)
		
		keypair, _ := crypto.GenerateKeypair()
		
		// Create event with transaction
		tx := createTestTransaction(t, keypair)
		event := createTestEventWithTx(t, keypair, tx)
		
		err := dag.AddEvent(event)
		require.NoError(t, err)
		
		// Verify transaction is in event
		retrievedEvent, exists := dag.GetEvent(event.Id)
		assert.True(t, exists)
		assert.Len(t, retrievedEvent.Transactions, 1)
		assert.Equal(t, tx.Id, retrievedEvent.Transactions[0].Id)
	})

	// Test 6: Validator stake weighting
	t.Run("ValidatorStakeWeighting", func(t *testing.T) {
		engine = consensus.NewConsensusEngine(consensus.NewDAG())
		
		// Add validators with different stakes
		for i, stake := range []uint64{1000, 2000, 3000, 4000} {
			keypair, _ := crypto.GenerateKeypair()
			validator := &consensus.Validator{
				Address:    fmt.Sprintf("validator-%d", i),
				PublicKey:  keypair.Public,
				Stake:      stake,
				IsActive:   true,
			}
			engine.AddValidator(validator)
		}
		
		// Verify total stake calculation
		validator, exists := engine.GetValidator("validator-0")
		assert.True(t, exists)
		assert.Equal(t, uint64(1000), validator.Stake)
	})
}

func TestConsensusPerformance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Test throughput
	t.Run("EventThroughput", func(t *testing.T) {
		dag := consensus.NewDAG()
		
		keypair, _ := crypto.GenerateKeypair()
		
		start := time.Now()
		numEvents := 1000
		
		for i := 0; i < numEvents; i++ {
			event := createTestEvent(t, keypair, []string{}, uint64(i/10))
			err := dag.AddEvent(event)
			require.NoError(t, err)
		}
		
		elapsed := time.Since(start)
		throughput := float64(numEvents) / elapsed.Seconds()
		
		t.Logf("Processed %d events in %v (%.2f events/sec)", numEvents, elapsed, throughput)
		assert.Greater(t, throughput, 100.0) // At least 100 events/sec
	})

	// Test finality speed
	t.Run("FinalitySpeed", func(t *testing.T) {
		dag := consensus.NewDAG()
		engine := consensus.NewConsensusEngine(dag)
		go engine.Start(ctx)
		defer engine.Stop()
		
		keypair, _ := crypto.GenerateKeypair()
		
		// Create events rapidly
		event1 := createTestEvent(t, keypair, []string{}, 0)
		dag.AddEvent(event1)
		
		event2 := createTestEvent(t, keypair, []string{event1.Id}, 1)
		dag.AddEvent(event2)
		
		// Measure finality determination time
		start := time.Now()
		engine.DetermineFame(0)
		elapsed := time.Since(start)
		
		t.Logf("Finality determination took %v", elapsed)
		assert.Less(t, elapsed, 100*time.Millisecond)
	})
}

// Helper functions
func createTestEvent(t *testing.T, keypair *crypto.Keypair, parents []string, round uint64) *pb.Event {
	t.Helper()
	
	event := &pb.Event{
		Id:        generateEventID(),
		Creator:   "test-validator",
		Parents:   parents,
		Timestamp: time.Now().UnixNano(),
		Round:     round,
		Transactions: []*pb.Transaction{},
	}
	
	// Sign event
	err := crypto.SignEvent(event, keypair)
	require.NoError(t, err)
	
	return event
}

func createTestEventWithTx(t *testing.T, keypair *crypto.Keypair, tx *pb.Transaction) *pb.Event {
	t.Helper()
	
	event := createTestEvent(t, keypair, []string{}, 0)
	event.Transactions = []*pb.Transaction{tx}
	
	// Resign event with transaction
	err := crypto.SignEvent(event, keypair)
	require.NoError(t, err)
	
	return event
}

func createTestTransaction(t *testing.T, keypair *crypto.Keypair) *pb.Transaction {
	t.Helper()
	
	tx := &pb.Transaction{
		ChainId:   "testnet",
		NetworkId: "testnet-v1",
		Nonce:     uint64(time.Now().Unix()),
		Fee:       1000,
		GasLimit:  50000,
		Timestamp: time.Now().UnixNano(),
		Payload: &pb.Transaction_Transfer{
			Transfer: &pb.TransferPayload{
				To:     "recipient",
				Amount: 1000,
			},
		},
		PublicKey: keypair.Public,
	}
	
	err := crypto.SignTransaction(tx, keypair)
	require.NoError(t, err)
	
	return tx
}

func createTestValidators(t *testing.T, num int) []*crypto.Keypair {
	t.Helper()
	
	var validators []*crypto.Keypair
	for i := 0; i < num; i++ {
		keypair, err := crypto.GenerateKeypair()
		require.NoError(t, err)
		validators = append(validators, keypair)
	}
	
	return validators
}

func createComplexDAG(t *testing.T, dag *consensus.DAG, validators []*crypto.Keypair, rounds int) []*pb.Event {
	t.Helper()
	
	var allEvents []*pb.Event
	var previousRoundEvents []*pb.Event
	
	// Round 0: Genesis witnesses
	for _, keypair := range validators {
		event := createTestEvent(t, keypair, []string{}, 0)
		err := dag.AddEvent(event)
		require.NoError(t, err)
		allEvents = append(allEvents, event)
		previousRoundEvents = append(previousRoundEvents, event)
	}
	
	// Subsequent rounds
	for round := uint64(1); round < uint64(rounds); round++ {
		var currentRoundEvents []*pb.Event
		
		for _, keypair := range validators {
			// Use all previous round events as parents
			var parentIDs []string
			for _, evt := range previousRoundEvents {
				parentIDs = append(parentIDs, evt.Id)
			}
			
			event := createTestEvent(t, keypair, parentIDs, round)
			err := dag.AddEvent(event)
			require.NoError(t, err)
			
			allEvents = append(allEvents, event)
			currentRoundEvents = append(currentRoundEvents, event)
		}
		
		previousRoundEvents = currentRoundEvents
	}
	
	return allEvents
}

func generateEventID() string {
	return fmt.Sprintf("event-%d-%d", time.Now().UnixNano(), rand.Int63())
}