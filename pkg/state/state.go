package state

import (
    "context"
    "fmt"
    "sync"

    "github.com/ndag/ndagcoin/pkg/consensus"
    "github.com/ndag/ndagcoin/pkg/crypto"
    "github.com/ndag/ndagcoin/pkg/pb"
    "github.com/ndag/ndagcoin/pkg/storage"
)

type PublicKey = crypto.PublicKey

// StateMachine manages the blockchain state
type StateMachine struct {
    store      *storage.Store
    consensus  *consensus.ConsensusEngine
    accounts   map[string]*AccountState
    tokens     map[string]*pb.Token
    nfts       map[string]*pb.NFT
    pools      map[string]*pb.LiquidityPool
    mu         sync.RWMutex
}

// AccountState represents an account's state
type AccountState struct {
    Address    string
    Balance    uint64
    Nonce      uint64
    PublicKey  []byte
    Tokens     map[string]uint64 // token_id -> balance
    NFTs       []string          // NFT identifiers
    Delegated  uint64            // Delegated stake
    Validator  string            // Validator address if delegating
}

// NewStateMachine creates a new state machine
func NewStateMachine(store *storage.Store, consensus *consensus.ConsensusEngine) *StateMachine {
    return &StateMachine{
        store:     store,
        consensus: consensus,
        accounts:  make(map[string]*AccountState),
        tokens:    make(map[string]*pb.Token),
        nfts:      make(map[string]*pb.NFT),
        pools:     make(map[string]*pb.LiquidityPool),
    }
}

// ExecuteTransaction executes a transaction and updates state
func (sm *StateMachine) ExecuteTransaction(tx *pb.Transaction) error {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    // Verify transaction
    if err := sm.verifyTransaction(tx); err != nil {
        return fmt.Errorf("transaction verification failed: %w", err)
    }

    // Execute based on payload type
    switch p := tx.Payload.(type) {
    case *pb.Transaction_Transfer:
        return sm.executeTransfer(tx, p.Transfer)
    case *pb.Transaction_CreateToken:
        return sm.executeCreateToken(tx, p.CreateToken)
    case *pb.Transaction_TransferToken:
        return sm.executeTransferToken(tx, p.TransferToken)
    case *pb.Transaction_CreateCollection:
        return sm.executeCreateCollection(tx, p.CreateCollection)
    case *pb.Transaction_MintNft:
        return sm.executeMintNFT(tx, p.MintNft)
    case *pb.Transaction_TransferNft:
        return sm.executeTransferNFT(tx, p.TransferNft)
    case *pb.Transaction_CreateValidator:
        return sm.executeCreateValidator(tx, p.CreateValidator)
    case *pb.Transaction_DelegateStake:
        return sm.executeDelegateStake(tx, p.DelegateStake)
    default:
        return fmt.Errorf("unsupported transaction type: %T", p)
    }
}

// verifyTransaction verifies a transaction
func (sm *StateMachine) verifyTransaction(tx *pb.Transaction) error {
    // Check nonce
    if tx.Nonce == 0 {
        return fmt.Errorf("nonce cannot be zero")
    }

    // Check fee
    if tx.Fee < 1000 { // Minimum fee
        return fmt.Errorf("fee too low")
    }

    // Verify sender exists and has sufficient balance
    sender := string(tx.PublicKey)
    account, exists := sm.accounts[sender]
    if !exists {
        return fmt.Errorf("sender account not found")
    }

    // Check nonce
    if tx.Nonce <= account.Nonce {
        return fmt.Errorf("invalid nonce")
    }

    // Check balance (simplified)
    totalCost := tx.Fee + sm.getTransactionValue(tx)
    if account.Balance < totalCost {
        return fmt.Errorf("insufficient balance")
    }

    return nil
}

// getTransactionValue gets the value being transferred
func (sm *StateMachine) getTransactionValue(tx *pb.Transaction) uint64 {
    switch p := tx.Payload.(type) {
    case *pb.Transaction_Transfer:
        return p.Transfer.Amount
    case *pb.Transaction_TransferToken:
        return p.TransferToken.Amount
    default:
        return 0
    }
}

