package network

import (
    "encoding/binary"
    "fmt"
    "io"
    "sync"
    "time"

    "github.com/libp2p/go-libp2p-core/network"
    "github.com/libp2p/go-libp2p-core/peer"
    "github.com/ndag/ndagcoin/pkg/pb"
    "github.com/pkg/errors"
    "github.com/rs/zerolog/log"
    "google.golang.org/protobuf/proto"
)

const (
    syncProtocolID = "/ndag/sync/1.0.0"
    dhtProtocolID  = "/ndag/dht/1.0.0"
    
    // Production sync limits
    MaxSyncBatchSize   = 100
    MaxSyncRounds      = 1000
    SyncRequestTimeout = 30 * time.Second
    SyncRetryAttempts  = 3
    SyncRetryDelay     = 5 * time.Second
    
    MaxMessageSize = 4 * 1024 * 1024 // 4MB
)

// SyncManager handles all synchronization operations
type SyncManager struct {
    network *P2PNetwork
    peers   map[peer.ID]*SyncPeer
    mu      sync.RWMutex
    ctx     context.Context
    cancel  context.CancelFunc
}

type SyncPeer struct {
    ID         peer.ID
    LastSync   time.Time
    Round      uint64
    Connected  bool
    Retries    int
}

func NewSyncManager(network *P2PNetwork) *SyncManager {
    ctx, cancel := context.WithCancel(network.ctx)
    
    sm := &SyncManager{
        network: network,
        peers:   make(map[peer.ID]*SyncPeer),
        ctx:     ctx,
        cancel:  cancel,
    }
    
    // Register sync protocol handler
    network.host.SetStreamHandler(syncProtocolID, sm.handleSyncStream)
    
    // Start background sync
    go sm.syncLoop()
    
    return sm
}

func (sm *SyncManager) syncLoop() {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-sm.ctx.Done():
            return
        case <-ticker.C:
            sm.syncWithPeers()
        }
    }
}

func (sm *SyncManager) syncWithPeers() {
    peers := sm.network.GetPeers()
    if len(peers) == 0 {
        log.Debug().Msg("No peers to sync with")
        return
    }
    
    for _, peerInfo := range peers {
        go sm.syncWithPeer(peerInfo.ID)
    }
}

func (sm *SyncManager) syncWithPeer(peerID peer.ID) error {
    // Check if already syncing
    sm.mu.Lock()
    if peer, exists := sm.peers[peerID]; exists && !peer.Connected {
        sm.mu.Unlock()
        return nil
    }
    sm.peers[peerID] = &SyncPeer{ID: peerID, Connected: true}
    sm.mu.Unlock()
    
    defer func() {
        sm.mu.Lock()
        if peer := sm.peers[peerID]; peer != nil {
            peer.Connected = false
        }
        sm.mu.Unlock()
    }()
    
    // Open stream
    ctx, cancel := context.WithTimeout(sm.ctx, SyncRequestTimeout)
    defer cancel()
    
    stream, err := sm.network.host.NewStream(ctx, peerID, syncProtocolID)
    if err != nil {
        return errors.Wrapf(err, "failed to connect to peer %s", peerID)
    }
    defer stream.Close()
    
    // Get our latest round
    latestRound := sm.network.consensus.CurrentRound()
    
    // Send sync request
    req := &pb.SyncDAGRequest{
        StartRound: latestRound + 1,
        EndRound:   latestRound + MaxSyncRounds,
        Limit:      MaxSyncBatchSize,
    }
    
    reqData, err := proto.Marshal(req)
    if err != nil {
        return errors.Wrap(err, "failed to marshal sync request")
    }
    
    if err := writeMessageWithTimeout(stream, reqData, SyncRequestTimeout); err != nil {
        return errors.Wrap(err, "failed to send sync request")
    }
    
    // Read responses
    eventsReceived := 0
    for {
        respData, err := readMessageWithTimeout(stream, SyncRequestTimeout)
        if err != nil {
            if err == io.EOF {
                break
            }
            return errors.Wrap(err, "failed to read sync response")
        }
        
        var resp pb.SyncDAGResponse
        if err := proto.Unmarshal(respData, &resp); err != nil {
            return errors.Wrap(err, "failed to unmarshal sync response")
        }
        
        // Add event to DAG
        if resp.Event != nil {
            if err := sm.network.dag.AddEvent(resp.Event); err != nil {
                log.Warn().Err(err).Str("event_id", resp.Event.Id).Msg("Failed to add synced event")
            } else {
                eventsReceived++
                if err := sm.network.storage.PutEvent(resp.Event.Id, mustMarshal(resp.Event)); err != nil {
                    log.Error().Err(err).Str("event_id", resp.Event.Id).Msg("Failed to store event")
                }
            }
        }
        
        // Add transactions to mempool
        for _, tx := range resp.Transactions {
            if !sm.network.mempool.Has(tx.Id) {
                if added := sm.network.mempool.Add(tx); added {
                    log.Debug().Str("tx_id", tx.Id).Msg("Added synced transaction to mempool")
                }
            }
        }
        
        if !resp.HasMore {
            break
        }
    }
    
    log.Info().
        Str("peer", peerID.String()).
        Int("events", eventsReceived).
        Msg("Sync completed")
    
    return nil
}

