package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/libp2p/go-libp2p-kad-dht"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
)

// P2PNetworkProduction extends the network with production features
type P2PNetworkProduction struct {
	*P2PNetwork
	dht       *dht.IpfsDHT
	discovery *routing.RoutingDiscovery
	peers     map[peer.ID]*PeerInfo
	mu        sync.RWMutex
}

type PeerInfo struct {
	ID            peer.ID
	Address       string
	Connection    network.Conn
	Direction     network.Direction
	Latency       time.Duration
	LastSeen      time.Time
	ProtocolVersion string
	ChainID       string
	Round         uint64
	Score         float64
}

const (
	// Production constants
	maxMessageSize     = 8 * 1024 * 1024 // 8MB max message
	maxPeers           = 200
	minPeers           = 10
	peerScoreDecay     = 0.95
	peerScoreThreshold = -50.0
	bootstrapInterval  = 5 * time.Minute
	discoveryInterval  = 1 * time.Minute
)

// NewP2PNetworkProduction creates a production-ready P2P network
func NewP2PNetworkProduction(parent *P2PNetwork) (*P2PNetworkProduction, error) {
	if parent == nil || parent.host == nil {
		return nil, errors.New("parent network not initialized")
	}

	// Initialize DHT for peer discovery
	kadDHT, err := dht.New(parent.ctx, parent.host)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create DHT")
	}

	prod := &P2PNetworkProduction{
		P2PNetwork: parent,
		dht:        kadDHT,
		discovery:  routing.NewRoutingDiscovery(kadDHT),
		peers:      make(map[peer.ID]*PeerInfo),
	}

	// Bootstrap DHT with configured peers
	if err := prod.bootstrapDHT(); err != nil {
		log.Warn().Err(err).Msg("Failed to bootstrap DHT")
	}

	// Start background services
	go prod.discoveryLoop()
	go prod.peerScoringLoop()
	go prod.connectionManagementLoop()

	log.Info().Msg("Production P2P network initialized")
	return prod, nil
}

// bootstrapDHT connects to bootstrap nodes and initializes DHT
func (p *P2PNetworkProduction) bootstrapDHT() error {
	if len(p.config.P2P.BootstrapPeers) == 0 {
		log.Warn().Msg("No bootstrap peers configured")
		return nil
	}

	log.Info().Int("count", len(p.config.P2P.BootstrapPeers)).Msg("Bootstrapping DHT")

	ctx, cancel := context.WithTimeout(p.ctx, 60*time.Second)
	defer cancel()

	// Connect to bootstrap nodes
	var connected int
	for _, addr := range p.config.P2P.BootstrapPeers {
		addrInfo, err := peer.AddrInfoFromString(addr)
		if err != nil {
			log.Error().Err(err).Str("addr", addr).Msg("Invalid bootstrap address")
			continue
		}

		if err := p.host.Connect(ctx, *addrInfo); err != nil {
			log.Warn().Err(err).Str("peer", addrInfo.ID.String()).Msg("Failed to connect to bootstrap")
			continue
		}

		// Advertise ourselves
		if err := p.dht.Bootstrap(ctx); err != nil {
			log.Error().Err(err).Msg("DHT bootstrap error")
		}

		connected++
		log.Info().Str("peer", addrInfo.ID.String()).Msg("Connected to bootstrap")
	}

	log.Info().Int("connected", connected).Msg("Bootstrap completed")
	return nil
}

// discoveryLoop handles periodic peer discovery
func (p *P2PNetworkProduction) discoveryLoop() {
	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.findPeers()
		}
	}
}

// findPeers discovers and connects to new peers via DHT
func (p *P2PNetworkProduction) findPeers() {
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()

	// Start DHT advertisement
	_, err := p.discovery.Advertise(ctx, "ndag-network")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to advertise")
	}

	// Find peers
	peerChan, err := p.discovery.FindPeers(ctx, "ndag-network")
	if err != nil {
		log.Error().Err(err).Msg("Failed to find peers")
		return
	}

	var newPeers int
	for peer := range peerChan {
		if peer.ID == p.host.ID() {
			continue
		}

		if p.getPeerInfo(peer.ID) != nil {
			continue
		}

		if len(p.peers) >= maxPeers {
			break
		}

		go p.connectToPeer(peer)
		newPeers++
	}

	if newPeers > 0 {
		log.Info().Int("new_peers", newPeers).Int("total", len(p.peers)).Msg("Found new peers")
	}
}

