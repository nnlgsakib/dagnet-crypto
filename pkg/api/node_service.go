package api

import (
	"context"
	"fmt"
	"time"

	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// NodeService implements the NodeService gRPC interface
type NodeService struct {
	pb.UnimplementedNodeServiceServer
	server *APIServer
}

// SubmitTx submits a transaction to the network
func (s *NodeService) SubmitTx(ctx context.Context, req *pb.SubmitTxRequest) (*pb.SubmitTxResponse, error) {
	if req.Transaction == nil {
		return nil, status.Error(codes.InvalidArgument, "transaction is required")
	}

	// Validate transaction
	if err := s.validateTransaction(req.Transaction); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if transaction already exists
	if exists, _ := s.server.storage.EventStore.Has([]byte(req.Transaction.Id)); exists {
		return &pb.SubmitTxResponse{
			TxId:      req.Transaction.Id,
			Status:    "already_exists",
			Timestamp: time.Now().UnixNano(),
		}, nil
	}

	// Add to mempool
	if !s.server.mempool.Add(req.Transaction) {
		return &pb.SubmitTxResponse{
			TxId:      req.Transaction.Id,
			Status:    "rejected",
			ErrorMessage: "mempool full or duplicate transaction",
			Timestamp: time.Now().UnixNano(),
		}, nil
	}

	// Broadcast to network if enabled
	if s.server.p2p != nil {
		if err := s.server.p2p.BroadcastTransaction(req.Transaction); err != nil {
			log.Warn().Err(err).Str("tx_id", req.Transaction.Id).Msg("Failed to broadcast transaction")
		}
	}

	// If async mode, return immediately
	if req.Async {
		return &pb.SubmitTxResponse{
			TxId:      req.Transaction.Id,
			Status:    "pending",
			Timestamp: time.Now().UnixNano(),
		}, nil
	}

	// Wait for transaction to be included in an event (with timeout)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// In real implementation, would subscribe to event stream and wait for tx
	time.Sleep(100 * time.Millisecond) // Simulate processing

	return &pb.SubmitTxResponse{
		TxId:      req.Transaction.Id,
		Status:    "accepted",
		Timestamp: time.Now().UnixNano(),
	}, nil
}

// GetNodeStatus returns the current node status
func (s *NodeService) GetNodeStatus(ctx context.Context, _ *emptypb.Empty) (*pb.NodeStatus, error) {
	latestRound := s.server.consensus.dag.LatestRound()
	numPeers := 0
	if s.server.p2p != nil {
		numPeers = s.server.p2p.GetPeerCount()
	}

	return &pb.NodeStatus{
		NodeId:          getNodeID(s.server.config),
		Version:         "1.0.0",
		ChainId:         s.server.config.Network.ChainID,
		NetworkId:       s.server.config.Network.NetworkID,
		Status:          getNodeStatus(s.server),
		LatestRound:     latestRound,
		LatestEpoch:     calculateEpoch(latestRound, s.server.config.Consensus.EpochDuration),
		LatestTimestamp: time.Now().UnixNano(),
		NumPeers:        uint64(numPeers),
		NumEvents:       uint64(s.server.consensus.dag.EventsCount()),
		NumTransactions: uint64(s.server.mempool.Size()),
		LatestStateHash: getLatestStateHash(s.server),
		SyncInfo:        getSyncInfo(s.server),
		ValidatorInfo:   getValidatorInfo(s.server),
	}, nil
}

// GetPeers returns connected peer information
func (s *NodeService) GetPeers(ctx context.Context, _ *emptypb.Empty) (*pb.PeerList, error) {
	if s.server.p2p == nil {
		return &pb.PeerList{Peers: []*pb.PeerInfo{}}, nil
	}

	peers := s.server.p2p.GetPeers()
	peerInfos := make([]*pb.PeerInfo, len(peers))

	for i, peer := range peers {
		peerInfos[i] = &pb.PeerInfo{
			PeerId:           peer.ID.String(),
			Address:          peer.Addrs[0].String(),
			ConnectionStatus: "connected",
			ConnectionTime:   time.Now().UnixNano(),
			ProtocolVersion:  []byte("/ndag/1.0.0"),
			ChainId:          s.server.config.Network.ChainID,
		}
	}

	return &pb.PeerList{
		Peers:      peerInfos,
		TotalPeers: uint64(len(peerInfos)),
	}, nil
}

// GetEvent retrieves a specific event by ID
func (s *NodeService) GetEvent(ctx context.Context, req *pb.GetEventRequest) (*pb.Event, error) {
	if req.EventId == "" {
		return nil, status.Error(codes.InvalidArgument, "event_id is required")
	}

	eventData, err := s.server.storage.GetEvent(req.EventId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "event not found: %s", req.EventId)
	}

	var event pb.Event
	if err := proto.Unmarshal(eventData, &event); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal event: %v", err)
	}

	return &event, nil
}

