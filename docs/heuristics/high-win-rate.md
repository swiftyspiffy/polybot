# High Win Rate Detection

## Overview

Flags traders with an unusually high win rate on "non-obvious" resolved positions. This heuristic focuses on wins where the entry price was below a threshold, filtering out "easy" wins on near-certain outcomes.

## Rationale

- Prediction markets are designed to be difficult to consistently beat
- A wallet with 90%+ win rate across multiple resolved positions is statistically unlikely without edge
- High win rates may indicate access to non-public information or sophisticated analysis
- **Key insight**: Buying at 98¢ and winning is "obvious" - anyone would do that. But consistently winning when buying at 40¢ or lower suggests genuine edge or insider knowledge

## Detection Logic

```
IF wallet.suspiciousWinRate >= HighWinRateThreshold
   AND wallet.suspiciousPositions >= MinResolvedForWinRate
   AND trade.notional >= MinNotional
THEN trigger HighWinRate alert
```

### Algorithm

1. When a trade above the minimum notional threshold is detected
2. Fetch the wallet's closed/resolved positions via the Data API
3. Filter positions to only count those with entry price <= `WinRateMaxEntryPrice`
4. Calculate "suspicious" win rate: `suspiciousWins / (suspiciousWins + suspiciousLosses)`
5. A "suspicious win" is a position where:
   - Entry price (AvgPrice) was below the threshold (default 85¢)
   - Position closed with positive realized PnL
6. If suspicious win rate exceeds threshold with sufficient sample size, flag the trade

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_MIN_NOTIONAL` | `4000` | Minimum trade size (USD) to evaluate |
| `TRADE_HIGH_WIN_RATE` | `0.90` | Win rate threshold (90%) |
| `TRADE_MIN_RESOLVED_FOR_WIN_RATE` | `5` | Minimum resolved positions required |
| `TRADE_WIN_RATE_MAX_ENTRY_PRICE` | `0.70` | Max entry price to count as "suspicious" (70¢) |

## Entry Price Threshold

The `TRADE_WIN_RATE_MAX_ENTRY_PRICE` setting filters out "obvious" wins:

| Entry Price | Counted as Suspicious? | Reasoning |
|-------------|------------------------|-----------|
| 0.30 | Yes | High-risk bet, winning suggests edge |
| 0.50 | Yes | Coin flip odds, consistent wins are notable |
| 0.70 | Yes (at default) | Still requires meaningful edge |
| 0.75 | No | Fairly confident bet, less suspicious |
| 0.90 | No | Near-certain outcome, not suspicious |
| 0.98 | No | "Obvious" bet anyone would make |

**Example**: A trader who:
- Buys 10 positions at 95¢+ and wins 10 → NOT flagged (all "obvious")
- Buys 10 positions at 40¢ and wins 9 → FLAGGED (90% suspicious win rate)
- Buys 5 at 95¢ (wins 5) + 5 at 40¢ (wins 4) → FLAGGED (80% suspicious win rate from the 5 non-obvious bets, if threshold allows)

## Alert Title

- Single: `"High Win Rate Trader"`
- Combined examples:
  - `"Low Activity + High Win Rate"`
  - `"High Win Rate + Extreme Odds Bet"`
  - `"High Win Rate + Rapid Trading"`

## Data Sources

- **Closed Positions**: `GET /closedPositions?user={address}` from Data API
- Returns list of resolved positions with realized PnL and average entry price

## Win Rate Calculation

```go
// Only count positions where entry was below threshold (non-obvious bets)
suspiciousWinRate = float64(suspiciousWins) / float64(suspiciousWins + suspiciousLosses)
```

Where:
- `suspiciousWins` = positions with `realizedPnl > 0` AND `avgPrice <= maxEntryPrice`
- `suspiciousLosses` = positions with `realizedPnl < 0` AND `avgPrice <= maxEntryPrice`
- Positions with entry price above threshold are excluded from suspicious win rate calculation
- Positions with `realizedPnl == 0` are excluded

## Limitations

- Small sample sizes can produce misleading win rates
- Some traders may have high win rates through legitimate skill
- Win rate doesn't account for position sizing (a few large losses can offset many small wins)
- Market makers may have high win rates on spreads but low profit per trade
- The entry price threshold may need tuning based on market conditions

## Code Location

- Detection: `internal/app/trade_monitor.go` - `processTradeEvent()` method
- Win rate calculation: `internal/app/wallet_tracker.go` - `fetchStats()`
- Configuration: `config/config.go` - `TradeMonitorConfig`
