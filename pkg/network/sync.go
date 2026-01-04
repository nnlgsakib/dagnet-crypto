package network

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
)

// SyncProtocol implements the request-response synchronization protocol
const (
	syncProtocolID = "/ndag/sync/1.0.0"
	MaxSyncEvents  = 1000
	SyncTimeout    = 30 * time.Second
)

// SyncManager handles synchronization with peers
type SyncManager struct {
	network *P2PNetwork
}

// NewSyncManager creates a new sync manager
func NewSyncManager(network *P2PNetwork) *SyncManager {
	sm := &SyncManager{
		network: network,
	}

	// Register stream handler
	network.host.SetStreamHandler(syncProtocolID, sm.handleSyncStream)

	return sm
}

// SyncWithPeer synchronizes events with a specific peer
func (sm *SyncManager) SyncWithPeer(ctx context.Context, peerID peer.ID, startRound, endRound uint64) error {
	// Open stream to peer
	stream, err := sm.network.host.NewStream(ctx, peerID, syncProtocolID)
	if err != nil {
		return errors.Wrapf(err, "failed to open sync stream to peer %s", peerID)
	}
	defer stream.Close()

	// Create sync request
	req := &pb.SyncDAGRequest{
		StartRound: startRound,
		EndRound:   endRound,
		Limit:      MaxSyncEvents,
	}

	// Send request
	reqData, err := proto.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal sync request")
	}

	if err := writeMessageWithTimeout(stream, reqData, SyncTimeout); err != nil {
		return errors.Wrap(err, "failed to send sync request")
	}

	// Read responses
	for {
		respData, err := readMessageWithTimeout(stream, SyncTimeout)
		if err != nil {
			if err == io.EOF {
				break // Sync complete
			}
			return errors.Wrap(err, "failed to read sync response")
		}

		var resp pb.SyncDAGResponse
		if err := proto.Unmarshal(respData, &resp); err != nil {
			return errors.Wrap(err, "failed to unmarshal sync response")
		}

		// Process received events
		if resp.Event != nil {
			if err := sm.network.dag.AddEvent(resp.Event); err != nil {
				log.Warn().Err(err).Str("event_id", resp.Event.Id).Msg("Failed to add synced event")
			}
		}

		// Process transactions
		for _, tx := range resp.Transactions {
			if err := sm.network.mempool.Add(tx); err != nil {
				log.Warn().Err(err).Str("tx_id", tx.Id).Msg("Failed to add synced transaction")
			}
		}

		if !resp.HasMore {
			break
		}
	}

	log.Info().
		Str("peer", peerID.String()).
		Uint64("start_round", startRound).
		Uint64("end_round", endRound).
		Msg("Sync completed")

	return nil
}

// handleSyncStream handles incoming sync requests
func (sm *SyncManager) handleSyncStream(stream network.Stream) {
	defer stream.Close()

	peerID := stream.Conn().RemotePeer()
	log.Info().Str("peer", peerID.String()).Msg("Sync stream opened")

	// Read request
	reqData, err := readMessageWithTimeout(stream, SyncTimeout)
	if err != nil {
		log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to read sync request")
		return
	}

	var req pb.SyncDAGRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to unmarshal sync request")
		return
	}

	log.Info().
		Str("peer", peerID.String()).
		Uint64("start_round", req.StartRound).
		Uint64("end_round", req.EndRound).
		Msg("Received sync request")

	// Send events in batches
	sent := 0
	for round := req.StartRound; round <= req.EndRound; round++ {
		events := sm.network.dag.GetEventsByRound(round)

		for _, event := range events {
			// Skip if already sent limit
			if sent >= int(req.Limit) {
				break
			}

			resp := &pb.SyncDAGResponse{
				Event: event,
				HasMore: sent < len(events)-1 || round < req.EndRound,
			}

			respData, err := proto.Marshal(resp)
			if err != nil {
				log.Error().Err(err).Str("event_id", event.Id).Msg("Failed to marshal sync response")
				continue
			}

			if err := writeMessageWithTimeout(stream, respData, SyncTimeout); err != nil {
				log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to send sync response")
				return
			}

			sent++
		}
	}

	// Send end marker
	resp := &pb.SyncDAGResponse{
		Event:   nil,
		HasMore: false,
	}

	respData, err := proto.Marshal(resp)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal final sync response")
		return
	}

	if err := writeMessageWithTimeout(stream, respData, SyncTimeout); err != nil {
		log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to send final sync response")
	}

	log.Info().
		Str("peer", peerID.String()).
		Int("sent", sent).
		Msg("Sync stream closed")
}