func (sm *SyncManager) handleSyncStream(stream network.Stream) {
    defer stream.Close()
    
    peerID := stream.Conn().RemotePeer()
    log.Debug().Str("peer", peerID.String()).Msg("Sync stream opened")
    
    // Read request
    reqData, err := readMessageWithTimeout(stream, SyncRequestTimeout)
    if err != nil {
        log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to read sync request")
        return
    }
    
    var req pb.SyncDAGRequest
    if err := proto.Unmarshal(reqData, &req); err != nil {
        log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to unmarshal sync request")
        return
    }
    
    log.Debug().
        Str("peer", peerID.String()).
        Uint64("start", req.StartRound).
        Uint64("end", req.EndRound).
        Msg("Processing sync request")
    
    // Send events in batches
    sent := 0
    for round := req.StartRound; round <= req.EndRound && sent < int(req.Limit); round++ {
        events := sm.network.dag.GetEventsByRound(round)
        
        for _, event := range events {
            resp := &pb.SyncDAGResponse{
                Event:   event,
                HasMore: round < req.EndRound || sent < len(events)-1,
            }
            
            respData, err := proto.Marshal(resp)
            if err != nil {
                log.Error().Err(err).Str("event_id", event.Id).Msg("Failed to marshal sync response")
                continue
            }
            
            if err := writeMessageWithTimeout(stream, respData, SyncRequestTimeout); err != nil {
                log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to send sync response")
                return
            }
            
            sent++
        }
    }
    
    // Send final response
    resp := &pb.SyncDAGResponse{Event: nil, HasMore: false}
    respData, err := proto.Marshal(resp)
    if err != nil {
        log.Error().Err(err).Msg("Failed to marshal final sync response")
        return
    }
    
    if err := writeMessageWithTimeout(stream, respData, SyncRequestTimeout); err != nil {
        log.Error().Err(err).Str("peer", peerID.String()).Msg("Failed to send final sync response")
    }
    
    log.Debug().
        Str("peer", peerID.String()).
        Int("sent", sent).
        Msg("Sync stream closed")
}

