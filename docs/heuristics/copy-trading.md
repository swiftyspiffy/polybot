# Copy Trading Detection

## Overview

Identifies wallets that appear to be copying trades from "leader" wallets within a short time window. Copy trading patterns may indicate organized groups or bots following successful traders.

## Rationale

- Successful traders are often copied by others
- Groups may coordinate trades through Telegram/Discord
- Bots may automatically copy trades from high-performing wallets
- Copy trading can amplify market impact and indicates information flow

## Detection Logic

### Identifying Leaders

A wallet becomes a "leader" if:
```
wallet.winRate >= LeaderMinWinRate
   AND wallet.resolvedPositions >= LeaderMinResolved
OR wallet IN ContrarianWinnerCache
```

### Detecting Copy Trades

```
FOR each trade in market within CopyTradeWindow after leader trade:
   IF trade.side == leader.side
      AND trade.outcome == leader.outcome
      AND trade.wallet != leader.wallet
   THEN count as potential copy

IF copyCount >= CopyTradeMinCount
THEN trigger CopyTrader alert
```

### Algorithm

1. Track trades by leaders (high win rate or contrarian winners)
2. For each new trade, check if it matches a recent leader trade
3. Match criteria: same market, same side, same outcome, within time window
4. If enough copies detected, alert on the copying wallets

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `COPY_TRADE_WINDOW` | `10m` | Time window to detect copies |
| `COPY_TRADE_MIN_COUNT` | `3` | Minimum copy trades to trigger |
| `COPY_TRADE_LEADER_MIN_WIN` | `0.70` | Minimum win rate for leader |
| `COPY_TRADE_LEADER_MIN_RESOLVED` | `5` | Minimum resolved positions for leader |

## Alert Title

- Single: `"Copy Trader Detected"`
- Combined examples:
  - `"Copy Trader Following Contrarian Winner"`
  - `"Copy Trader with High Win Rate"`

## Data Structure

```go
type CopyTracker struct {
    leaderTrades map[string][]LeaderTrade  // market -> trades
}

type LeaderTrade struct {
    Leader      string
    Market      string
    Outcome     string
    Side        string
    Timestamp   time.Time
    Copiers     []string  // wallets that copied
}
```

## Example Scenario

Timeline:
```
00:00 - Leader (80% win rate) buys YES on "Will X happen?"
00:02 - Wallet A buys YES on same market
00:05 - Wallet B buys YES on same market
00:08 - Wallet C buys YES on same market
```

After 3 copies within 10 minutes:
- Alert: "Copy Trader Detected" for Wallets A, B, C
- The alert identifies they all copied the leader

## Copy Trading Signals

| Signal | Strength | Interpretation |
|--------|----------|----------------|
| Same side within 1min | Strong | Automated bot |
| Same side within 5min | Medium | Active monitoring |
| Same side within 10min | Weak | Could be coincidence |
| Multiple copiers | Strong | Coordinated group |

## Limitations

- Coincidental trades on popular markets
- News events cause many similar trades
- Herding behavior is natural in markets
- False positives on high-volume markets

## Code Location

- Copy tracking: `internal/app/copy_tracker.go`
- Leader detection: `CopyTracker.IsLeader()`
- Copy matching: `CopyTracker.RecordTrade()` and `CopyTracker.CheckForCopies()`