// executeTransfer executes a native token transfer
func (sm *StateMachine) executeTransfer(tx *pb.Transaction, transfer *pb.TransferPayload) error {
    sender := string(tx.PublicKey)
    
    // Get or create accounts
    senderAccount, exists := sm.accounts[sender]
    if !exists {
        return fmt.Errorf("sender account not found")
    }
    
    recipientAccount := sm.getOrCreateAccount(transfer.To)
    
    // Transfer balance
    if senderAccount.Balance < transfer.Amount+tx.Fee {
        return fmt.Errorf("insufficient balance")
    }
    
    senderAccount.Balance -= transfer.Amount + tx.Fee
    recipientAccount.Balance += transfer.Amount
    
    // Update nonce
    senderAccount.Nonce = tx.Nonce
    
    return nil
}

// executeCreateToken executes token creation
func (sm *StateMachine) executeCreateToken(tx *pb.Transaction, createToken *pb.CreateTokenPayload) error {
    // Check if token already exists
    if _, exists := sm.tokens[createToken.TokenId]; exists {
        return fmt.Errorf("token already exists")
    }
    
    // Create token
    token := &pb.Token{
        TokenId:      createToken.TokenId,
        Name:         createToken.Name,
        Symbol:       createToken.Symbol,
        Decimals:     createToken.Decimals,
        TotalSupply:  createToken.InitialSupply,
        AdminAddress: createToken.AdminAddress,
        Mintable:     createToken.Mintable,
        Burnable:     createToken.Burnable,
    }
    
    sm.tokens[createToken.TokenId] = token
    
    // Assign initial supply to admin
    adminAccount := sm.getOrCreateAccount(createToken.AdminAddress)
    adminAccount.Tokens[createToken.TokenId] = createToken.InitialSupply
    
    return nil
}

// executeTransferToken executes token transfer
func (sm *StateMachine) executeTransferToken(tx *pb.Transaction, transferToken *pb.TransferTokenPayload) error {
    sender := string(tx.PublicKey)
    
    // Get accounts
    senderAccount := sm.getOrCreateAccount(sender)
    recipientAccount := sm.getOrCreateAccount(transferToken.To)
    
    // Check token balance
    senderBalance := senderAccount.Tokens[transferToken.TokenId]
    if senderBalance < transferToken.Amount {
        return fmt.Errorf("insufficient token balance")
    }
    
    // Transfer tokens
    senderAccount.Tokens[transferToken.TokenId] -= transferToken.Amount
    recipientAccount.Tokens[transferToken.TokenId] += transferToken.Amount
    
    // Pay fee from sender's main balance
    if senderAccount.Balance < tx.Fee {
        return fmt.Errorf("insufficient balance for fee")
    }
    senderAccount.Balance -= tx.Fee
    
    return nil
}

// executeCreateCollection executes NFT collection creation
func (sm *StateMachine) executeCreateCollection(tx *pb.Transaction, collection *pb.CreateCollectionPayload) error {
    collectionID := collection.CollectionId
    
    // Check if collection already exists (simplified)
    for _, nft := range sm.nfts {
        if nft.CollectionId == collectionID {
            return fmt.Errorf("collection already exists")
        }
    }
    
    // Collection is implicitly created when first NFT is minted
    return nil
}

// executeMintNFT executes NFT minting
func (sm *StateMachine) executeMintNFT(tx *pb.Transaction, mintNFT *pb.MintNFTPayload) error {
    nftID := fmt.Sprintf("%s-%s", mintNFT.CollectionId, mintNFT.NftId)
    
    // Check if NFT already exists
    if _, exists := sm.nfts[nftID]; exists {
        return fmt.Errorf("NFT already exists")
    }
    
    // Create NFT
    nft := &pb.NFT{
        CollectionId: mintNFT.CollectionId,
        NftId:        mintNFT.NftId,
        Owner:        mintNFT.Owner,
        MetadataUri:  mintNFT.MetadataUri,
        Attributes:   mintNFT.Attributes,
        MetadataHash: mintNFT.MetadataHash,
        MintHeight:   1, // Simplified height
    }
    
    sm.nfts[nftID] = nft
    
    // Add to owner's NFTs
    ownerAccount := sm.getOrCreateAccount(mintNFT.Owner)
    ownerAccount.NFTs = append(ownerAccount.NFTs, nftID)
    
    // Pay minting fee
    owner := string(tx.PublicKey)
    ownerAccount = sm.getOrCreateAccount(owner)
    if ownerAccount.Balance < tx.Fee {
        return fmt.Errorf("insufficient balance for minting fee")
    }
    ownerAccount.Balance -= tx.Fee
    
    return nil
}