// connectToPeer attempts to connect to a discovered peer
func (p *P2PNetworkProduction) connectToPeer(peerInfo peer.AddrInfo) {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()

	if err := p.host.Connect(ctx, peerInfo); err != nil {
		log.Debug().Err(err).Str("peer", peerInfo.ID.String()).Msg("Failed to connect to discovered peer")
		return
	}

	// Initialize peer tracking
	p.mu.Lock()
	p.peers[peerInfo.ID] = &PeerInfo{
		ID:         peerInfo.ID,
		Address:    peerInfo.Addrs[0].String(),
		Connection: p.host.Network().ConnsToPeer(peerInfo.ID)[0],
		Direction:  p.host.Network().ConnsToPeer(peerInfo.ID)[0].Stat().Direction,
		LastSeen:   time.Now(),
		Score:      0.0,
	}
	p.mu.Unlock()

	log.Info().Str("peer", peerInfo.ID.String()).Str("address", peerInfo.Address).Msg("Connected to new peer")
}

// peerScoringLoop manages dynamic peer scoring and cleanup
func (p *P2PNetworkProduction) peerScoringLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.updatePeerScores()
			p.removePoorPeers()
		}
	}
}

// updatePeerScores updates performance-based peer scores
func (p *P2PNetworkProduction) updatePeerScores() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, peer := range p.peers {
		// Decay score
		peer.Score *= peerScoreDecay

		// Boost score for good behavior (successful message relay, low latency)
		if conns := p.host.Network().ConnsToPeer(id); len(conns) > 0 {
			if conns[0].Stat().Direction == network.DirInbound {
				peer.Score += 1.0 // Prefer inbound connections (others connecting to us)
			}
		}

		// Penalize for connection issues
		if time.Since(peer.LastSeen) > 5*time.Minute {
			peer.Score -= 5.0
		}

		log.Debug().Str("peer", id.String()).Float64("score", peer.Score).Msg("Peer score updated")
	}
}

// removePoorPeers disconnects from poorly performing peers
func (p *P2PNetworkProduction) removePoorPeers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, peer := range p.peers {
		if peer.Score < peerScoreThreshold {
			log.Info().Str("peer", id.String()).Float64("score", peer.Score).Msg("Removing poor peer")

			if conns := p.host.Network().ConnsToPeer(id); len(conns) > 0 {
				for _, conn := range conns {
					conn.Close()
				}
			}

			delete(p.peers, id)
		}
	}
}

// connectionManagementLoop maintains healthy peer count
func (p *P2PNetworkProduction) connectionManagementLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.maintainPeerCount()
		}
	}
}

// maintainPeerCount ensures we have sufficient connections
func (p *P2PNetworkProduction) maintainPeerCount() {
	connected := len(p.host.Network().Peers())

	if connected < minPeers {
		log.Info().Int("connected", connected).Int("min", minPeers).Msg("Low peer count, discovering more...")
		p.findPeers()
	} else if connected > maxPeers {
		log.Info().Int("connected", connected).Int("max", maxPeers).Msg("High peer count, trimming...")
		p.trimExcessPeers()
	}
}

// trimExcessPeers disconnects from excess peers
func (p *P2PNetworkProduction) trimExcessPeers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	peers := p.host.Network().Peers()
	target := maxPeers - 10 // Keep buffer

	for i := target; i < len(peers); i++ {
		peer := peers[i]
		if info, exists := p.peers[peer]; exists && info.Score <= 0 {
			conns := p.host.Network().ConnsToPeer(peer)
			for _, conn := range conns {
				conn.Close()
			}
			delete(p.peers, peer)
			log.Debug().Str("peer", peer.String()).Msg("Trimmed excess peer")
		}
	}
}

// getPeerInfo returns peer information
func (p *P2PNetworkProduction) getPeerInfo(id peer.ID) *PeerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.peers[id]
}

// GetPeerStats returns detailed peer statistics
func (p *P2PNetworkProduction) GetPeerStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := make(map[string]interface{})
	var scores []float64
	connected := 0

	for _, peer := range p.peers {
		if peer.Connection != nil {
			connected++
			scores = append(scores, peer.Score)
		}
	}

	stats["connected"] = connected
	stats["tracked"] = len(p.peers)

	if len(scores) > 0 {
		var sum float64
		for _, s := range scores {
			sum += s
		}
		stats["avg_score"] = sum / float64(len(scores))
		stats["min_score"] = minScore(scores)
		stats["max_score"] = maxScore(scores)
	}

	return stats
}

// Production helper functions

func minScore(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	min := scores[0]
	for _, s := range scores[1:] {
		if s < min {
			min = s
		}
	}
	return min
}

func maxScore(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	max := scores[0]
	for _, s := range scores[1:] {
		if s > max {
			max = s
		}
	}
	return max
}