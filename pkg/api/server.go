package api

import (
    "context"
    "net"
    "net/http"
    "time"

    "github.com/ndag/ndagcoin/internal/config"
    "github.com/ndag/ndagcoin/pkg/pb"
    "google.golang.org/grpc"
)

// Server represents the API server
type Server struct {
    config    *config.Config
    grpcServer *grpc.Server
    restServer *http.Server
}

// NewServer creates a new API server
func NewServer(cfg *config.Config) *Server {
    return &Server{
        config: cfg,
    }
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
    // Start gRPC server if enabled
    if s.config.API.EnableGRPC {
        if err := s.startGRPC(ctx); err != nil {
            return err
        }
    }
    
    // Start REST server if enabled
    if s.config.API.EnableREST {
        if err := s.startREST(ctx); err != nil {
            return err
        }
    }
    
    <-ctx.Done()
    return nil
}

// Stop stops the API server
func (s *Server) Stop() error {
    if s.grpcServer != nil {
        s.grpcServer.GracefulStop()
    }
    if s.restServer != nil {
        return s.restServer.Shutdown(context.Background())
    }
    return nil
}

// startGRPC starts the gRPC server
func (s *Server) startGRPC(ctx context.Context) error {
    lis, err := net.Listen("tcp", s.config.API.GRPCListenAddress)
    if err != nil {
        return err
    }
    
    s.grpcServer = grpc.NewServer()
    pb.RegisterNodeServiceServer(s.grpcServer, &nodeService{})
    pb.RegisterWalletServiceServer(s.grpcServer, &walletService{})
    pb.RegisterAdminServiceServer(s.grpcServer, &adminService{})
    
    go func() {
        <-ctx.Done()
        s.grpcServer.GracefulStop()
    }()
    
    go s.grpcServer.Serve(lis)
    return nil
}

// startREST starts the REST server
func (s *Server) startREST(ctx context.Context) error {
    mux := http.NewServeMux()
    
    // Health check
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`))
    })
    
    // Status
    mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"status":"running","network":"ndag-devnet"}`))
    })
    
    s.restServer = &http.Server{
        Addr:    s.config.API.RESTListenAddress,
        Handler: mux,
    }
    
    go func() {
        <-ctx.Done()
        s.restServer.Shutdown(context.Background())
    }()
    
    go s.restServer.ListenAndServe()
    return nil
}

// nodeService implements the NodeService
var _ pb.NodeServiceServer = (*nodeService)(nil)

type nodeService struct {
    pb.UnimplementedNodeServiceServer
}

func (s *nodeService) SubmitTx(ctx context.Context, req *pb.SubmitTxRequest) (*pb.SubmitTxResponse, error) {
    // TODO: Implement tx submission
    return &pb.SubmitTxResponse{
        TxId:       "",
        Status:     "pending",
        Timestamp:  time.Now().UnixNano(),
    }, nil
}

// walletService implements the WalletService
var _ pb.WalletServiceServer = (*walletService)(nil)

type walletService struct {
    pb.UnimplementedWalletServiceServer
}

// adminService implements the AdminService  
var _ pb.AdminServiceServer = (*adminService)(nil)

type adminService struct {
    pb.UnimplementedAdminServiceServer
}
