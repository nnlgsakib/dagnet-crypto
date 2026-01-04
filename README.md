# NDAG Coin - Production-Grade DAG Blockchain

A high-performance, production-ready cryptocurrency network implementing DAG-based consensus with native tokens, NFTs, and comprehensive security hardening.

## Overview

NDAG Coin implements a **Directed Acyclic Graph (DAG)**-based blockchain with Hashgraph-inspired consensus, achieving deterministic finality in ~30 seconds under normal network conditions. The network features native L1 assets (fungible tokens and NFTs), staking, slashing, and VRF-based reward mechanisms.

### Key Features

- **DAG Consensus**: Hashgraph-inspired gossip-about-gossip with virtual voting
- **Native Assets**: Built-in fungible tokens and NFTs (not smart contracts)
- **Validator Staking**: Proof-of-Stake with delegation and slashing
- **Reward Mechanism**: VRF-based eligibility with Proof-of-Participation rewards
- **Security**: Comprehensive threat model with DoS protection, replay protection, and Sybil defenses
- **Performance**: ~30 second finality, high throughput, efficient gossip protocol

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   ndagctl CLI   │    │    ndagd Node   │    │   Network P2P   │
│                 │    │                 │    │                 │
│  Wallet & Admin │◄──►│  Consensus DAG  │◄──►│  libp2p Gossip  │
│                 │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                                │
                                ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  Native Assets  │    │  State Machine  │    │   Storage       │
│                 │    │                 │    │                 │
│  Tokens & NFTs  │◄──►│  Account State  │◄──►│  LevelDB        │
│                 │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## Quick Start

### Prerequisites

- Go 1.21+
- Protocol Buffers compiler (`protoc`)
- Make
- Docker (optional, for containerized deployment)

### Build from Source

```bash
# Clone the repository
git clone https://github.com/ndag/ndagcoin.git
cd ndagcoin

# Install dependencies and build
make setup

# Run tests
make test

# Create genesis file
make genesis

# Start development network
make run-devnet
```

### Using Pre-built Binaries

```bash
# Download latest release
wget https://github.com/ndag/ndagcoin/releases/latest/download/ndagd-linux-amd64
wget https://github.com/ndag/ndagcoin/releases/latest/download/ndagctl-linux-amd64

# Make executable
chmod +x ndagd-linux-amd64 ndagctl-linux-amd64

# Move to PATH
sudo mv ndagd-linux-amd64 /usr/local/bin/ndagd
sudo mv ndagctl-linux-amd64 /usr/local/bin/ndagctl
```

## Usage

### ndagd - Node Daemon

Start the node daemon with default configuration:

```bash
ndagd start --config configs/devnet.toml
```

Start with custom configuration:

```bash
ndagd start \
  --config configs/mainnet.toml \
  --data-dir /var/lib/ndag \
  --p2p-port 9000 \
  --api-port 9090
```

### ndagctl - CLI Tool

Generate a new wallet:

```bash
ndagctl wallet generate --name mywallet --password "mysecurepassword"
```

Send a transaction:

```bash
ndagctl tx transfer \
  --from mywallet \
  --to ndag1abc123... \
  --amount 1000 \
  --password "mysecurepassword"
```

Get account balance:

```bash
ndagctl query balance --address ndag1abc123...
```

Create a new token:

```bash
ndagctl token create \
  --name "My Token" \
  --symbol "MTK" \
  --decimals 6 \
  --initial-supply 1000000 \
  --from mywallet \
  --password "mysecurepassword"
```

Mint an NFT:

```bash
ndagctl nft mint \
  --collection my-collection \
  --nft-id nft-001 \
  --metadata-uri ipfs://QmY7MIMj...
  --from mywallet \
  --password "mysecurepassword"
```

## Protocol Specification

### Consensus Algorithm

NDAG Coin implements a **Hashgraph-inspired consensus algorithm**:

1. **Gossip-about-gossip**: Nodes gossip events containing transactions and gossip history
2. **Virtual voting**: Nodes calculate votes based on gossip history without sending vote messages
3. **Witnesses**: First events in each round from each validator
4. **Famous witnesses**: Determined through iterative voting rounds
5. **Finality**: Events are finalized when they're ancestors of famous witnesses

**Key Properties:**
- **Deterministic finality**: No reorganization after finalization
- **Asynchronous safety**: Protocol is safe under partial synchrony
- **Liveness**: Finality progresses with >2/3 honest stake
- **Fair ordering**: Transactions ordered by logical timestamps

### Transaction Types

