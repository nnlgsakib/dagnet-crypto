package tests

import (
	"testing"

	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeypair(t *testing.T) {
	keypair, err := crypto.GenerateKeypair()
	require.NoError(t, err)
	assert.NotNil(t, keypair)
	assert.NotNil(t, keypair.Private)
	assert.NotNil(t, keypair.Public)
}

func TestSignAndVerify(t *testing.T) {
	keypair, err := crypto.GenerateKeypair()
	require.NoError(t, err)

	data := []byte("test data")
	signature, err := keypair.Sign(data)
	require.NoError(t, err)
	
	// Verify valid signature
	valid := crypto.Verify(keypair.Public, data, signature)
	assert.True(t, valid)
	
	// Verify invalid signature
	invalidData := []byte("different data")
	valid = crypto.Verify(keypair.Public, invalidData, signature)
	assert.False(t, valid)
}

func TestHashEvent(t *testing.T) {
	event := &pb.Event{
		Id:        "test-event",
		Creator:   "validator1",
		Parents:   []string{"parent1", "parent2"},
		Timestamp: 1234567890,
		Round:     1,
	}
	
	hash := crypto.HashEvent(event)
	assert.NotNil(t, hash)
	assert.False(t, hash.IsZero())
	assert.Equal(t, 32, len(hash.Bytes()))
}

func TestHashTransaction(t *testing.T) {
	tx := &pb.Transaction{
		Id:        "test-tx",
		ChainId:   "testnet",
		NetworkId: "testnet-v1",
		Nonce:     1,
		Fee:       1000,
		GasLimit:  50000,
		Timestamp: 1234567890,
	}
	
	hash := crypto.HashTransaction(tx)
	assert.NotNil(t, hash)
	assert.False(t, hash.IsZero())
}

func TestTransactionSigning(t *testing.T) {
	keypair, err := crypto.GenerateKeypair()
	require.NoError(t, err)
	
	tx := &pb.Transaction{
		ChainId:   "testnet",
		NetworkId: "testnet-v1",
		Nonce:     1,
		Fee:       1000,
		GasLimit:  50000,
		Timestamp: 1234567890,
		PublicKey: keypair.Public,
	}
	
	// Sign transaction
	err = crypto.SignTransaction(tx, keypair)
	require.NoError(t, err)
	assert.NotEmpty(t, tx.Signature)
	assert.Equal(t, keypair.Public, tx.PublicKey)
	
	// Verify signature
	valid := crypto.VerifyTransactionSignature(tx)
	assert.True(t, valid)
}

func TestVRFCreation(t *testing.T) {
	keypair, err := crypto.GenerateKeypair()
	require.NoError(t, err)
	
	message := []byte("test message")
	proof, output, err := keypair.ComputeVRF(message)
	require.NoError(t, err)
	assert.NotNil(t, proof)
	assert.NotNil(t, output)
}

func TestVRFVerification(t *testing.T) {
	keypair, err := crypto.GenerateKeypair()
	require.NoError(t, err)
	
	message := []byte("test message")
	proof, expectedOutput, err := keypair.ComputeVRF(message)
	require.NoError(t, err)
	
	// Verify VRF
	valid, output := crypto.VerifyVRF(keypair.Public, proof, message)
	assert.True(t, valid)
	assert.Equal(t, expectedOutput, output)
	
	// Verify with wrong message
	wrongMessage := []byte("wrong message")
	valid, _ = crypto.VerifyVRF(keypair.Public, proof, wrongMessage)
	assert.False(t, valid)
}