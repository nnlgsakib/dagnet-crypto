package network

import (
	"context"
	"fmt"

	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/pb"
)

// P2PNetwork represents the P2P network
type P2PNetwork struct {
	config    *config.Config
	eventChan chan *pb.Event
	txChan    chan *pb.Transaction
}

// NewP2PNetwork creates a new P2P network
func NewP2PNetwork(ctx context.Context, cfg *config.Config) (*P2PNetwork, error) {
	if !cfg.P2P.Enabled {
		return nil, fmt.Errorf("P2P network not enabled")
	}
	
	return &P2PNetwork{
		config:    cfg,
		eventChan: make(chan *pb.Event, 100),
		txChan:    make(chan *pb.Transaction, 1000),
	}, nil
}

// Start starts the P2P network
func (n *P2PNetwork) Start(ctx context.Context) error {
	return nil
}

// Stop stops the P2P network
func (n *P2PNetwork) Stop() error {
	close(n.eventChan)
	close(n.txChan)
	return nil
}

// GetEventChannel returns the event channel
func (n *P2PNetwork) GetEventChannel() <-chan *pb.Event {
	return n.eventChan
}

// GetTxChannel returns the transaction channel
func (n *P2PNetwork) GetTxChannel() <-chan *pb.Transaction {
	return n.txChan
}
