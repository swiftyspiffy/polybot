# Pre-Move Positioning Detection

## Overview

Detects wallets that are consistently positioned before major price swings. If a wallet's trades are frequently followed by significant price moves in their favor, it suggests they may have advance information about upcoming events.

## Rationale

- Informed traders position themselves before price-moving news
- Consistent alpha (favorable positioning) across multiple trades is statistically unlikely by chance
- Tracking trades and checking price after delay reveals patterns
- A 70%+ success rate on 10+ trades is a strong signal
- This pattern is difficult to achieve without information edge

## Detection Logic

### Recording Phase (Immediate)

```
FOR each trade where notional >= PreMoveMinNotional:
    Record: wallet, conditionID, outcome, side, price, timestamp
    Add to pendingMoves map for later verification
```

### Verification Phase (After Delay)

```
FOR each pending move older than PreMoveCheckDelay:
    Fetch current price via GetPositions API
    Calculate move_percent = (current_price - trade_price) / trade_price

    IF side == BUY:
        favorable = move_percent > 0
    ELSE (SELL):
        favorable = move_percent < 0

    Update wallet stats:
        - total_trades++
        - if favorable AND abs(move_percent) >= PreMoveMinMoveSize:
            successful_moves++

    Calculate alpha_score = successful_moves / total_trades

    IF total_trades >= PreMoveMinTrades
       AND alpha_score >= PreMoveMinAlpha
       AND NOT alerted_within_cooldown
    THEN trigger PreMovePositioning alert
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PATTERN_TRACKER_GIST_ID` | - | Gist ID for persistence |
| `PRE_MOVE_CHECK_DELAY` | `4h` | How long to wait before checking price |
| `PRE_MOVE_MIN_NOTIONAL` | `5000` | Minimum trade size to track |
| `PRE_MOVE_MIN_MOVE_SIZE` | `0.10` | Minimum price move (10%) for success |
| `PRE_MOVE_MIN_TRADES` | `10` | Minimum trades to calculate alpha |
| `PRE_MOVE_MIN_ALPHA` | `0.70` | Minimum success rate (70%) for alert |
| `PRE_MOVE_CHECK_INTERVAL` | `30m` | How often to verify pending moves |
| `PRE_MOVE_ALERT_COOLDOWN` | `24h` | Minimum time between alerts per wallet |

## Alert Title

- Single: `"Pre-Move Positioning"`
- Combined examples:
  - `"Pre-Move Positioning + High Win Rate"`
  - `"Pre-Move Positioning + Proven Contrarian"`
  - `"Pre-Move Positioning + Massive Trade"`

## Example Scenario

Wallet 0xABC makes these trades over 2 weeks:

| Trade | Market | Side | Trade Price | Price After 4h | Move % | Favorable? |
|-------|--------|------|-------------|----------------|--------|------------|
| 1 | Market A | BUY | 0.40 | 0.52 | +30% | Yes |
| 2 | Market B | SELL | 0.65 | 0.48 | -26% | Yes |
| 3 | Market C | BUY | 0.30 | 0.28 | -7% | No |
| 4 | Market D | BUY | 0.55 | 0.71 | +29% | Yes |
| 5 | Market E | SELL | 0.75 | 0.82 | +9% | No |
| 6 | Market F | BUY | 0.35 | 0.48 | +37% | Yes |
| 7 | Market G | BUY | 0.42 | 0.54 | +29% | Yes |
| 8 | Market H | SELL | 0.58 | 0.45 | -22% | Yes |
| 9 | Market I | BUY | 0.25 | 0.39 | +56% | Yes |
| 10 | Market J | BUY | 0.60 | 0.68 | +13% | Yes |

Analysis:
- Total trades: 10
- Successful moves (>=10%): 8
- Alpha score: 80%
- Average favorable move: +27%
- **Triggers Pre-Move Positioning Alert**

## Data Structures

```go
type PreMoveRecord struct {
    ID          string    // wallet:conditionID:timestamp
    Wallet      string
    ConditionID string
    Side        string    // BUY or SELL
    TradePrice  float64
    TradeTime   time.Time
    // Filled after verification
    PriceAfter  float64
    MovePercent float64
    Favorable   bool
    Verified    bool
}

type PreMoveStats struct {
    Wallet          string
    TotalTrades     int       // Trades tracked
    SuccessfulMoves int       // Favorable moves >= threshold
    AlphaScore      float64   // SuccessfulMoves / TotalTrades
    AvgMoveSize     float64   // Average favorable move size
    LastAlertTime   time.Time // For cooldown
}
```

## Alert Data

```go
// Fields added to TradeAlert
PreMoveTotalTrades     int     // Total tracked trades
PreMoveSuccessfulMoves int     // Successful favorable moves
PreMoveAlphaScore      float64 // Success rate (0-1)
PreMoveAvgMoveSize     float64 // Average move size
HasPreMoveInfo         bool    // True if data present
```

## Why 4-Hour Delay?

The default 4-hour verification delay:
- Allows significant price movement to occur
- Captures short-term informed trading (not just long-term)
- Filters out noise and random fluctuations
- Balances speed of detection vs. accuracy
- Can be tuned via `PRE_MOVE_CHECK_DELAY`

## Favorable Move Logic

For **BUY** trades:
- Favorable = price went UP
- Move % = (current - trade) / trade
- Example: Buy at 0.40, now 0.52 = +30% favorable

For **SELL** trades:
- Favorable = price went DOWN
- Move % = -(current - trade) / trade
- Example: Sell at 0.65, now 0.48 = +26% favorable (they avoided the drop)

## Limitations

- Requires wallet to maintain position for price lookup
- 4-hour delay means detection lags behind initial trades
- Legitimate traders may have high alpha through skill/research
- Market makers might show similar patterns
- Does not distinguish informed trading from lucky streaks
- Small sample sizes (10 trades) can be noisy

## Persistence

State is persisted to GitHub Gist:
- `pendingMoves`: Trades awaiting verification
- `preMoveStats`: Per-wallet alpha statistics

Verified moves older than 7 days are cleaned up automatically.

## Code Location

- Pattern tracking: `internal/app/pattern_tracker.go`
- Recording: `PatternTracker.recordPreMove()`
- Verification: `PatternTracker.checkPendingMoves()`
- Background checker: `PatternTracker.runPreMoveChecker()`
- Alert check: `PatternTracker.checkPreMoveAlert()`
- Integration: `TradeMonitor.processTradeEvent()` (via `ProcessTrade()`)
