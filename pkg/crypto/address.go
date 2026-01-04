package crypto

import (
    "encoding/hex"
    "fmt"

    "lukechampine.com/blake3"
)

// Address represents a blockchain address
type Address string

const AddressPrefix = "ndag"

// GetAddressFromPublicKey generates an address from a public key
func GetAddressFromPublicKey(pubKey PublicKey) string {
    if len(pubKey) == 0 {
        return ""
    }
    
    // Take first 8 bytes of public key hash
    hash := blake3.Sum256(pubKey)
    return fmt.Sprintf("%s%s", AddressPrefix, hex.EncodeToString(hash[:8]))
}

// ValidateAddress checks if an address is valid
// ValidateAddress performs comprehensive validation for production use
func ValidateAddress(address string) error {
    if address == "" {
        return errors.New("address cannot be empty")
    }
    
    if len(address) != 44 { 
        return fmt.Errorf("invalid address length: %d (expected 44: 'ndag' + 40 hex chars)", len(address))
    }
    
    if address[:4] != AddressPrefix {
        return fmt.Errorf("invalid address prefix: expected 'ndag', got '%s'", address[:4])
    }
    
    hexPart := address[4:]
    if len(hexPart) != 40 {
        return fmt.Errorf("invalid hex part length: %d (expected 40)", len(hexPart))
    }
    
    // Check if hex part is valid
    decoded, err := hex.DecodeString(hexPart)
    if err != nil {
        return fmt.Errorf("invalid address hex encoding: %v", err)
    }
    
    if len(decoded) != 20 {
        return fmt.Errorf("invalid decoded address length: %d (expected 20)", len(decoded))
    }
    
    return nil
}

// String returns the string representation of an address
func (a Address) String() string {
    return string(a)
}

// Bytes returns the address as bytes
func (a Address) Bytes() []byte {
    if len(a) < 4 {
        return nil
    }
    bytes, _ := hex.DecodeString(string(a[4:]))
    return bytes
}

// IsValid returns true if the address is valid
func (a Address) IsValid() bool {
    return ValidateAddress(string(a)) == nil
}