// executeTransferNFT executes NFT transfer
func (sm *StateMachine) executeTransferNFT(tx *pb.Transaction, transferNFT *pb.TransferNFTPayload) error {
    nftID := fmt.Sprintf("%s-%s", transferNFT.CollectionId, transferNFT.NftId)
    
    // Get NFT
    nft, exists := sm.nfts[nftID]
    if !exists {
        return fmt.Errorf("NFT not found")
    }
    
    // Verify ownership
    sender := string(tx.PublicKey)
    if nft.Owner != sender {
        return fmt.Errorf("sender is not NFT owner")
    }
    
    // Transfer NFT
    senderAccount := sm.getOrCreateAccount(sender)
    recipientAccount := sm.getOrCreateAccount(transferNFT.To)
    
    // Update ownership
    nft.Owner = transferNFT.To
    sm.nfts[nftID] = nft
    
    // Update account NFT lists
    sm.removeNFTFromAccount(senderAccount, nftID)
    recipientAccount.NFTs = append(recipientAccount.NFTs, nftID)
    
    // Pay transfer fee
    if senderAccount.Balance < tx.Fee {
        return fmt.Errorf("insufficient balance for fee")
    }
    senderAccount.Balance -= tx.Fee
    
    return nil
}

// executeCreateValidator executes validator creation
func (sm *StateMachine) executeCreateValidator(tx *pb.Transaction, validator *pb.CreateValidatorPayload) error {
    // Verify minimum stake
    if validator.CommissionRate > 100000000 {
        return fmt.Errorf("commission rate too high")
    }
    
    // Create validator in consensus (simplified)
    validatorKey := crypto.PublicKey(validator.PublicKey)
    stake := uint64(100000000) // Minimum stake
    
    consensusValidator := &consensus.Validator{
        Address: validator.ValidatorAddress,
        PublicKey: validatorKey,
        Stake: stake,
        IsActive: true,
    }
    
    sm.consensus.AddValidator(consensusValidator)
    
    // Pay creation fee
    creator := string(tx.PublicKey)
    creatorAccount := sm.getOrCreateAccount(creator)
    if creatorAccount.Balance < tx.Fee {
        return fmt.Errorf("insufficient balance for validator creation fee")
    }
    creatorAccount.Balance -= tx.Fee
    
    return nil
}

// executeDelegateStake executes stake delegation
func (sm *StateMachine) executeDelegateStake(tx *pb.Transaction, delegateStake *pb.DelegateStakePayload) error {
    delegator := string(tx.PublicKey)
    delegatorAccount := sm.getOrCreateAccount(delegator)
    
    // Check balance for delegation
    if delegatorAccount.Balance < delegateStake.Amount+tx.Fee {
        return fmt.Errorf("insufficient balance for delegation")
    }
    
    // Transfer stake to validator
    delegatorAccount.Balance -= delegateStake.Amount + tx.Fee
    delegatorAccount.Delegated += delegateStake.Amount
    delegatorAccount.Validator = delegateStake.ValidatorAddress
    
    // Update validator stake (simplified)
    if validator, exists := sm.consensus.GetValidator(delegateStake.ValidatorAddress); exists {
        validator.Stake += delegateStake.Amount
    }
    
    return nil
}

// Helper methods

func (sm *StateMachine) getOrCreateAccount(address string) *AccountState {
    account, exists := sm.accounts[address]
    if !exists {
        account = &AccountState{
            Address: address,
            Balance: 0,
            Nonce:   0,
            Tokens:  make(map[string]uint64),
            NFTs:    make([]string, 0),
        }
        sm.accounts[address] = account
    }
    return account
}

func (sm *StateMachine) removeNFTFromAccount(account *AccountState, nftID string) {
    for i, id := range account.NFTs {
        if id == nftID {
            account.NFTs = append(account.NFTs[:i], account.NFTs[i+1:]...)
            break
        }
    }
}

// Start starts the state machine
func (sm *StateMachine) Start(ctx context.Context) {
    // Background state management
    go func() {
        <-ctx.Done()
        sm.Stop()
    }()
}

// Stop stops the state machine
func (sm *StateMachine) Stop() {
    // Cleanup and save state
}