| Type | Description | Fee | Gas Limit |
|------|-------------|-----|-----------|
| `Transfer` | Transfer NDAG tokens | 1000 | 50,000 |
| `CreateValidator` | Register validator | 10,000,000 | 5,000,000 |
| `DelegateStake` | Delegate to validator | 5,000 | 200,000 |
| `UndelegateStake` | Undelegate tokens | 5,000 | 200,000 |
| `CreateToken` | Create fungible token | 100,000 | 1,000,000 |
| `MintToken` | Mint tokens | 2,000 | 100,000 |
| `BurnToken` | Burn tokens | 1,000 | 80,000 |
| `TransferToken` | Transfer tokens | 1,500 | 100,000 |
| `CreateCollection` | Create NFT collection | 500,000 | 2,000,000 |
| `MintNFT` | Mint NFT | 10,000 | 500,000 |
| `TransferNFT` | Transfer NFT | 3,000 | 150,000 |
| `ClaimRewards` | Claim validator rewards | 2,000 | 100,000 |

### Staking & Rewards

**Validator Requirements:**
- Minimum self-stake: 100,000 NDAG
- Commission rate: 0-100% (configurable)
- Uptime requirement: >90% to avoid slashing

**Reward Formula:**

```
Reward = Base_Reward × (0.4 × Event_Rate + 0.3 × Stake_Weight + 0.3 × Participation_Score)
```

**Slashing Conditions:**
- **Equivocation**: 33% stake slashed (double-signing)
- **Invalid Events**: 10% stake slashed (fraudulent transactions)
- **Double-Spend**: 25% stake slashed (attempted double-spend detection)
- **Downtime**: Gradual stake decay for <90% uptime

### Network Protocol

**GossipSub Topics:**
- `/ndag/events/v1` - Event gossip protocol
- `/ndag/txs/v1` - Transaction gossip protocol
- `/ndag/consensus/v1` - Consensus messages

**Req/Resp Protocols:**
- `SyncDAGRange` - Sync events by round range
- `GetEvent` - Fetch specific event
- `GetTx` - Fetch specific transaction
- `GetStateSnapshot` - Get state snapshot

**Peer Management:**
- Bootstrap nodes for initial connections
- DHT for peer discovery
- Stake-weighted peer scoring
- Automatic connection management

## Configuration

Configuration files are in TOML format:

```toml
# Network Configuration
network_id = "ndag-mainnet"
chain_id = "ndag-mainnet-v1"
data_dir = "/var/lib/ndag"

# P2P Configuration
p2p_port = 9000
bootstrap_peers = [
    "/dns/bootstrap.ndag.network/tcp/9000/p2p/Qm...",
]
max_peers = 100

# Consensus Configuration
round_duration = "1s"
epoch_duration = "604800s" # 1 week
min_gas_price = 1000
max_gas_per_round = 10000000

# API Configuration
api_enabled = true
api_port = 9090
api_cors_origins = ["*"]

# Logging
log_level = "info"
log_format = "json"

# Validator Configuration (if running validator)
[validator]
enabled = true
address = "ndag1val123..."
commission_rate = 10000000 # 10%
min_self_stake = 100000000000 # 100,000 NDAG

# Storage Configuration
[storage]
backend = "leveldb"
pruning = "default"
pruning_keep_recent = 362880 # 1 day of events

# Genesis Configuration
[genesis]
genesis_file = "configs/genesis.pb"
initial_supply = 100000000000000000 # 100M NDAG
```

## API Reference

The node exposes gRPC and REST APIs:

**gRPC Endpoint**: `localhost:9090`

**REST Endpoint**: `localhost:9091`

Example using `grpcurl`:

```bash
# Get node status
grpcurl -plaintext localhost:9090 ndagcoin.pb.NodeService.GetNodeStatus

# Submit transaction
grpcurl -plaintext -d @ localhost:9090 ndagcoin.pb.NodeService.SubmitTx <<EOM
{
  "transaction": {
    "chain_id": "ndag-mainnet",
    "nonce": 1,
    "fee": 1000,
    "payload": {
      "transfer": {
        "to": "ndag1recipient...",
        "amount": "1000000"
      }
    }
  }
}
EOM
```

## Testing & Development

### Run Tests

```bash
# All tests
make test

# Unit tests only
make unit-test

# Integration tests
make integration-test

# Fuzz tests
make fuzz

# With coverage
make test && go tool cover -html=coverage.out
```

### Local Development Network

