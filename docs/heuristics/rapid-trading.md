# Rapid Trading Detection

## Overview

Detects wallets making multiple large trades within a short time window. Rapid accumulation of positions may indicate urgent trading based on time-sensitive information.

## Rationale

- Informed traders may need to establish positions quickly before information becomes public
- Multiple trades in quick succession suggests urgency
- Breaking up large orders into smaller pieces is a common tactic to avoid detection
- The combination of speed and size is a strong signal of informed trading

## Detection Logic

```
IF count(trades in window) >= RapidTradeMinCount
   AND sum(notional in window) >= RapidTradeMinTotal
THEN trigger RapidTrading alert
```

### Algorithm

1. Track recent trades per wallet in a sliding time window
2. When a new trade arrives, add it to the wallet's trade history
3. Remove trades older than the window duration
4. Count trades and sum notional values within the window
5. If both thresholds are met, flag the trade

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_RAPID_WINDOW` | `5m` | Time window to track trades |
| `TRADE_RAPID_MIN_COUNT` | `3` | Minimum trades in window |
| `TRADE_RAPID_MIN_TOTAL` | `5000` | Minimum total notional (USD) in window |

## Alert Title

- Single: `"Rapid Trading Detected"`
- Combined examples:
  - `"Low Activity + Rapid Trading"`
  - `"High Win Rate + Rapid Trading"`
  - `"Extreme Odds + Rapid Trading"`

## Example Scenario

Within a 5-minute window, a wallet executes:
1. Trade 1: Buy 5,000 shares @ $0.60 = $3,000
2. Trade 2: Buy 3,000 shares @ $0.61 = $1,830
3. Trade 3: Buy 2,500 shares @ $0.62 = $1,550

Total: 3 trades, $6,380 notional = Triggers rapid trading alert

## Data Structure

```go
type recentTrade struct {
    Notional  float64
    Timestamp time.Time
}

// Tracked per wallet address
recentTrades map[string][]recentTrade
```

## Limitations

- Market makers legitimately trade rapidly
- Arbitrageurs may execute multiple trades quickly
- News events cause legitimate rapid trading by many participants
- DCA (dollar-cost averaging) strategies may trigger false positives

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Trade tracking: `internal/app/trade_monitor.go` - `recentTrades` map with cleanup