func writeMessageWithTimeout(stream network.Stream, data []byte, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    if err := stream.SetWriteDeadline(deadline); err != nil {
        return errors.Wrap(err, "failed to set write deadline")
    }
    
    // Write length prefix (4 bytes)
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

func readMessageWithTimeout(stream network.Stream, timeout time.Duration) ([]byte, error) {
    deadline := time.Now().Add(timeout)
    if err := stream.SetReadDeadline(deadline); err != nil {
        return nil, errors.Wrap(err, "failed to set read deadline")
    }
    
    // Read length prefix (4 bytes)
    var length uint32
    if err := binary.Read(stream, binary.BigEndian, &length); err != nil {
        if err == io.EOF {
            return nil, io.EOF
        }
        return nil, errors.Wrap(err, "failed to read message length")
    }
    
    // Validate length (prevent DoS)
    if length > uint32(MaxMessageSize) {
        return nil, errors.Errorf("message too large: %d bytes", length)
    }
    
    // Read data
    data := make([]byte, length)
    if _, err := io.ReadFull(stream, data); err != nil {
        return nil, errors.Wrap(err, "failed to read message data")
    }
    
    return data, nil
}

// MustMarshal marshals a protobuf message, panicking on error (production-safe for internal use)
func MustMarshal(msg proto.Message) []byte {
    data, err := proto.Marshal(msg)
    if err != nil {
        panic(fmt.Sprintf("failed to marshal: %v", err))
    }
    return data
}

// SyncWithPeer synchronizes a specific range with a peer
func (sm *SyncManager) SyncWithPeer(ctx context.Context, peerID peer.ID, startRound, endRound uint64) error {
    if peerID == "" {
        return errors.New("peer ID cannot be empty")
    }
    if startRound > endRound {
        return errors.New("invalid round range")
    }

    stream, err := sm.network.host.NewStream(ctx, peerID, syncProtocolID)
    if err != nil {
        return errors.Wrapf(err, "failed to open sync stream to peer %s", peerID)
    }
    defer stream.Close()

    // Send sync request
    req := &pb.SyncDAGRequest{
        StartRound: startRound,
        EndRound:   endRound,
        Limit:      MaxSyncBatchSize,
        IncludeTransactions: true,
    }

    reqData, err := proto.Marshal(req)
    if err != nil {
        return errors.Wrap(err, "failed to marshal sync request")
    }

    if err := writeMessageWithTimeout(stream, reqData, SyncRequestTimeout); err != nil {
        return errors.Wrap(err, "failed to send sync request")
    }

    // Process responses
    eventsReceived := 0
    txsReceived := 0

    for {
        respData, err := readMessageWithTimeout(stream, SyncRequestTimeout)
        if err != nil {
            if err == io.EOF {
                break
            }
            return errors.Wrap(err, "failed to read sync response")
        }

        var resp pb.SyncDAGResponse
        if err := proto.Unmarshal(respData, &resp); err != nil {
            return errors.Wrap(err, "failed to unmarshal sync response")
        }

        // Add event to DAG
        if resp.Event != nil {
            if err := sm.network.dag.AddEvent(resp.Event); err != nil {
                log.Warn().Err(err).Str("event_id", resp.Event.Id).Msg("Failed to add synced event")
            } else {
                eventsReceived++
                // Store in database
                if err := sm.network.storage.PutEvent(resp.Event.Id, MustMarshal(resp.Event)); err != nil {
                    log.Error().Err(err).Str("event_id", resp.Event.Id).Msg("Failed to store event")
                }
            }
        }

        // Add transactions to mempool
        for _, tx := range resp.Transactions {
            if !sm.network.mempool.Has(tx.Id) {
                if added := sm.network.mempool.Add(tx); added {
                    txsReceived++
                }
            }
        }

        if !resp.HasMore {
            break
        }

        // Prevent infinite loops
        if eventsReceived > 10000 || txsReceived > 50000 {
            log.Warn().Int("events", eventsReceived).Int("txs", txsReceived).Msg("Sync limit reached")
            break
        }
    }

    log.Info().
        Str("peer", peerID.String()).
        Int("events", eventsReceived).
        Int("txs", txsReceived).
        Msg("Sync completed")

    return nil
}

// RequestMissingEvents requests specific missing events from network
func (sm *SyncManager) RequestMissingEvents(eventIDs []string) error {
    if len(eventIDs) == 0 {
        return nil
    }

    log.Info().Int("count", len(eventIDs)).Msg("Requesting missing events")

    // Request from random peers to increase success rate
    peers := sm.network.GetPeers()
    if len(peers) == 0 {
        return errors.New("no peers available")
    }

    // Distribute requests across available peers
    for i, eventID := range eventIDs {
        peer := peers[i%len(peers)]
        if err := sm.requestSingleEvent(peer.ID, eventID); err != nil {
            log.Warn().Err(err).Str("event_id", eventID).Str("peer", peer.ID.String()).Msg("Failed to request event")
        }
    }

    return nil
}

// requestSingleEvent requests a single event from a specific peer
func (sm *SyncManager) requestSingleEvent(peerID peer.ID, eventID string) error {
    ctx, cancel := context.WithTimeout(sm.ctx, 10*time.Second)
    defer cancel()

    stream, err := sm.network.host.NewStream(ctx, peerID, "/ndag/event/1.0.0")
    if err != nil {
        return errors.Wrapf(err, "failed to connect to peer %s", peerID)
    }
    defer stream.Close()

    // Send event request
    req := &pb.GetEventRequest{EventId: eventID}
    reqData, err := proto.Marshal(req)
    if err != nil {
        return errors.Wrap(err, "failed to marshal request")
    }

    if err := writeMessageWithTimeout(stream, reqData, 5*time.Second); err != nil {
        return errors.Wrap(err, "failed to send request")
    }

    // Read response
    respData, err := readMessageWithTimeout(stream, 5*time.Second)
    if err != nil {
        return errors.Wrap(err, "failed to read response")
    }

    var event pb.Event
    if err := proto.Unmarshal(respData, &event); err != nil {
        return errors.Wrap(err, "failed to unmarshal event")
    }

    // Add to DAG
    if err := sm.network.dag.AddEvent(&event); err != nil {
        return errors.Wrap(err, "failed to add event")
    }

    return nil
}

// GetSyncStatus returns current sync status
func (sm *SyncManager) GetSyncStatus() (int, bool) {
    // This function provides info for monitoring sync progress
    return len(sm.peers), true
}