// GetTx retrieves a transaction by ID
func (s *NodeService) GetTx(ctx context.Context, req *pb.GetRequest) (*pb.Transaction, error) {
	if req.TxId == "" {
		return nil, status.Error(codes.InvalidArgument, "tx_id is required")
	}

	txData, err := s.server.storage.GetTransaction(req.TxId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "transaction not found: %s", req.TxId)
	}

	var tx pb.Transaction
	if err := proto.Unmarshal(txData, &tx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal transaction: %v", err)
	}

	return &tx, nil
}

// GetValidatorSet returns the current validator set
func (s *NodeService) GetValidatorSet(ctx context.Context, _ *emptypb.Empty) (*pb.ValidatorSet, error) {
	// In production, would load from state store
	return &pb.ValidatorSet{
		Validators: []*pb.Validator{},
		TotalStake: 0,
	}, nil
}

// GetFeeInfo returns current fee market information
func (s *NodeService) GetFeeInfo(ctx context.Context, _ *emptypb.Empty) (*pb.FeeInfo, error) {
	// Calculate dynamic fees based on recent rounds
	baseFee := s.server.config.Consensus.MinGasPrice
	maxFee := baseFee * 100 // 100x base fee during congestion

	// Calculate gas usage from recent events
	gasUsed := uint64(0)
	latestRound := s.server.consensus.CurrentRound()
	events := s.server.consensus.dag.GetEventsByRound(latestRound)
	
	for _, event := range events {
		gasUsed += uint64(len(event.Transactions)) * 50000 // Approximate gas per tx
	}

	targetGas := s.server.config.Consensus.TargetGasPerRound

	return &pb.FeeInfo{
		BaseFee:            baseFee,
		MinFee:             baseFee,
		MaxFee:             maxFee,
		GasPrice:           baseFee,
		TargetGasPerRound:  targetGas,
		CurrentGasUsed:     gasUsed,
		MaxGasPerRound:     s.server.config.Consensus.MaxGasPerRound,
	}, nil
}

// EstimateGas estimates gas for a transaction
func (s *NodeService) EstimateGas(ctx context.Context, req *pb.EstimateGasRequest) (*pb.EstimateGasResponse, error) {
	if req.Transaction == nil {
		return nil, status.Error(codes.InvalidArgument, "transaction is required")
	}

	// Base gas cost
	gas := uint64(50000)

	// Add gas based on transaction type and complexity
	switch req.Transaction.Payload.(type) {
	case *pb.Transaction_createValidator:
		gas = 5000000 // 5M gas for validator creation
	case *pb.Transaction_CreateToken:
		gas = 1000000 // 1M gas for token creation
	case *pb.Transaction_CreateCollection:
		gas = 2000000 // 2M gas for collection creation
	case *pb.Transaction_MintNft:
		gas = 500000 // 500K gas for NFT minting
	}

	// Multiply by transaction size factor
	txSize := len(req.Transaction.String())
	gas += uint64(txSize * 10) // 10 gas per byte

	return &pb.EstimateGasResponse{
		EstimatedGas:  gas,
		EstimatedFee:  gas * s.server.config.Consensus.MinGasPrice,
		ErrorMessage:  "",
	}, nil
}

// Helper functions
func (s *NodeService) validateTransaction(tx *pb.Transaction) error {
	if tx.ChainId != s.server.config.Network.ChainID {
		return fmt.Errorf("invalid chain ID: %s", tx.ChainId)
	}
	if tx.NetworkId != s.server.config.Network.NetworkID {
		return fmt.Errorf("invalid network ID: %s", tx.NetworkId)
	}
	if tx.Fee < s.server.config.Consensus.MinGasPrice {
		return fmt.Errorf("fee below minimum: %d < %d", tx.Fee, s.server.config.Consensus.MinGasPrice)
	}
	if tx.GasLimit > s.server.config.Consensus.MaxGasPerRound {
		return fmt.Errorf("gas limit exceeds maximum: %d > %d", tx.GasLimit, s.server.config.Consensus.MaxGasPerRound)
	}
	return nil
}

func getNodeID(cfg *config.Config) string {
	if cfg.Validator.Enabled && cfg.Validator.Address != "" {
		return cfg.Validator.Address
	}
	return "ndag-node-" + time.Now().Format("20060102150405")
}

func getNodeStatus(server *APIServer) string {
	if server.p2p != nil && server.p2p.GetPeerCount() > 0 {
		return "active"
	}
	if server.p2p != nil {
		return "syncing"
	}
	return "standalone"
}

func getLatestStateHash(server *APIServer) []byte {
	// In production, would return actual state hash
	return []byte{}
}

func getSyncInfo(server *APIServer) *pb.SyncInfo {
	return &pb.SyncInfo{
		LatestEventHeight: server.consensus.CurrentRound(),
		LatestEventRound:  server.consensus.CurrentRound(),
		CatchingUp:        0,
		TotalSyncedEvents: uint64(server.consensus.dag.EventsCount()),
	}
}

func getValidatorInfo(server *APIServer) *pb.ValidatorInfo {
	return &pb.ValidatorInfo{
		IsValidator:   server.config.IsValidator(),
		ValidatorAddress: server.config.Validator.Address,
		VotingPower:   0,
		CommissionRate: server.config.Validator.CommissionRate,
		IsJailed:      false,
	}
}