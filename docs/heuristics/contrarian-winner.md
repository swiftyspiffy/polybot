# Contrarian Winner Detection

## Overview

Tracks wallets with a proven history of successful contrarian bets and alerts on their future trades. These "proven contrarian winners" have demonstrated the ability to consistently bet against market consensus and win.

## Rationale

- A track record of successful contrarian bets is rare and significant
- Wallets that consistently win on long-shots may have informational edge
- Past success at contrarian betting is predictive of future success
- Following proven winners is a common trading strategy

## Detection Logic

### Identifying Contrarian Winners

```
IF wallet.contrarianWins >= MinWins
   AND wallet.contrarianWinRate >= MinContrarianRate
THEN add wallet to ContrarianCache
```

### Alerting on Their Trades

```
IF wallet IN ContrarianCache
   AND trade.notional >= MinNotional
THEN trigger ContrarianWinner alert
```

### Algorithm

1. **Discovery Phase**: When processing closed positions
   - Identify positions closed at contrarian prices (< threshold or > 1-threshold)
   - Track wins vs losses at contrarian prices per wallet
   - If wallet meets criteria, add to persistent cache

2. **Alert Phase**: When processing new trades
   - Check if wallet is in the contrarian winners cache
   - If so, alert on any significant trade they make

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CONTRARIAN_CACHE_GIST_ID` | - | Gist ID for persistence |
| `CONTRARIAN_CACHE_FILE_NAME` | `contrarian_winners.txt` | Filename in Gist |
| `CONTRARIAN_CACHE_SAVE_INTERVAL` | `5m` | Save frequency |
| `CONTRARIAN_MIN_WINS` | `3` | Minimum contrarian wins required |
| `CONTRARIAN_MIN_RATE` | `0.70` | Minimum % of wins that are contrarian |
| `CONTRARIAN_THRESHOLD` | `0.20` | Price threshold (<20% or >80%) |

## Alert Title

- Single: `"Proven Contrarian Winner"`
- Combined examples:
  - `"Proven Contrarian + Another Contrarian Bet"`
  - `"Proven Contrarian + Massive Trade"`

## Contrarian Price Threshold

A position is considered "contrarian" if:
- Entry price was < 20% (buying unlikely outcome)
- Entry price was > 80% (selling likely outcome)

```go
isContrarian := price < threshold || price > (1 - threshold)
// With threshold = 0.20:
// Contrarian if price < 0.20 OR price > 0.80
```

## Data Structure

```go
type ContrarianCache struct {
    wallets map[string]*ContrarianWallet
}

type ContrarianWallet struct {
    Address         string
    ContrarianWins  int
    TotalWins       int
    LastUpdated     time.Time
}
```

## Persistence

The cache is persisted to a GitHub Gist as a simple text file:
```
0x1234...5678
0xabcd...ef01
0x9876...5432
```

This allows the bot to remember proven winners across restarts.

## Limitations

- Small sample sizes can produce false positives
- Past performance doesn't guarantee future results
- Wallets may be sold or change behavior
- Multiple people may control the same wallet

## Code Location

- Cache management: `internal/app/contrarian_cache.go`
- Winner tracking: `ContrarianCache.RecordWin()` and `ContrarianCache.IsContrarianWinner()`
- Alert integration: `internal/app/trade_monitor.go` - `shouldAlert()` method
