package state

import (
	"fmt"

	"github.com/ndag/ndagcoin/pkg/consensus"
	"github.com/ndag/ndagcoin/pkg/crypto"
	"github.com/ndag/ndagcoin/pkg/pb"
	"github.com/ndag/ndagcoin/pkg/storage"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

// TransactionExecutor handles transaction execution with proper state transitions
type TransactionExecutor struct {
	state     *StateMachine
	store     *storage.Store
	consensus *consensus.ConsensusEngine
}

// NewTransactionExecutor creates a new transaction executor
func NewTransactionExecutor(state *StateMachine, store *storage.Store, consensus *consensus.ConsensusEngine) *TransactionExecutor {
	return &TransactionExecutor{
		state:     state,
		store:     store,
		consensus: consensus,
	}
}

// Execute executes a single transaction against the current state
func (e *TransactionExecutor) Execute(tx *pb.Transaction) (*ExecutionResult, error) {
	if tx == nil {
		return nil, errors.New("transaction cannot be nil")
	}

	// Verify transaction signature first
	if !crypto.VerifyTransactionSignature(tx) {
		return NewExecutionResult(tx.Id, false, "invalid signature", 0), errors.New("invalid transaction signature")
	}

	// Get sender account
	senderAddr := crypto.GetAddressFromPublicKey(tx.PublicKey)
	senderAccount, err := e.getAccount(senderAddr)
	if err != nil {
		return NewExecutionResult(tx.Id, false, fmt.Sprintf("sender account error: %v", err), 0), err
	}

	// Check nonce
	if tx.Nonce != senderAccount.Nonce+1 {
		return NewExecutionResult(tx.Id, false, fmt.Sprintf("invalid nonce: expected %d, got %d", senderAccount.Nonce+1, tx.Nonce), 0), errors.New("invalid nonce")
	}

	// Check fees
	if !e.hasSufficientBalance(senderAccount, tx.Fee) {
		return NewExecutionResult(tx.Id, false, "insufficient balance for fees", 0), errors.New("insufficient balance for fees")
	}

	// Create execution context
	ctx := &ExecutionContext{
		Sender:     senderAccount,
		Tx:         tx,
		GasUsed:    0,
		Refund:     0,
		Events:     make([]*pb.Event, 0),
	}

	// Execute based on transaction type
	result, err := e.executeTransaction(ctx)
	if err != nil {
		return NewExecutionResult(tx.Id, false, fmt.Sprintf("execution failed: %v", err), ctx.GasUsed), err
	}

	// Update account nonce
	ctx.Sender.Nonce = tx.Nonce

	// Apply gas and refund
	ctx.Sender.Balance -= tx.Fee
	ctx.Sender.Balance += ctx.Refund

	// Save updated account
	if err := e.saveAccount(ctx.Sender); err != nil {
		return NewExecutionResult(tx.Id, false, fmt.Sprintf("failed to save sender account: %v", err), ctx.GasUsed), err
	}

	return result, nil
}

// executeTransaction executes the transaction based on type
func (e *TransactionExecutor) executeTransaction(ctx *ExecutionContext) (*ExecutionResult, error) {
	switch p := ctx.Tx.Payload.(type) {
	case *pb.Transaction_Transfer:
		return e.executeTransfer(ctx, p.Transfer)
	case *pb.Transaction_CreateValidator:
		return e.executeCreateValidator(ctx, p.CreateValidator)
	case *pb.Transaction_DelegateStake:
		return e.executeDelegateStake(ctx, p.DelegateStake)
	case *pb.Transaction_CreateToken:
		return e.executeCreateToken(ctx, p.CreateToken)
	case *pb.Transaction_MintToken:
		return e.executeMintToken(ctx, p.MintToken)
	case *pb.Transaction_BurnToken:
		return e.executeBurnToken(ctx, p.BurnToken)
	case *pb.Transaction_TransferToken:
		return e.executeTransferToken(ctx, p.TransferToken)
	case *pb.Transaction_CreateCollection:
		return e.executeCreateCollection(ctx, p.CreateCollection)
	case *pb.Transaction_MintNft:
		return e.executeMintNFT(ctx, p.MintNft)
	case *pb.Transaction_TransferNft:
		return e.executeTransferNFT(ctx, p.TransferNft)
	default:
		return nil, fmt.Errorf("unsupported transaction type: %T", p)
	}
}

// executeTransfer executes a native token transfer
func (e *TransactionExecutor) executeTransfer(ctx *ExecutionContext, transfer *pb.TransferPayload) (*ExecutionResult, error) {
	// Check amount
	if transfer.Amount == 0 {
		return nil, errors.New("transfer amount cannot be zero")
	}

	// Check sender has sufficient balance
	if !e.hasSufficientBalance(ctx.Sender, transfer.Amount) {
		return nil, errors.New("insufficient balance for transfer")
	}

	// Get recipient account
	recipient, err := e.getOrCreateAccount(transfer.To)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipient account")
	}

	// Perform transfer
	ctx.Sender.Balance -= transfer.Amount
	recipient.Balance += transfer.Amount

	// Save recipient account
	if err := e.saveAccount(recipient); err != nil {
		return nil, errors.Wrap(err, "failed to save recipient account")
	}

	// Emit transfer event
	event := &pb.Event{
		Id:        generateEventID(),
		Creator:   "system",
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}

	return NewExecutionResult(ctx.Tx.Id, true, "transfer successful", 50000, event), nil
}

