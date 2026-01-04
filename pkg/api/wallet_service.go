package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// WalletService implements the WalletService gRPC interface
type WalletService struct {
	pb.UnimplementedWalletServiceServer
	server   *APIServer
	keystore *crypto.KeyStore
}

// NewWalletService creates a new wallet service
func NewWalletService(server *APIServer) (*WalletService, error) {
	keystore, err := crypto.NewKeyStore(server.config.GetFullDataDir() + "/keystore")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create keystore")
	}

	return &WalletService{
		server:   server,
		keystore: keystore,
	}, nil
}

// GenerateKey generates a new keypair and stores it encrypted
func (s *WalletService) GenerateKey(ctx context.Context, req *pb.GenerateKeyRequest) (*pb.GenerateKeyResponse, error) {
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Generate new keypair
	keypair, err := s.keystore.GenerateKey(req.KeyName, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate key: %v", err)
	}

	// Generate mnemonic (simplified for demo)
	var mnemonic string
	if req.Mnemonic != nil && *req.Mnemonic != "" {
		mnemonic = *req.Mnemonic
	} else {
		// Generate 24-word mnemonic
		entropy := make([]byte, 32)
		if _, err := rand.Read(entropy); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate entropy: %v", err)
		}
		// In production: use proper BIP39 mnemonic generation
		mnemonic = "abandon ability able about above absent absorb abstract absurd abuse access accident"
	}

	return &pb.GenerateKeyResponse{
		Address:   fmt.Sprintf("ndag%s", hex.EncodeToString(keypair.Public[:8])),
		PublicKey: hex.EncodeToString(keypair.Public),
		Mnemonic:  mnemonic,
	}, nil
}

// ImportKey imports an existing private key
func (s *WalletService) ImportKey(ctx context.Context, req *pb.ImportKeyRequest) (*pb.ImportKeyResponse, error) {
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}
	if req.PrivateKey == "" {
		return nil, status.Error(codes.InvalidArgument, "private_key is required")
	}

	// Decode private key
	privateKey, err := hex.DecodeString(req.PrivateKey)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid private key format: %v", err)
	}

	// Import key
	keypair, err := s.keystore.ImportKey(req.KeyName, req.Password, privateKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to import key: %v", err)
	}

	return &pb.ImportKeyResponse{
		Address:   fmt.Sprintf("ndag%s", hex.EncodeToString(keypair.Public[:8])),
		PublicKey: hex.EncodeToString(keypair.Public),
		Result:    "success",
		CreatedAt: time.Now().UnixNano(),
	}, nil
}

// ListKeys returns all keys in the keystore
func (s *WalletService) ListKeys(ctx context.Context, _ *emptypb.Empty) (*pb.ListKeysResponse, error) {
	names := s.keystore.ListKeys()
	keys := make([]*pb.KeyInfo, len(names))

	for i, name := range names {
		info, err := s.keystore.GetKeyInfo(name)
		if err != nil {
			continue // Skip keys that can't be loaded
		}

		keys[i] = &pb.KeyInfo{
			Name:      name,
			Address:   info.Address,
			PublicKey: hex.EncodeToString(info.PublicKey),
			CreatedAt: info.CreatedAt.UnixNano(),
			IsActive:  info.IsActive,
		}
	}

	return &pb.ListKeysResponse{
		Keys: keys,
	}, nil
}

// GetKey returns key information
func (s *WalletService) GetKey(ctx context.Context, req *pb.GetKeyRequest) (*pb.GetKeyResponse, error) {
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}

	info, err := s.keystore.GetKeyInfo(req.KeyName)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "key not found: %v", err)
	}

	return &pb.GetKeyResponse{
		KeyInfo: &pb.KeyInfo{
			Name:      req.KeyName,
			Address:   info.Address,
			PublicKey: hex.EncodeToString(info.PublicKey),
			CreatedAt: info.CreatedAt.UnixNano(),
			IsActive:  info.IsActive,
		},
	}, nil
}

// DeleteKey deletes a key from the keystore
func (s *WalletService) DeleteKey(ctx context.Context, req *pb.DeleteKeyRequest) (*pb.DeleteKeyResponse, error) {
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Verify password by trying to get the key
	_, err := s.keystore.GetKey(req.KeyName, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "invalid password: %v", err)
	}

	// Delete the key
	if err := s.keystore.DeleteKey(req.KeyName); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete key: %v", err)
	}

	return &pb.DeleteKeyResponse{
		Result: "success",
	}, nil
}

// SignTx signs a transaction with the specified key
func (s *WalletService) SignTx(ctx context.Context, req *pb.SignTxRequest) (*pb.SignTxResponse, error) {
	if req.Transaction == nil {
		return nil, status.Error(codes.InvalidArgument, "transaction is required")
	}
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Get the key
	keypair, err := s.keystore.GetKey(req.KeyName, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "failed to get key: %v", err)
	}

	// Remove existing signature
	originalSig := req.Transaction.Signature
	req.Transaction.Signature = nil
	req.Transaction.PublicKey = keypair.Public

	// Sign the transaction
	if err := crypto.SignTransaction(req.Transaction, keypair); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign transaction: %v", err)
	}

	return &pb.SignTxResponse{
		SignedTransaction: req.Transaction,
		Signature:         req.Transaction.Signature,
		ErrorMessage:      "",
	}, nil
}

// GetMnemonic returns the mnemonic phrase for a key
func (s *WalletService) GetMnemonic(ctx context.Context, req *pb.GetMnemonicRequest) (*pb.GetMnemonicResponse, error) {
	if req.KeyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Verify password by loading the key
	_, err := s.keystore.GetKey(req.KeyName, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "invalid password: %v", err)
	}

	// In production: retrieve actual mnemonic from secure storage
	// For demo: return example mnemonic (in real implementation, properly store/retrieve)
	mnemonic := "abandon ability able about above absent absorb abstract absurd abuse access accident achieve acid acoustic acquire across action actor actress actual adapt add addict address"

	return &pb.GetMnemonicResponse{
		Mnemonic: mnemonic,
	}, nil
}