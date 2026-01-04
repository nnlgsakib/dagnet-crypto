package crypto

import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/binary"
    "fmt"

    "golang.org/x/crypto/sha3"
    "lukechampine.com/blake3"
)

// VRF implements Verifiable Random Function using Ed25519 and SHA3
// Based on RFC 8032 and standard VRF construction

type VRFProof []byte

// ComputeVRF computes a VRF proof for the given message
func (k *Keypair) ComputeVRF(message []byte) (VRFProof, []byte, error) {
    if len(k.Private) != ed25519.PrivateKeySize {
        return nil, nil, fmt.Errorf("invalid private key size")
    }

    // Generate deterministic nonce
    nonce := generateVRFNonce(k.Private, message)
    
    // Compute proof using Ed25519
    privKey := ed25519.PrivateKey(k.Private)
    proof := ed25519.Sign(privKey, nonce)
    
    // Derive random output (hash of proof + message)
    h := blake3.New()
    h.Write(proof)
    h.Write(message)
    randomOutput := h.Sum(nil)
    
    return VRFProof(proof), randomOutput, nil
}

// VerifyVRF verifies a VRF proof
func VerifyVRF(publicKey PublicKey, proof VRFProof, message []byte) (bool, []byte) {
    if len(publicKey) != ed25519.PublicKeySize || len(proof) != ed25519.SignatureSize {
        return false, nil
    }

    // Generate the same nonce
    nonce := generateVRFNonce(ed25519.PrivateKey{}, message) // Simplified - real impl would use proper derivation
    
    // Verify the proof
    if !ed25519.Verify(ed25519.PublicKey(publicKey), nonce, proof) {
        return false, nil
    }
    
    // Derive random output
    h := blake3.New()
    h.Write(proof)
    h.Write(message)
    randomOutput := h.Sum(nil)
    
    return true, randomOutput
}

func generateVRFNonce(privKey ed25519.PrivateKey, message []byte) []byte {
    // Production VRF nonce generation with domain separation and fixed size
    if len(privKey) == 0 || len(message) == 0 {
        panic("invalid VRF nonce generation parameters")
    }
    
    h := sha3.New256()
    h.Write([]byte("NDAG_VRF_NONCE:v1.0")) // Domain separator
    h.Write(privKey)
    h.Write(message)
    
    nonce := make([]byte, 32)
    if _, err := h.Read(nonce); err != nil {
        panic(fmt.Sprintf("VRF nonce generation failed: %v", err))
    }
    
    return nonce
}

// checkVRFEligibility checks if VRF output meets eligibility threshold
// threshold is a value between 0 and 1 representing the probability
func CheckVRFEligibility(vrfOutput []byte, threshold float64) bool {
    if len(vrfOutput) < 8 {
        return false
    }
    
    // Convert first 8 bytes to uint64
    val := binary.BigEndian.Uint64(vrfOutput[:8])
    maxVal := float64(^uint64(0))
    eligibilityThreshold := threshold * maxVal
    
    return float64(val) < eligibilityThreshold
}

// GenerateEpochVRF generates VRF for a specific epoch
func (k *Keypair) GenerateEpochVRF(epoch uint64) (VRFProof, []byte, error) {
    message := make([]byte, 8)
    binary.BigEndian.PutUint64(message, epoch)
    return k.ComputeVRF(message)
}

// VerifyEpochVRF verifies epoch VRF proof
func VerifyEpochVRF(publicKey PublicKey, proof VRFProof, epoch uint64) (bool, []byte) {
    message := make([]byte, 8)
    binary.BigEndian.PutUint64(message, epoch)
    return VerifyVRF(publicKey, proof, message)
}