// writeMessageWithTimeout writes a length-prefixed message with timeout
func writeMessageWithTimeout(stream network.Stream, data []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if err := stream.SetWriteDeadline(deadline); err != nil {
		return errors.Wrap(err, "failed to set write deadline")
	}

	// Write length (4 bytes)
	length := uint32(len(data))
	if err := binary.Write(stream, binary.BigEndian, length); err != nil {
		return errors.Wrap(err, "failed to write message length")
	}

	// Write data
	if _, err := stream.Write(data); err != nil {
		return errors.Wrap(err, "failed to write message data")
	}

	return nil
}

// readMessageWithTimeout reads a length-prefixed message with timeout
func readMessageWithTimeout(stream network.Stream, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	if err := stream.SetReadDeadline(deadline); err != nil {
		return nil, errors.Wrap(err, "failed to set read deadline")
	}

	// Read length (4 bytes)
	var length uint32
	if err := binary.Read(stream, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, errors.Wrap(err, "failed to read message length")
	}

	// Validate length
	if length > MaxMessageSize {
		return nil, errors.Errorf("message too large: %d bytes", length)
	}

	// Read data
	data := make([]byte, length)
	if _, err := io.ReadFull(stream, data); err != nil {
		return nil, errors.Wrap(err, "failed to read message data")
	}

	return data, nil
}

// SyncDAGRange synchronizes events for a range of rounds
func (sm *SyncManager) SyncDAGRange(ctx context.Context, startRound, endRound uint64) error {
	// Get connected peers
	peers := sm.network.GetPeers()
	if len(peers) == 0 {
		return errors.New("no peers available for sync")
	}

	// Sync with multiple peers in parallel
	var wg sync.WaitGroup
	errors := make(chan error, len(peers))

	for _, peerInfo := range peers {
		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			if err := sm.SyncWithPeer(ctx, p.ID, startRound, endRound); err != nil {
				errors <- err
			}
		}(peerInfo)
	}

	wg.Wait()
	close(errors)

	// Check if any sync succeeded
	successCount := 0
	for err := range errors {
		if err == nil {
			successCount++
		} else {
			log.Warn().Err(err).Msg("Sync with peer failed")
		}
	}

	if successCount == 0 {
		return errors.New("failed to sync with all peers")
	}

	return nil
}

// GetMissingEvents requests missing events from peers
func (sm *SyncManager) GetMissingEvents(ctx context.Context, eventIDs []string) error {
	peers := sm.network.GetPeers()
	if len(peers) == 0 {
		return errors.New("no peers available")
	}

	// Request each missing event from a random peer
	for _, eventID := range eventIDs {
		peer := peers[rand.Intn(len(peers))]
		if err := sm.requestEvent(ctx, peer.ID, eventID); err != nil {
			log.Warn().Err(err).Str("event_id", eventID).Msg("Failed to request missing event")
		}
	}

	return nil
}

// requestEvent requests a single event from a peer
func (sm *SyncManager) requestEvent(ctx context.Context, peerID peer.ID, eventID string) error {
	// This would implement a separate protocol for fetching individual events
	// For now, return not implemented
	return errors.New("requestEvent not yet implemented")
}