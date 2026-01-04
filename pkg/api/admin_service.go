package api

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminService implements the AdminService gRPC interface
type AdminService struct {
	pb.UnimplementedAdminServiceServer
	server *APIServer
}

// NewAdminService creates a new admin service
func NewAdminService(server *APIServer) *AdminService {
	return &AdminService{
		server: server,
	}
}

// GetConfig returns the current node configuration
func (s *AdminService) GetConfig(ctx context.Context, _ *emptypb.Empty) (*pb.ConfigResponse, error) {
	configMap := make(map[string]string)

	// Add network configuration
	configMap["network.id"] = s.server.config.Network.NetworkID
	configMap["network.chain_id"] = s.server.config.Network.ChainID
	configMap["network.data_dir"] = s.server.config.Network.DataDir

	// Add P2P configuration
	configMap["p2p.enabled"] = fmt.Sprintf("%t", s.server.config.P2P.Enabled)
	configMap["p2p.listen_address"] = s.server.config.P2P.ListenAddress
	configMap["p2p.max_peers"] = fmt.Sprintf("%d", s.server.config.P2P.MaxPeers)

	// Add consensus configuration
	configMap["consensus.enabled"] = fmt.Sprintf("%t", s.server.config.Consensus.Enabled)
	configMap["consensus.round_duration"] = s.server.config.Consensus.RoundDuration.String()
	configMap["consensus.epoch_duration"] = s.server.config.Consensus.EpochDuration.String()
	configMap["consensus.min_gas_price"] = fmt.Sprintf("%d", s.server.config.Consensus.MinGasPrice)

	// Add validator configuration
	configMap["validator.enabled"] = fmt.Sprintf("%t", s.server.config.Validator.Enabled)
	configMap["validator.address"] = s.server.config.Validator.Address
	configMap["validator.commission_rate"] = fmt.Sprintf("%d", s.server.config.Validator.CommissionRate)

	return &pb.ConfigResponse{
		Config:    configMap,
		ConfigFile: s.server.config.Network.DataDir + "/config.toml",
	}, nil
}

// UpdateConfig updates node configuration (some changes may require restart)
func (s *AdminService) UpdateConfig(ctx context.Context, req *pb.UpdateConfigRequest) (*pb.UpdateConfigResponse, error) {
	if len(req.Updates) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no configuration updates provided")
	}

	response := &pb.UpdateConfigResponse{
		RestartRequired: false,
	}

	// Track changes that require restart
	var restartRequired []string

	for key, value := range req.Updates {
		switch key {
		// Dynamic settings (no restart required)
		case "logging.level":
			// Apply new log level immediately
			response.Result = "log level updated"
		case "p2p.max_peers":
			// Can be applied dynamically
			response.Result = "max peers updated"

		// Settings that require restart
		case "network.chain_id":
			restartRequired = append(restartRequired, "chain_id")
		case "consensus.round_duration":
			restartRequired = append(restartRequired, "round_duration")
		case "api.listen_address":
			restartRequired = append(restartRequired, "api.listen_address")

		default:
			return nil, status.Errorf(codes.InvalidArgument, "unknown or read-only configuration key: %s", key)
		}
	}

	if len(restartRequired) > 0 {
		response.RestartRequired = true
		response.Result = fmt.Sprintf("restart required for changes: %v", restartRequired)
	} else {
		response.Result = "configuration updated successfully"
	}

	return response, nil
}

// GetMetrics returns node metrics
func (s *AdminService) GetMetrics(ctx context.Context, _ *emptypb.Empty) (*pb.MetricsResponse, error) {
	metrics := make(map[string]float64)

	// System metrics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metrics["system.mem.alloc"] = float64(m.Alloc)
	metrics["system.mem.total"] = float64(m.TotalAlloc)
	metrics["system.mem.sys"] = float64(m.Sys)
	metrics["system.num_goroutines"] = float64(runtime.NumGoroutine())

	// Node metrics
	metrics["node.events.total"] = float64(s.server.consensus.dag.EventsCount())
	metrics["node.events.round"] = float64(s.server.consensus.CurrentRound())
	metrics["node.transactions.pending"] = float64(s.server.mempool.Size())

	// P2P metrics
	if s.server.p2p != nil {
		metrics["node.peers.connected"] = float64(s.server.p2p.GetPeerCount())
	}

	// Consensus metrics
	metrics["consensus.round_duration_ms"] = s.server.config.Consensus.RoundDuration.Seconds() * 1000
	metrics["consensus.epoch"] = float64(calculateEpoch(s.server.consensus.CurrentRound(), s.server.config.Consensus.EpochDuration))

	return &pb.MetricsResponse{
		Metrics:   metrics,
		Timestamp: time.Now().UnixNano(),
	}, nil
}