```bash
# Create genesis for devnet
./build/ndagctl genesis create --network devnet --output configs/genesis-devnet.pb

# Start single node
./build/ndagd start --config configs/devnet.toml

# Or use the devnet script for multiple nodes
./scripts/devnet.sh
```

### Fuzz Testing

Run fuzz tests for critical components:

```bash
# Protocol buffer decoding
make fuzz FUZZ=TestFuzzEventSerialization

# Event validation
make fuzz FUZZ=TestFuzzEventValidation

# Consensus rules
make fuzz FUZZ=TestFuzzConsensusRules
```

## Docker & Deployment

### Build Docker Images

```bash
make docker
```

### Run with Docker Compose

```bash
docker-compose -f docker/devnet.yml up -d
```

### Production Deployment

See [docs/deployment.md](docs/deployment.md) for production deployment guidelines.

## Security

### Threat Model

See [docs/threat-model.md](docs/threat-model.md) for comprehensive threat model analysis.

### Security Features

- ✅ Replay protection (nonces + chain-id)
- ✅ DoS resistance (rate limiting, gossip scoring)
- ✅ Sybil resistance (stake-weighted validation)
- ✅ Eclipse attack resistance (peer diversity, DHT)
- ✅ Equivocation detection and slashing
- ✅ Double-spend prevention (deterministic ordering)
- ✅ Network partition tolerance (>2/3 stake assumption)
- ✅ Cryptographically secure randomness (VRF)

### Security Checklist

See [docs/security-checklist.md](docs/security-checklist.md) for complete security verification checklist.

## Performance

### Benchmarks

**Target Performance (Mainnet):**
- Transactions per second: 5,000+
- Finality time: ~30 seconds
- Event propagation: <1 second
- State sync: 100+ events/second

**Test Results (Devnet):**
```bash
# Run benchmarks
make bench

# Results:
# - Event creation: 10,000 ops/sec
# - Transaction processing: 3,500 tx/sec
# - State transitions: 5,000 ops/sec
# - Network throughput: 50 Mbps sustained
```

### Load Testing

```bash
# Run load tests
./scripts/load-test.sh --duration 300 --rate 1000
```

## Governance

NDAG Coin features on-chain governance for protocol upgrades:

- **Proposal Creation**: Submit governance proposals
- **Voting**: Stake-weighted voting
- **Execution**: Automatic upgrade execution

See [docs/governance.md](docs/governance.md) for details.

## Economics

### Token Economics

- **Initial Supply**: 100,000,000 NDAG
- **Inflation**: 5% annually (reward emissions)
- **Transaction Fees**: Burned (deflationary pressure)
- **Staking Rewards**: Varies by participation

### Fee Market

Dynamic fee market based on network congestion:

```python
base_fee = previous_base_fee * gas_used / target_gas
min_fee = 1000 ndagosas
max_fee = 1000000 ndagosas
```

## Roadmap

### Q1 2024 ✅
- [x] Core DAG consensus implementation
- [x] Native token support
- [x] NFT support
- [x] Validator staking
- [x] Basic P2P networking

### Q2 2024 🚧
- [ ] Sharding support
- [ ] Cross-shard transactions
- [ ] Advanced fee market (EIP-1559 style)
- [x] Governance module
- [ ] Mobile wallet SDK

### Q3 2024 📋
- [ ] Zero-knowledge proofs support
- [ ] Privacy features
- [ ] Enterprise integrations
- [ ] Cross-chain bridges
- [ ] Developer tooling

### Q4 2024 📋
- [ ] Full mainnet launch
- [ ] EVM compatibility layer
- [ ] DeFi protocol suite
- [ ] DAO framework

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

NDAG Coin is released under the MIT License. See [LICENSE](LICENSE) for details.

## References

- [Hashgraph Whitepaper](https://www.swirlds.com/swirlds-consensus-algorithm-summary/)
- [Libp2p Specification](https://libp2p.io/)
- [Protocol Buffers](https://developers.google.com/protocol-buffers)
- [BLAKE3 Hash Function](https://blake3.io/)

## Community

- **Discord**: https://discord.gg/ndagcoin
- **Telegram**: https://t.me/ndagcoin
- **Twitter**: https://twitter.com/ndagcoin
- **Reddit**: https://reddit.com/r/ndagcoin
- **Forum**: https://forum.ndag.network

## Support

For support and questions:

- GitHub Issues: https://github.com/ndag/ndagcoin/issues
- Documentation: https://docs.ndag.network
- Email: support@ndag.network

---

**Disclaimer**: This software is provided as-is. Please review the security considerations and conduct thorough testing before using in production environments.