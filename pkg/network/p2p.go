package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/multiformats/go-multiaddr"
	"github.com/ndag/ndagcoin/internal/config"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
)

const (
	eventTopic   = "/ndag/events/v1"
	txTopic      = "/ndag/txs/v1"
	poTopic      = "/ndag/po/v1" // Proof of participation
	protocolID   = "/ndag/sync/1.0.0"
	maxMessageSize = 4 * 1024 * 1024 // 4MB
)

// P2PNetwork implements the production-ready P2P networking layer
type P2PNetwork struct {
	config      *config.Config
	host        host.Host
	pubsub      *pubsub.PubSub
	eventTopic  *pubsub.Topic
	txTopic     *pubsub.Topic
	eventSub    *pubsub.Subscription
	txSub       *pubsub.Subscription
	eventChan   chan *pb.Event
	txChan      chan *pb.Transaction
	validators  map[string]struct{}
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewP2PNetwork creates a new production P2P network instance
func NewP2PNetwork(ctx context.Context, cfg *config.Config) (*P2PNetwork, error) {
	if !cfg.P2P.Enabled {
		return nil, errors.New("P2P network not enabled in configuration")
	}

	n := &P2PNetwork{
		config:     cfg,
		eventChan:  make(chan *pb.Event, 1000),
		txChan:     make(chan *pb.Transaction, 5000),
		validators: make(map[string]struct{}),
	}

	return n, nil
}

// Start initializes and starts the P2P network
func (n *P2PNetwork) Start(ctx context.Context) error {
	n.ctx, n.cancel = context.WithCancel(ctx)

	// Initialize libp2p host
	if err := n.initHost(); err != nil {
		return errors.Wrap(err, "failed to initialize libp2p host")
	}

	// Initialize pubsub
	if err := n.initPubSub(); err != nil {
		return errors.Wrap(err, "failed to initialize pubsub")
	}

	// Connect to bootstrap peers
	if err := n.connectToBootstraps(); err != nil {
		log.Warn().Err(err).Msg("Failed to connect to some bootstrap peers")
	}

	// Start background goroutines
	n.startMessageHandlers()
	n.startDiscovery()

	log.Info().
		Str("peer_id", n.host.ID().String()).
		Str("listen_addr", n.config.P2P.ListenAddress).
		Msg("P2P network started")

	return nil
}

// Stop gracefully shuts down the P2P network
func (n *P2PNetwork) Stop() error {
	if n.cancel != nil {
		n.cancel()
	}

	n.wg.Wait()

	if n.eventSub != nil {
		n.eventSub.Cancel()
	}
	if n.txSub != nil {
		n.txSub.Cancel()
	}

	if n.eventTopic != nil {
		n.eventTopic.Close()
	}
	if n.txTopic != nil {
		n.txTopic.Close()
	}

	if n.host != nil {
		return n.host.Close()
	}

	close(n.eventChan)
	close(n.txChan)

	return nil
}

// initHost initializes the libp2p host
func (n *P2PNetwork) initHost() error {
	// Parse listen address
	listenAddr, err := multiaddr.NewMultiaddr(n.config.P2P.ListenAddress)
	if err != nil {
		return errors.Wrap(err, "invalid listen address")
	}

	// Create libp2p host
	host, err := libp2p.New(
		libp2p.ListenAddrs(listenAddr),
		libp2p.EnableNATService(),
		libp2p.EnableRelay(),
		libp2p.ConnectionManager(libp2p.NewConnManager(
			n.config.P2P.MinPeers,
			n.config.P2P.MaxPeers,
			time.Minute,
		)),
		libp2p.ProtocolVersion("/ndag/1.0.0"),
	)
	if err != nil {
		return errors.Wrap(err, "failed to create libp2p host")
	}

	n.host = host

	// Set up stream handlers
	host.SetStreamHandler(protocol.ID(protocolID), n.handleSyncStream)

	return nil
}

// initPubSub initializes the pubsub system
func (n *P2PNetwork) initPubSub() error {
	// Create pubsub with GossipSub
	ps, err := pubsub.NewGossipSub(n.ctx, n.host)
	if err != nil {
		return errors.Wrap(err, "failed to create gossip sub")
	}

	n.pubsub = ps

	// Join topics
	eventTopic, err := ps.Join(eventTopic)
	if err != nil {
		return errors.Wrap(err, "failed to join event topic")
	}
	n.eventTopic = eventTopic

	txTopic, err := ps.Join(txTopic)
	if err != nil {
		return errors.Wrap(err, "failed to join tx topic")
	}
	n.txTopic = txTopic

	// Subscribe to topics
	eventSub, err := eventTopic.Subscribe(pubsub.WithBufferSize(1024))
	if err != nil {
		return errors.Wrap(err, "failed to subscribe to events")
	}
	n.eventSub = eventSub

	txSub, err := txTopic.Subscribe(pubsub.WithBufferSize(2048))
	if err != nil {
		return errors.Wrap(err, "failed to subscribe to txs")
	}
	n.txSub = txSub

	return nil
}

// connectToBootstraps connects to bootstrap peers
func (n *P2PNetwork) connectToBootstraps() error {
	if len(n.config.P2P.BootstrapPeers) == 0 {
		log.Info().Msg("No bootstrap peers configured")
		return nil
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(n.config.P2P.BootstrapPeers))

	for _, addr := range n.config.P2P.BootstrapPeers {
		wg.Add(1)
		go func(bootstrapAddr string) {
			defer wg.Done()

			addr, err := multiaddr.NewMultiaddr(bootstrapAddr)
			if err != nil {
				errors <- errors.Wrapf(err, "invalid bootstrap address: %s", bootstrapAddr)
				return
			}

			peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
			if err != nil {
				errors <- errors.Wrapf(err, "invalid peer info for: %s", bootstrapAddr)
				return
			}

			ctx, cancel := context.WithTimeout(n.ctx, n.config.P2P.ConnectionTimeout)
			defer cancel()

			if err := n.host.Connect(ctx, *peerInfo); err != nil {
				errors <- errors.Wrapf(err, "failed to connect to bootstrap: %s", bootstrapAddr)
				return
			}

			log.Info().Str("peer", peerInfo.ID.String()).Msg("Connected to bootstrap peer")
		}(addr)
	}

	wg.Wait()
	close(errors)

	// Count errors
	errorCount := 0
	for err := range errors {
		log.Error().Err(err).Msg("Bootstrap connection error")
		errorCount++
	}

	// If all connections failed, return error
	if errorCount == len(n.config.P2P.BootstrapPeers) && len(n.config.P2P.BootstrapPeers) > 0 {
		return errors.New("failed to connect to all bootstrap peers")
	}

	return nil
}

// startMessageHandlers starts background message handlers
func (n *P2PNetwork) startMessageHandlers() {
	// Event handler
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.handleEventMessages()
	}()

	// Transaction handler
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.handleTxMessages()
	}()
}

