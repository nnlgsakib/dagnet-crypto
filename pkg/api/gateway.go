package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/mempool"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/state"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// APIServer represents the production API server
type APIServer struct {
	config    *config.Config
	grpcSrv   *grpc.Server
	gateway   *http.Server
	storage   *storage.Store
	mempool   *mempool.Mempool
	consensus *consensus.ConsensusEngine
	state     *state.StateMachine
}

// NewAPIServer creates a new API server
func NewAPIServer(
	cfg *config.Config,
	store *storage.Store,
	mp *mempool.Mempool,
	cons *consensus.ConsensusEngine,
	st *state.StateMachine,
) *APIServer {
	return &APIServer{
		config:    cfg,
		storage:   store,
		mempool:   mp,
		consensus: cons,
		state:     st,
	}
}

// Start starts both gRPC and REST gateway servers
func (s *APIServer) Start(ctx context.Context) error {
	// Start gRPC server
	if s.config.API.EnableGRPC {
		if err := s.startGRPC(ctx); err != nil {
			return errors.Wrap(err, "failed to start gRPC server")
		}
	}

	// Start REST gateway
	if s.config.API.EnableREST {
		if err := s.startREST(ctx); err != nil {
			return errors.Wrap(err, "failed to start REST gateway")
		}
	}

	log.Info().Msg("API server started successfully")
	<-ctx.Done()
	return nil
}

// Stop gracefully shuts down the API server
func (s *APIServer) Stop() error {
	log.Info().Msg("Shutting down API server")

	// Shutdown gRPC server
	if s.grpcSrv != nil {
		s.grpcSrv.GracefulStop()
	}

	// Shutdown REST gateway
	if s.gateway != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.gateway.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to shutdown REST gateway")
		}
	}

	log.Info().Msg("API server stopped")
	return nil
}

// startGRPC starts the gRPC server
func (s *APIServer) startGRPC(ctx context.Context) error {
	// Create listener
	lis, err := net.Listen("tcp", s.config.API.GRPCListenAddress)
	if err != nil {
		return errors.Wrap(err, "failed to create gRPC listener")
	}

	// Create gRPC server with middleware
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 * 1024 * 1024), // 4MB
		grpc.MaxSendMsgSize(4 * 1024 * 1024), // 4MB
		grpc.ConnectionTimeout(30 * time.Second),
		grpc.ChainUnaryInterceptor(
			loggingInterceptor,
			recoveryInterceptor,
			rateLimitInterceptor,
		),
		grpc.ChainStreamInterceptor(
			streamLoggingInterceptor,
			streamRecoveryInterceptor,
		),
	}

	s.grpcSrv = grpc.NewServer(opts...)

	// Register services
	pb.RegisterNodeServiceServer(s.grpcSrv, &nodeService{s})
	pb.RegisterWalletServiceServer(s.grpcSrv, &walletService{s})
	pb.RegisterAdminServiceServer(s.grpcSrv, &adminService{s})

	// Start serving
	go func() {
		log.Info().Str("address", s.config.API.GRPCListenAddress).Msg("gRPC server listening")
		if err := s.grpcSrv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("Failed to serve gRPC")
		}
	}()

	// Monitor context for shutdown
	go func() {
		<-ctx.Done()
		s.grpcSrv.GracefulStop()
	}()

	return nil
}

// startREST starts the REST gateway
func (s *APIServer) startREST(ctx context.Context) error {
	// Create gRPC connection for gateway
	grpcConn, err := grpc.DialContext(
		ctx,
		s.config.API.GRPCListenAddress,
		grpc.WithInsecure(),
		grpc.WithBlock(),
	)
	if err != nil {
		return errors.Wrap(err, "failed to dial gRPC server")
	}

	// Create gateway mux
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: protojson.MarshalOptions{
					UseProtoNames:   true,
					EmitUnpopulated: true,
				},
				UnmarshalOptions: protojson.UnmarshalOptions{
					DiscardUnknown: true,
				},
			},
		}),
		runtime.WithErrorHandler(customErrorHandler),
		runtime.WithForwardResponseOption(customResponseModifier),
	)

	// Register services
	if err := pb.RegisterNodeServiceHandler(ctx, mux, grpcConn); err != nil {
		return errors.Wrap(err, "failed to register node service handler")
	}
	if err := pb.RegisterWalletServiceHandler(ctx, mux, grpcConn); err != nil {
		return errors.Wrap(err, "failed to register wallet service handler")
	}
	if err := pb.RegisterAdminServiceHandler(ctx, mux, grpcConn); err != nil {
		return errors.Wrap(err, "failed to register admin service handler")
	}

	// Add health check
	mux.HandlePath("GET", "/health", s.healthHandler)
	mux.HandlePath("GET", "/metrics", s.metricsHandler)

	// Create HTTP server
	s.gateway = &http.Server{
		Addr:    s.config.API.RESTListenAddress,
		Handler: mux,
		// Security settings
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start serving
	go func() {
		log.Info().Str("address", s.config.API.RESTListenAddress).Msg("REST gateway listening")
		if err := s.gateway.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to serve REST gateway")
		}
	}()

	return nil
}

// Interceptors
customErrorHandler implements proper error handling
func customErrorHandler(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(runtime.HTTPStatusFromCode(status.Code(err)))
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   err.Error(),
		"code":    int(status.Code(err)),
		"message": status.Convert(err).Message(),
	})
}

// customResponseModifier adds common headers to responses
func customResponseModifier(ctx context.Context, w http.ResponseWriter, p protoiface.MessageV1) error {
	w.Header().Set("X-NDAG-Version", "1.0.0")
	w.Header().Set("X-NDAG-Network", "ndag-mainnet")
	return nil
}

// Handlers
func (s *APIServer) healthHandler(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	
	status := map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
		"network": s.config.Network.NetworkID,
		"chain":   s.config.Network.ChainID,
		"uptime":  time.Since(time.Now()).String(),
	}
	
	json.NewEncoder(w).Encode(status)
}

func (s *APIServer) metricsHandler(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	// Prometheus metrics endpoint
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	// TODO: Implement actual metrics
	w.Write([]byte("# NDAG Coin Metrics\nnode_health 1\n"))
}