// executeCreateValidator registers a new validator
func (e *TransactionExecutor) executeCreateValidator(ctx *ExecutionContext, create *pb.CreateValidatorPayload) (*ExecutionResult, error) {
	// Validate commission rates
	if create.CommissionRate > create.MaxCommissionRate {
		return nil, errors.New("commission rate exceeds maximum")
	}

	// Check minimum self-stake (100 NDAG)
	minStake := uint64(10000000000)
	if !e.hasSufficientBalance(ctx.Sender, minStake) {
		return nil, errors.New("insufficient balance for validator minimum stake")
	}

	// Create validator
	validator := &pb.Validator{
		Address:              create.ValidatorAddress,
		PublicKey:            create.PublicKey,
		Stake:                minStake,
		CommissionRate:       create.CommissionRate,
		MaxCommissionRate:    create.MaxCommissionRate,
		CommissionChangeRate: create.CommissionChangeRate,
		Moniker:              create.Moniker,
		Identity:             create.Identity,
		Website:              create.Website,
		Details:              create.Details,
		Jailed:               false,
		VotingPower:          minStake,
	}

	// Deduct self-stake from sender
	ctx.Sender.Balance -= minStake
	ctx.Sender.Delegated = minStake
	ctx.Sender.Validator = create.ValidatorAddress

	// Store validator
	validatorData, err := proto.Marshal(validator)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal validator")
	}

	if err := e.store.ValidatorStore.PutValidator(create.ValidatorAddress, validatorData); err != nil {
		return nil, errors.Wrap(err, "failed to store validator")
	}

	// Add to consensus
	consensusValidator := &consensus.Validator{
		Address:   create.ValidatorAddress,
		PublicKey: crypto.PublicKey(create.PublicKey),
		Stake:     minStake,
		IsActive:  true,
	}
	e.consensus.AddValidator(consensusValidator)

	return NewExecutionResult(ctx.Tx.Id, true, "validator created successfully", 5000000, &pb.Event{
		Id:        generateEventID(),
		Creator:   create.ValidatorAddress,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeDelegateStake handles stake delegation
func (e *TransactionExecutor) executeDelegateStake(ctx *ExecutionContext, delegate *pb.DelegateStakePayload) (*ExecutionResult, error) {
	// Check amount
	if delegate.Amount == 0 {
		return nil, errors.New("delegate amount cannot be zero")
	}

	// Check sender has sufficient balance
	if !e.hasSufficientBalance(ctx.Sender, delegate.Amount) {
		return nil, errors.New("insufficient balance for delegation")
	}

	// Get validator
	validator, err := e.getValidator(delegate.ValidatorAddress)
	if err != nil {
		return nil, errors.Wrap(err, "validator not found")
	}

	// Check if sender is already delegating to different validator
	if ctx.Sender.Validator != "" && ctx.Sender.Validator != delegate.ValidatorAddress {
		return nil, errors.New("already delegating to another validator")
	}

	// Delegate stake
	ctx.Sender.Delegated += delegate.Amount
	ctx.Sender.Validator = delegate.ValidatorAddress
	validator.Stake += delegate.Amount

	// Save validator
	validatorData, err := proto.Marshal(validator)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal validator")
	}

	if err := e.store.ValidatorStore.PutValidator(delegate.ValidatorAddress, validatorData); err != nil {
		return nil, errors.Wrap(err, "failed to save validator")
	}

	// Update consensus validator
	if consensusValidator, exists := e.consensus.GetValidator(delegate.ValidatorAddress); exists {
		consensusValidator.Stake = validator.Stake
	}

	return NewExecutionResult(ctx.Tx.Id, true, "delegation successful", 200000, &pb.Event{
		Id:        generateEventID(),
		Creator:   ctx.Sender.Address,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeCreateToken creates a new fungible token
func (e *TransactionExecutor) executeCreateToken(ctx *ExecutionContext, create *pb.CreateTokenPayload) (*ExecutionResult, error) {
	// Check if token already exists
	if _, err := e.store.GetToken(create.TokenId); err == nil {
		return nil, errors.New("token already exists")
	}

	// Create token
	token := &pb.Token{
		TokenId:           create.TokenId,
		Name:              create.Name,
		Symbol:            create.Symbol,
		Decimals:          create.Decimals,
		TotalSupply:       create.InitialSupply,
		AdminAddress:      create.AdminAddress,
		MetadataUri:       create.MetadataUri,
		Mintable:          create.Mintable,
		Burnable:          create.Burnable,
		Pausable:          create.Pausable,
		Blacklistable:     create.Blacklistable,
		Paused:            false,
		BlacklistedAddresses: []string{},
	}

	// Store token
	tokenData, err := proto.Marshal(token)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token")
	}

	if err := e.store.PutToken(create.TokenId, tokenData); err != nil {
		return nil, errors.Wrap(err, "failed to store token")
	}

	// Mint initial supply to admin
	adminAccount, err := e.getOrCreateAccount(create.AdminAddress)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get admin account")
	}

	adminAccount.Tokens[create.TokenId] = create.InitialSupply

	if err := e.saveAccount(adminAccount); err != nil {
		return nil, errors.Wrap(err, "failed to save admin account")
	}

	return NewExecutionResult(ctx.Tx.Id, true, "token created successfully", 1000000, &pb.Event{
		Id:        generateEventID(),
		Creator:   create.AdminAddress,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeMintToken mints new tokens
func (e *TransactionExecutor) executeMintToken(ctx *ExecutionContext, mint *pb.MintTokenPayload) (*ExecutionResult, error) {
	// Get token
	tokenData, err := e.store.GetToken(mint.TokenId)
	if err != nil {
		return nil, errors.New("token not found")
	}

	var token pb.Token
	if err := proto.Unmarshal(tokenData, &token); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal token")
	}

	// Check if token is mintable
	if !token.Mintable {
		return nil, errors.New("token is not mintable")
	}

	// Check if sender is admin
	if ctx.Sender.Address != token.AdminAddress {
		return nil, errors.New("only admin can mint tokens")
	}

	// Get recipient account
	recipient, err := e.getOrCreateAccount(mint.To)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipient account")
	}

	// Mint tokens
	token.TotalSupply += mint.Amount
	recipient.Tokens[mint.TokenId] += mint.Amount

	// Save token
	tokenData, err = proto.Marshal(&token)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token")
	}

	if err := e.store.PutToken(mint.TokenId, tokenData); err != nil {
		return nil, errors.Wrap(err, "failed to store token")
	}

	// Save recipient account
	if err := e.saveAccount(recipient); err != nil {
		return nil, errors.Wrap(err, "failed to save recipient account")
	}

	return NewExecutionResult(ctx.Tx.Id, true, "tokens minted successfully", 100000, &pb.Event{
		Id:        generateEventID(),
		Creator:   ctx.Sender.Address,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeTransferToken transfers tokens
func (e *TransactionExecutor) executeTransferToken(ctx *ExecutionContext, transfer *pb.TransferTokenPayload) (*ExecutionResult, error) {
	// Check sender has token balance
	if ctx.Sender.Tokens[transfer.TokenId] < transfer.Amount {
		return nil, errors.New("insufficient token balance")
	}

	// Get recipient account
	recipient, err := e.getOrCreateAccount(transfer.To)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipient account")
	}

	// Transfer tokens
	ctx.Sender.Tokens[transfer.TokenId] -= transfer.Amount
	recipient.Tokens[transfer.TokenId] += transfer.Amount

	// Save accounts
	if err := e.saveAccount(ctx.Sender); err != nil {
		return nil, errors.Wrap(err, "failed to save sender account")
	}

	if err := e.saveAccount(recipient); err != nil {
		return nil, errors.Wrap(err, "failed to save recipient account")
	}

	return NewExecutionResult(ctx.Tx.Id, true, "token transfer successful", 50000, &pb.Event{
		Id:        generateEventID(),
		Creator:   ctx.Sender.Address,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeMintNFT mints a new NFT
func (e *TransactionExecutor) executeMintNFT(ctx *ExecutionContext, mint *pb.MintNFTPayload) (*ExecutionResult, error) {
	// Get collection
	collectionData, err := e.store.NFTStore.GetCollection(mint.CollectionId)
	if err != nil {
		return nil, errors.New("collection not found")
	}

	var collection pb.NFTCollection
	if err := proto.Unmarshal(collectionData, &collection); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal collection")
	}

	// Check if sender is authorized
	if ctx.Sender.Address != collection.AdminAddress {
		return nil, errors.New("only admin can mint NFTs")
	}

	// Create NFT
	nftID := fmt.Sprintf("%s-%s", mint.CollectionId, mint.NftId)
	nft := &pb.NFT{
		CollectionId: mint.CollectionId,
		NftId:        mint.NftId,
		Owner:        mint.Owner,
		MetadataUri:  mint.MetadataUri,
		Attributes:   mint.Attributes,
		MetadataHash: mint.MetadataHash,
		IsBurned:     false,
		MintHeight:   uint64(e.consensus.CurrentRound()),
	}

	// Store NFT
	nftData, err := proto.Marshal(nft)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal NFT")
	}

	if err := e.store.NFTStore.PutNFT(mint.CollectionId, mint.NftId, nftData); err != nil {
		return nil, errors.Wrap(err, "failed to store NFT")
	}

	return NewExecutionResult(ctx.Tx.Id, true, "NFT minted successfully", 500000, &pb.Event{
		Id:        generateEventID(),
		Creator:   ctx.Sender.Address,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// executeCreateCollection creates an NFT collection
func (e *TransactionExecutor) executeCreateCollection(ctx *ExecutionContext, create *pb.CreateCollectionPayload) (*ExecutionResult, error) {
	// Check if collection already exists
	collectionID := fmt.Sprintf("collection-%s-%d", ctx.Sender.Address, time.Now().Unix())
	if _, err := e.store.NFTStore.GetCollection(collectionID); err == nil {
		return nil, errors.New("collection already exists")
	}

	// Create collection
	collection := &pb.NFTCollection{
		CollectionId: collectionID,
		Name:         create.Name,
		Symbol:       create.Symbol,
		AdminAddress: ctx.Sender.Address,
		MetadataUri:  create.MetadataUri,
		IsMutable:    create.IsMutable,
		TotalSupply:  0,
	}

	// Store collection
	collectionData, err := proto.Marshal(collection)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal collection")
	}

	if err := e.store.NFTStore.PutCollection(collectionID, collectionData); err != nil {
		return nil, errors.Wrap(err, "failed to store collection")
	}

	return NewExecutionResult(ctx.Tx.Id, true, "collection created successfully", 2000000, &pb.Event{
		Id:        generateEventID(),
		Creator:   ctx.Sender.Address,
		Timestamp: time.Now().UnixNano(),
		Round:     e.consensus.CurrentRound(),
	}), nil
}

// Helper functions

func (e *TransactionExecutor) getAccount(address string) (*AccountState, error) {
	accountData, err := e.store.GetAccount(address)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, errors.Errorf("account not found: %s", address)
		}
		return nil, errors.Wrap(err, "failed to get account")
	}

	var account pb.Account
	if err := proto.Unmarshal(accountData, &account); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal account")
	}

	return &AccountState{
		Address:    account.Address,
		Balance:    account.Balance,
		Nonce:      account.Nonce,
		PublicKey:  account.PublicKey,
		Tokens:     account.Tokens,
		Delegated:  account.DelegatedStake,
		Validator:  account.Validator,
	}, nil
}

func (e *TransactionExecutor) getOrCreateAccount(address string) (*AccountState, error) {
	account, err := e.getAccount(address)
	if err != nil {
		// Create new account
		account = &AccountState{
			Address:   address,
			Balance:   0,
			Nonce:     0,
			Tokens:    make(map[string]uint64),
			NFTs:      []string{},
			Delegated: 0,
		}
	}
	return account, nil
}

func (e *TransactionExecutor) getValidator(address string) (*pb.Validator, error) {
	validatorData, err := e.store.ValidatorStore.GetValidator(address)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get validator")
	}

	var validator pb.Validator
	if err := proto.Unmarshal(validatorData, &validator); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal validator")
	}

	return &validator, nil
}

func (e *TransactionExecutor) saveAccount(account *AccountState) error {
	pbAccount := &pb.Account{
		Address:       account.Address,
		Balance:       account.Balance,
		Nonce:         account.Nonce,
		PublicKey:     account.PublicKey,
		Tokens:        account.Tokens,
		DelegatedStake: account.Delegated,
		Validator:     account.Validator,
	}

	accountData, err := proto.Marshal(pbAccount)
	if err != nil {
		return errors.Wrap(err, "failed to marshal account")
	}

	if err := e.store.PutAccount(account.Address, accountData); err != nil {
		return errors.Wrap(err, "failed to save account")
	}

	return nil
}

func (e *TransactionExecutor) hasSufficientBalance(account *AccountState, amount uint64) bool {
	return account.Balance >= amount
}

// ExecutionContext holds the execution context
type ExecutionContext struct {
	Sender     *AccountState
	Tx         *pb.Transaction
	GasUsed    uint64
	Refund     uint64
	Events     []*pb.Event
}

// ExecutionResult represents the result of transaction execution
type ExecutionResult struct {
	TxID      string
	Success   bool
	Error     error
	GasUsed   uint64
	Events    []*pb.Event
	BlockHash []byte
	Log       string
}

// NewExecutionResult creates a new execution result
func NewExecutionResult(txID string, success bool, log string, gasUsed uint64, events ...*pb.Event) *ExecutionResult {
	return &ExecutionResult{
		TxID:    txID,
		Success: success,
		GasUsed: gasUsed,
		Events:  events,
		Log:     log,
	}
}