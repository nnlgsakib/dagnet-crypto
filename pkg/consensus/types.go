package consensus

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/ndag/ndagcoin/pkg/pb"
)

// DAG represents the directed acyclic graph of events
type DAG struct {
    events map[string]*pb.Event
    parents map[string][]string
    children map[string][]string
    witnesses map[uint64][]string // round -> witness IDs
    famous map[string]bool
    finalized map[string]bool
    mu sync.RWMutex
}

// NewDAG creates a new DAG instance
func NewDAG() *DAG {
    return &DAG{
        events: make(map[string]*pb.Event),
        parents: make(map[string][]string),
        children: make(map[string][]string),
        witnesses: make(map[uint64][]string),
        famous: make(map[string]bool),
        finalized: make(map[string]bool),
    }
}

// AddEvent adds an event to the DAG
func (d *DAG) AddEvent(event *pb.Event) error {
    d.mu.Lock()
    defer d.mu.Unlock()
    
    // Validate event
    if event == nil {
        return ErrInvalidEvent
    }
    if event.Id == "" {
        return ErrMissingEventID
    }
    
    // Check if event already exists
    if _, exists := d.events[event.Id]; exists {
        return ErrEventExists
    }
    
    // Add event to map
    d.events[event.Id] = event
    
    // Build parent-child relationships
    for _, parentID := range event.Parents {
        d.parents[event.Id] = append(d.parents[event.Id], parentID)
        d.children[parentID] = append(d.children[parentID], event.Id)
    }
    
    // Check if this event is a witness
    if d.isWitness(event) {
        d.witnesses[event.Round] = append(d.witnesses[event.Round], event.Id)
        event.Metadata = &pb.EventMetadata{
            IsWitness: true,
        }
    }
    
    return nil
}

// isWitness determines if an event is a witness
func (d *DAG) isWitness(event *pb.Event) bool {
    // An event is a witness if it's the first event in its round from a creator
    // Simplified: Check if any parent has the same round
    for _, parentID := range event.Parents {
        if parent, exists := d.events[parentID]; exists {
            if parent.Round == event.Round {
                return false // Not first in round
            }
        }
    }
    return true
}

// GetEvent retrieves an event by ID
func (d *DAG) GetEvent(eventID string) (*pb.Event, bool) {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    event, exists := d.events[eventID]
    return event, exists
}

// GetEventsByRound gets all events in a round
func (d *DAG) GetEventsByRound(round uint64) []*pb.Event {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    var events []*pb.Event
    for _, event := range d.events {
        if event.Round == round {
            events = append(events, event)
        }
    }
    return events
}

// GetWitnesses gets all witness events for a round
func (d *DAG) GetWitnesses(round uint64) []*pb.Event {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    var witnesses []*pb.Event
    for _, eventID := range d.witnesses[round] {
        if event, exists := d.events[eventID]; exists {
            witnesses = append(witnesses, event)
        }
    }
    return witnesses
}

// GetAncestors gets all ancestors of an event
func (d *DAG) GetAncestors(eventID string) map[string]bool {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    ancestors := make(map[string]bool)
    d.getAncestorsRecursive(eventID, ancestors)
    return ancestors
}

func (d *DAG) getAncestorsRecursive(eventID string, ancestors map[string]bool) {
    event, exists := d.events[eventID]
    if !exists {
        return
    }
    
    for _, parentID := range event.Parents {
        if !ancestors[parentID] {
            ancestors[parentID] = true
            d.getAncestorsRecursive(parentID, ancestors)
        }
    }
}

// GetDescendants gets all descendants of an event
func (d *DAG) GetDescendants(eventID string) map[string]bool {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    descendants := make(map[string]bool)
    d.getDescendantsRecursive(eventID, descendants)
    return descendants
}

func (d *DAG) getDescendantsRecursive(eventID string, descendants map[string]bool) {
    for _, childID := range d.children[eventID] {
        if !descendants[childID] {
            descendants[childID] = true
            d.getDescendantsRecursive(childID, descendants)
        }
    }
}

// StronglySee determines if event A strongly sees event B
// Strong seeing requires multiple paths with >2/3 validator stake
func (d *DAG) StronglySee(eventA, eventB *pb.Event) bool {
    // Simplified: For now, just check if B is an ancestor of A
    ancestors := d.GetAncestors(eventA.Id)
    return ancestors[eventB.Id]
}

// RoundNumber calculates the round number for an event
func (d *DAG) RoundNumber(event *pb.Event) uint64 {
    // Simplified: Round is max parent round + 1 if strongly seen, otherwise same as max parent
    maxParentRound := uint64(0)
    
    for _, parentID := range event.Parents {
        if parent, exists := d.events[parentID]; exists {
            if parent.Round > maxParentRound {
                maxParentRound = parent.Round
            }
        }
    }
    
    // Check if we can create a new round
    if d.canCreateNewRound(event, maxParentRound) {
        return maxParentRound + 1
    }
    
    return maxParentRound
}

// canCreateNewRound determines if an event can create a new round
func (d *DAG) canCreateNewRound(event *pb.Event, parentRound uint64) bool {
    // Need to strongly see >2/3 of witnesses from parent round
    witnesses := d.GetWitnesses(parentRound)
    if len(witnesses) == 0 {
        return false
    }
    
    stronglySeen := 0
    for _, witness := range witnesses {
        if d.StronglySee(event, witness) {
            stronglySeen++
        }
    }
    
    // Simplified: Assume 2/3+1 quorum
    required := (len(witnesses) * 2 / 3) + 1
    return stronglySeen >= required
}