// handleEventMessages processes incoming event messages
func (n *P2PNetwork) handleEventMessages() {
	for {
		select {
		case <-n.ctx.Done():
			return
		default:
			msg, err := n.eventSub.Next(n.ctx)
			if err != nil {
				log.Error().Err(err).Msg("Error receiving event message")
				continue
			}

			// Validate message size
			if len(msg.Data) > maxMessageSize {
				log.Warn().Int("size", len(msg.Data)).Msg("Event message too large")
				continue
			}

			// Deserialize event
			var event pb.Event
			if err := proto.Unmarshal(msg.Data, &event); err != nil {
				log.Error().Err(err).Msg("Failed to unmarshal event")
				continue
			}

			// Validate event
			if err := n.validateEvent(&event); err != nil {
				log.Error().Err(err).Str("event_id", event.Id).Msg("Invalid event received")
				continue
			}

			// Send to event channel
			select {
			case n.eventChan <- &event:
			default:
				log.Warn().Str("event_id", event.Id).Msg("Event channel full, dropping message")
			}
		}
	}
}

// handleTxMessages processes incoming transaction messages
func (n *P2PNetwork) handleTxMessages() {
	for {
		select {
		case <-n.ctx.Done():
			return
		default:
			msg, err := n.txSub.Next(n.ctx)
			if err != nil {
				log.Error().Err(err).Msg("Error receiving tx message")
				continue
			}

			// Validate message size
			if len(msg.Data) > maxMessageSize {
				log.Warn().Int("size", len(msg.Data)).Msg("Transaction message too large")
				continue
			}

			// Deserialize transaction
			var tx pb.Transaction
			if err := proto.Unmarshal(msg.Data, &tx); err != nil {
				log.Error().Err(err).Msg("Failed to unmarshal transaction")
				continue
			}

			// Validate transaction
			if err := n.validateTransaction(&tx); err != nil {
				log.Error().Err(err).Str("tx_id", tx.Id).Msg("Invalid transaction received")
				continue
			}

			// Send to transaction channel
			select {
			case n.txChan <- &tx:
			default:
				log.Warn().Str("tx_id", tx.Id).Msg("Transaction channel full, dropping message")
			}
		}
	}
}

