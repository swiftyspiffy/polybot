# Conviction Doubling Detection

## Overview

Alerts when traders add to losing positions with confidence. If a wallet has an existing position that's underwater (current price < entry price) and they buy MORE of the same outcome, it signals unusual conviction. Informed traders double down because they know the outcome.

## Rationale

- Most retail traders cut losses or panic sell when positions go underwater
- Adding to a losing position is psychologically difficult
- Doing so confidently suggests the trader has information
- If they add significantly while down 10%+, they believe the market is wrong
- Insiders would double down knowing their position will eventually win

## Detection Logic

### Identifying Conviction Doubling

```
IF wallet has existing position in market
   AND current_price < avg_entry_price (position is losing)
   AND unrealized_loss_pct >= ConvictionMinLossPct
   AND trade.side == "BUY"
   AND trade.size >= ConvictionMinAddSize
   AND trade.notional >= ConvictionMinAddValue
THEN trigger ConvictionDoubling alert
```

### Algorithm

1. When a BUY trade is detected
2. Check rate limit (avoid spamming position API)
3. Fetch wallet's current positions in this market
4. Find the position matching the traded outcome
5. Calculate if position existed before this trade
6. Check if position is underwater (current < avg entry)
7. Calculate loss percentage
8. If loss exceeds threshold and trade size meets minimums, alert

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PATTERN_TRACKER_GIST_ID` | - | Gist ID for persistence |
| `CONVICTION_MIN_ADD_SIZE` | `500` | Minimum shares to add |
| `CONVICTION_MIN_ADD_VALUE` | `1000` | Minimum USD value to add |
| `CONVICTION_MIN_LOSS_PCT` | `0.10` | Minimum unrealized loss (10%) |
| `CONVICTION_CHECK_INTERVAL` | `5m` | Cooldown per wallet+market |

## Alert Title

- Single: `"Conviction Doubling"`
- Combined examples:
  - `"Conviction Doubling + High Win Rate"`
  - `"Conviction Doubling + Proven Contrarian"`

## Example Scenario

Existing position:
- 1,500 YES shares @ $0.60 avg = $900 cost basis
- Current price: $0.48 (20% underwater)
- Unrealized loss: $180

Trade: BUY 500 YES shares @ $0.48 = $240

Analysis:
- Position was already 20% underwater
- Trader added $240 more to losing position
- Shows strong conviction market is mispriced
- If YES wins, they bought the dip with insider knowledge

## Alert Data

```go
type ConvictionAlert struct {
    ExistingSize   float64  // Size before this trade
    ExistingAvg    float64  // Average entry price
    CurrentPrice   float64  // Current market price
    LossPct        float64  // How much underwater (0.20 = 20%)
    AddedSize      float64  // Shares added
    AddedValue     float64  // USD value added
}
```

## Rate Limiting

To avoid overloading the Positions API:
- Cooldown of 5 minutes per wallet+market combination
- Maximum 60 position checks per minute globally
- Uses sliding window for rate limit tracking

## Limitations

- Value investors regularly add to losing positions ("buy the dip")
- Dollar-cost averaging is a legitimate strategy
- Market makers may rebalance positions
- Price could have dropped due to legitimate news
- Not all conviction buys are informed trades

## Code Location

- Pattern tracking: `internal/app/pattern_tracker.go`
- Detection: `PatternTracker.checkConvictionDoubling()`
- Integration: `TradeMonitor.processTradeEvent()`