// EventsCount returns the total number of events
func (d *DAG) EventsCount() int {
    d.mu.RLock()
    defer d.mu.RUnlock()
    return len(d.events)
}

// LatestRound returns the highest round in the DAG
func (d *DAG) LatestRound() uint64 {
    d.mu.RLock()
    defer d.mu.RUnlock()
    
    maxRound := uint64(0)
    for _, event := range d.events {
        if event.Round > maxRound {
            maxRound = event.Round
        }
    }
    return maxRound
}

// ConsensusEngine manages the consensus algorithm
type ConsensusEngine struct {
    dag *DAG
    validators map[string]*Validator
    currentRound uint64
    finality chan *FinalityEvent
    mu sync.RWMutex
}

// FinalityEvent represents a finality decision
type FinalityEvent struct {
    EventID string
    Round uint64
    Timestamp time.Time
    Famous bool
}

// NewConsensusEngine creates a new consensus engine
func NewConsensusEngine(dag *DAG) *ConsensusEngine {
    return &ConsensusEngine{
        dag: dag,
        validators: make(map[string]*Validator),
        finality: make(chan *FinalityEvent, 100),
    }
}

// Start starts the consensus engine
func (e *ConsensusEngine) Start(ctx context.Context) {
    // Background consensus logic
    go func() {
        <-ctx.Done()
        e.Stop()
    }()
}

// Stop stops the consensus engine
func (e *ConsensusEngine) Stop() {
    close(e.finality)
}

// Validator represents a network validator
type Validator struct {
    Address string
    PublicKey PublicKey
    Stake uint64
    Commissions uint64
    IsActive bool
}

// AddValidator adds a validator to the consensus engine
func (e *ConsensusEngine) AddValidator(validator *Validator) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.validators[validator.Address] = validator
}

// GetValidator retrieves a validator
func (e *ConsensusEngine) GetValidator(address string) (*Validator, bool) {
    e.mu.RLock()
    defer e.mu.RUnlock()
    v, exists := e.validators[address]
    return v, exists
}

// CurrentRound returns the current round
func (e *ConsensusEngine) CurrentRound() uint64 {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.currentRound
}

// DetermineFame determines if witnesses are famous using virtual voting
func (e *ConsensusEngine) DetermineFame(round uint64) error {
    witnesses := e.dag.GetWitnesses(round)
    if len(witnesses) == 0 {
        return ErrNoWitnesses
    }
    
    // Simplified virtual voting
    for _, witness := range witnesses {
        isFamous := e.voteOnFame(witness, round)
        
        e.dag.mu.Lock()
        e.dag.famous[witness.Id] = isFamous
        if witness.Metadata == nil {
            witness.Metadata = &pb.EventMetadata{}
        }
        witness.Metadata.IsFamous = isFamous
        e.dag.mu.Unlock()
        
        // Send finality event
        e.finality <- &FinalityEvent{
            EventID: witness.Id,
            Round: round,
            Timestamp: time.Now(),
            Famous: isFamous,
        }
    }
    
    return nil
}

// voteOnFame performs virtual voting to determine fame
func (e *ConsensusEngine) voteOnFame(witness *pb.Event, round uint64) bool {
    // Simplified: Famous if majority of next-round witnesses can see it
    nextRound := round + 1
    nextWitnesses := e.dag.GetWitnesses(nextRound)
    
    if len(nextWitnesses) == 0 {
        return false
    }
    
    canSee := 0
    for _, w := range nextWitnesses {
        if e.dag.StronglySee(w, witness) {
            canSee++
        }
    }
    
    // 2/3+1 majority
    required := (len(nextWitnesses) * 2 / 3) + 1
    return canSee >= required
}

// CheckFinality checks if an event is finalized
func (e *ConsensusEngine) CheckFinality(eventID string) bool {
    e.dag.mu.RLock()
    defer e.dag.mu.RUnlock()
    
    // Check if already finalized
    if e.dag.finalized[eventID] {
        return true
    }
    
    // Get event
    event, exists := e.dag.events[eventID]
    if !exists {
        return false
    }
    
    // Check if event is famous
    if !e.dag.famous[eventID] {
        return false
    }
    
    // Check if all ancestors are finalized
    ancestors := e.dag.GetAncestors(eventID)
    for ancestorID := range ancestors {
        if !e.dag.finalized[ancestorID] {
            return false
        }
    }
    
    // Mark as finalized
    e.dag.finalized[eventID] = true
    if event.Metadata == nil {
        event.Metadata = &pb.EventMetadata{}
    }
        event.Metadata.IsFinalized = true
    
    return true
}

// GetFinalityChannel returns the channel for finality events
func (e *ConsensusEngine) GetFinalityChannel() <-chan *FinalityEvent {
    return e.finality
}

// Consensus errors
var (
    ErrInvalidEvent = fmt.Errorf("invalid event")
    ErrMissingEventID = fmt.Errorf("missing event ID")
    ErrEventExists = fmt.Errorf("event already exists")
    ErrNoWitnesses = fmt.Errorf("no witnesses in round")
)