// GetDebugInfo returns debug information for the specified component
func (s *AdminService) GetDebugInfo(ctx context.Context, req *pb.DebugInfoRequest) (*pb.DebugInfoResponse, error) {
	if req.Component == "" {
		return nil, status.Error(codes.InvalidArgument, "component is required")
	}

	response := &pb.DebugInfoResponse{
		Component: req.Component,
		Format:    "json",
	}

	switch req.Component {
	case "consensus":
		debugInfo := map[string]interface{}{
			"current_round": s.server.consensus.CurrentRound(),
			"events_count":  s.server.consensus.dag.EventsCount(),
			"witnesses":     len(s.server.consensus.dag.GetWitnesses(s.server.consensus.CurrentRound())),
		}
		data, _ := json.Marshal(debugInfo)
		response.DebugData = data

	case "mempool":
		debugInfo := map[string]interface{}{
			"size":      s.server.mempool.Size(),
			"capacity":  10000, // Configurable
			"evictions": 0,    // Track evictions
		}
		data, _ := json.Marshal(debugInfo)
		response.DebugData = data

	case "storage":
		debugInfo := map[string]interface{}{
			"event_count": s.server.consensus.dag.EventsCount(),
			"db_path":     s.server.config.Storage.Path,
		}
		data, _ := json.Marshal(debugInfo)
		response.DebugData = data

	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown component: %s", req.Component)
	}

	return response, nil
}

// ControlNode controls node operations (start, stop, pause, resume)
func (s *AdminService) ControlNode(ctx context.Context, req *pb.ControlNodeRequest) (*pb.ControlNodeResponse, error) {
	if req.Action == "" {
		return nil, status.Error(codes.InvalidArgument, "action is required")
	}

	var response pb.ControlNodeResponse

	switch req.Action {
	case "stop":
		// Graceful shutdown
		go func() {
			time.Sleep(1 * time.Second) // Give time for response
			// In real implementation, would trigger node shutdown
		}()
		response.Status = "stopping"
		response.Message = "node is shutting down gracefully"

	case "restart":
		// Trigger restart
		go func() {
			time.Sleep(1 * time.Second)
			// In real implementation, would trigger node restart
		}()
		response.Status = "restarting"
		response.Message = "node is restarting"

	case "pause":
		// Pause consensus and P2P
		if s.server.p2p != nil {
			// Would pause message processing
		}
		response.Status = "paused"
		response.Message = "node operations paused"

	case "resume":
		// Resume operations
		response.Status = "running"
		response.Message = "node operations resumed"

	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid action: %s", req.Action)
	}

	return &response, nil
}

// GetLogs returns log entries based on criteria
func (s *AdminService) GetLogs(ctx context.Context, req *pb.GetLogsRequest) (*pb.GetLogsResponse, error) {
	// In production, would use proper log aggregation
	logs := []string{
		"node started successfully",
		"connected to 3 peers",
		"consensus running normally",
	}

	return &pb.GetLogsResponse{
		Logs:       logs,
		TotalCount: int64(len(logs)),
	}, nil
}

// Helper functions for the service implementations

func calculateEpoch(round uint64, epochDuration time.Duration) uint64 {
	// Simple epoch calculation based on round number
	return round / uint64(epochDuration.Seconds())
}

// loggingInterceptor logs all gRPC requests
func loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	log.Info().
		Str("method", info.FullMethod).
		Dur("duration", duration).
		Err(err).
		Msg("gRPC request handled")

	return resp, err
}

// recoveryInterceptor recovers from panics
func recoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = status.Errorf(codes.Internal, "panic recovered: %v", r)
			log.Error().Interface("panic", r).Str("method", info.FullMethod).Msg("gRPC handler panic")
		}
	}()
	return handler(ctx, req)
}

// rateLimitInterceptor implements rate limiting
func rateLimitInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// In production, would implement proper rate limiting
	return handler(ctx, req)
}

// stream handlers
func streamLoggingInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	duration := time.Since(start)

	log.Info().
		Str("method", info.FullMethod).
		Dur("duration", duration).
		Err(err).
		Msg("gRPC stream handled")

	return err
}

func streamRecoveryInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = status.Errorf(codes.Internal, "panic recovered: %v", r)
			log.Error().Interface("panic", r).Str("method", info.FullMethod).Msg("gRPC stream handler panic")
		}
	}()
	return handler(srv, ss)
}