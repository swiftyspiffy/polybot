# Stealth Accumulation Detection

## Overview

Detects wallets gradually building large positions through multiple smaller trades over time to avoid whale alerts. Sophisticated traders break up large orders to avoid detection while accumulating significant positions.

## Rationale

- Large single trades trigger whale alerts immediately
- Informed traders may split orders to hide their activity
- Accumulating slowly suggests intentional evasion
- Multiple trades spread over time is more sophisticated
- Total accumulated value often exceeds individual whale thresholds

## Detection Logic

### Tracking Accumulation

```
FOR each BUY trade where notional < StealthMaxSingleTrade:
   Add to accumulation record for wallet+market+outcome
   Prune trades older than StealthTimeWindow
```

### Detecting Stealth Pattern

```
IF trades_in_window >= StealthMinTrades
   AND total_size >= StealthMinTotalSize
   AND total_value >= StealthMinTotalValue
   AND spread_minutes >= StealthMinSpreadMinutes
   AND NOT already_alerted_recently
THEN trigger StealthAccumulation alert
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PATTERN_TRACKER_GIST_ID` | - | Gist ID for persistence |
| `STEALTH_TIME_WINDOW` | `6h` | Window to track accumulation |
| `STEALTH_MIN_TRADES` | `3` | Minimum trades to detect pattern |
| `STEALTH_MIN_TOTAL_SIZE` | `5000` | Minimum total shares |
| `STEALTH_MIN_TOTAL_VALUE` | `10000` | Minimum total USD value |
| `STEALTH_MAX_SINGLE_TRADE` | `25000` | Max single trade to be "stealth" |
| `STEALTH_MIN_SPREAD_MINUTES` | `60` | Minimum time spread (1 hour) |

## Alert Title

- Single: `"Stealth Accumulation"`
- Combined examples:
  - `"Stealth Accumulation + Low Activity"`
  - `"Stealth Accumulation + Contrarian Bet"`
  - `"Stealth Accumulation + High Win Rate"`

## Example Scenario

Trades by wallet 0xABC in market "Will X happen?" over 4 hours:

| Time | Side | Size | Price | Value |
|------|------|------|-------|-------|
| 10:00 | BUY YES | 1,500 | $0.35 | $525 |
| 11:30 | BUY YES | 2,000 | $0.36 | $720 |
| 13:00 | BUY YES | 2,500 | $0.37 | $925 |
| 14:00 | BUY YES | 4,000 | $0.38 | $1,520 |

Analysis:
- 4 trades over 4 hours
- Total: 10,000 shares at $3,690 value
- Average price: $0.369
- No single trade > $2,000 (below typical whale threshold)
- Total would have triggered whale alert as single order
- Clear stealth accumulation pattern

## Deduplication

To avoid alert fatigue:
- Track last alert time per wallet+market+outcome
- Don't re-alert within the same time window
- Reset tracking after time window expires

```go
type AccumulationRecord struct {
    Wallet      string
    ConditionID string
    Outcome     string
    Trades      []AccumulationTrade
}

type AccumulationTrade struct {
    Size      float64
    Price     float64
    Value     float64
    Timestamp time.Time
}
```

## Alert Data

```go
type StealthAlert struct {
    TradeCount int       // Number of trades
    TotalSize  float64   // Total shares accumulated
    TotalValue float64   // Total USD value
    AvgPrice   float64   // Average entry price
    SpreadMins int       // Time from first to last trade
}
```

## Why Skip Large Trades

Trades exceeding `StealthMaxSingleTrade` are NOT tracked for stealth:
- Large trades already trigger whale alerts
- Stealth is about avoiding detection
- Including large trades would defeat the purpose
- Focus on accumulation through small orders only

## Time Window Pruning

The system maintains a sliding window:
1. On each new trade, remove trades older than `StealthTimeWindow`
2. Calculate totals from remaining trades
3. Check if pattern thresholds are met
4. Alert if criteria satisfied

## Limitations

- Legitimate dollar-cost averaging looks similar
- Traders may split orders for liquidity reasons
- Market impact avoidance is a valid strategy
- Some traders naturally trade in smaller increments
- Not all stealth accumulation is informed trading

## Code Location

- Pattern tracking: `internal/app/pattern_tracker.go`
- Tracking: `PatternTracker.trackAccumulation()`
- Detection: Part of `PatternTracker.ProcessTrade()`
- Integration: `TradeMonitor.processTradeEvent()`