// handleSyncStream handles incoming sync streams
func (n *P2PNetwork) handleSyncStream(stream network.Stream) {
	defer stream.Close()

	// TODO: Implement sync protocol handlers
	log.Debug().Str("peer", stream.Conn().RemotePeer().String()).Msg("Sync stream opened")
}

// validateEvent validates an event before accepting it
func (n *P2PNetwork) validateEvent(event *pb.Event) error {
	if event == nil {
		return errors.New("event is nil")
	}
	if event.Id == "" {
		return errors.New("event missing ID")
	}
	if event.Creator == "" {
		return errors.New("event missing creator")
	}
	if event.Round == 0 && len(event.Parents) > 0 {
		return errors.New("genesis event cannot have parents")
	}

	// Verify signature
	if !crypto.VerifyEventSignature(event) {
		return errors.New("invalid event signature")
	}

	return nil
}

// validateTransaction validates a transaction before accepting it
func (n *P2PNetwork) validateTransaction(tx *pb.Transaction) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}
	if tx.Id == "" {
		return errors.New("transaction missing ID")
	}
	if tx.ChainId != n.config.Network.ChainID {
		return errors.New("incorrect chain ID")
	}
	if tx.NetworkId != n.config.Network.NetworkID {
		return errors.New("incorrect network ID")
	}
	if tx.Nonce == 0 {
		return errors.New("nonce cannot be zero")
	}
	if tx.Fee < n.config.Consensus.MinGasPrice {
		return errors.New("fee too low")
	}

	// Verify signature
	if !crypto.VerifyTransactionSignature(tx) {
		return errors.New("invalid transaction signature")
	}

	return nil
}

// startDiscovery starts peer discovery
func (n *P2PNetwork) startDiscovery() {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				n.discoverPeers()
			}
		}
	}()
}

// discoverPeers attempts to discover new peers
func (n *P2PNetwork) discoverPeers() {
	// TODO: Implement DHT-based peer discovery
	log.Debug().Msg("Running peer discovery")
}

// BroadcastEvent broadcasts an event to the network
func (n *P2PNetwork) BroadcastEvent(event *pb.Event) error {
	if n.eventTopic == nil {
		return errors.New("event topic not initialized")
	}

	data, err := proto.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "failed to marshal event")
	}

	if err := n.eventTopic.Publish(n.ctx, data); err != nil {
		return errors.Wrap(err, "failed to publish event")
	}

	log.Debug().Str("event_id", event.Id).Msg("Broadcasted event")
	return nil
}

// BroadcastTransaction broadcasts a transaction to the network
func (n *P2PNetwork) BroadcastTransaction(tx *pb.Transaction) error {
	if n.txTopic == nil {
		return errors.New("transaction topic not initialized")
	}

	data, err := proto.Marshal(tx)
	if err != nil {
		return errors.Wrap(err, "failed to marshal transaction")
	}

	if err := n.txTopic.Publish(n.ctx, data); err != nil {
		return errors.Wrap(err, "failed to publish transaction")
	}

	log.Debug().Str("tx_id", tx.Id).Msg("Broadcasted transaction")
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

// GetPeerCount returns the number of connected peers
func (n *P2PNetwork) GetPeerCount() int {
	if n.host == nil {
		return 0
	}
	return len(n.host.Network().Peers())
}

// GetPeers returns information about connected peers
func (n *P2PNetwork) GetPeers() []peer.AddrInfo {
	if n.host == nil {
		return nil
	}

	var peers []peer.AddrInfo
	for _, p := range n.host.Network().Peers() {
		conns := n.host.Network().ConnsToPeer(p)
		if len(conns) > 0 {
			addrs := make([]multiaddr.Multiaddr, len(conns))
			for i, conn := range conns {
				addrs[i] = conn.RemoteMultiaddr()
			}
			peers = append(peers, peer.AddrInfo{
				ID:    p,
				Addrs: addrs,
			})
		}
	}
	return peers
}