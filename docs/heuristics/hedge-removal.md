# Hedge Removal Detection

## Overview

Alerts when wallets holding hedged positions (both Yes and No shares in the same market) significantly reduce one side. This pattern may indicate the trader learned which outcome will win and is removing the losing side of their hedge.

## Rationale

- Holding both Yes and No is a common hedging strategy to limit risk
- A hedged position profits regardless of outcome (minus spread costs)
- Suddenly removing one side suggests the trader no longer needs that protection
- If they remove the side that ends up losing, it strongly suggests informed trading

## Detection Logic

### Identifying Hedged Positions

```
IF wallet.yesSize >= MinHedgeSize
   AND wallet.noSize >= MinHedgeSize
   AND wallet.yesValue >= MinHedgeValue
   AND wallet.noValue >= MinHedgeValue
THEN position is hedged
```

### Detecting Hedge Removal

```
IF trade.side == "SELL"
   AND wallet had hedged position before trade
   AND soldPct >= SignificantSellPct
THEN trigger HedgeRemoval alert
```

### Algorithm

1. When a SELL trade is detected
2. Check rate limit (avoid spamming position API)
3. Fetch wallet's current positions in this market
4. Calculate positions before the trade (add back sold shares)
5. If previously hedged and now significantly imbalanced, alert
6. Store event for resolution tracking

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HEDGE_TRACKER_GIST_ID` | - | Gist ID for persistence |
| `HEDGE_MIN_SIZE` | `100` | Minimum shares on each side |
| `HEDGE_MIN_VALUE` | `500` | Minimum USD value on each side |
| `HEDGE_SIGNIFICANT_SELL_PCT` | `0.50` | Reduction % to trigger (50%) |
| `HEDGE_POSITION_CHECK_INTERVAL` | `5m` | Cooldown per wallet+market |
| `HEDGE_MAX_POSITION_CHECKS` | `60` | Max API calls per minute |

## Alert Title

- Single: `"Hedge Removal Detected"`
- Combined examples:
  - `"Hedge Removal + Asymmetric Exit Pattern"`
  - `"Hedge Removal + High Win Rate"`
- Follow-up: `"Hedge Removal Confirmed"` (if they removed the losing side)

## Example Scenario

Before trade:
- YES: 1,000 shares @ $0.55 = $550
- NO: 800 shares @ $0.45 = $360

Trade: SELL 700 NO shares @ $0.45

After trade:
- YES: 1,000 shares @ $0.55 = $550
- NO: 100 shares @ $0.45 = $45

Analysis:
- Sold 87.5% of NO position
- Removed hedge protection on NO side
- If market resolves YES, they kept the winner

## Resolution Tracking

After detecting hedge removal:
1. Store the event with sold side and timestamp
2. Periodically check if market has resolved
3. If resolved, determine winning outcome
4. If they removed the losing side = CONFIRMED suspicious
5. Send follow-up "Resolution Confirmed" alert

```go
type HedgeRemovalEvent struct {
    Wallet         string
    ConditionID    string
    SoldSide       string    // "YES" or "NO"
    RemovedAt      time.Time
    Resolved       bool
    WinningOutcome string
    RemovedLoser   bool      // True if they got it right
}
```

## Rate Limiting

To avoid overloading the Positions API:
- Cooldown of 5 minutes per wallet+market combination
- Maximum 60 position checks per minute globally
- Uses sliding window for rate limit tracking

## Limitations

- Hedges may be removed for legitimate portfolio rebalancing
- Traders may be taking profit on one side
- Market makers adjust positions frequently
- Some hedges are meant to be temporary

## Code Location

- Hedge tracking: `internal/app/hedge_tracker.go`
- Detection: `HedgeTracker.ProcessTrade()`
- Resolution: `HedgeTracker.checkResolutions()` goroutine
