# Contrarian Bet Detection

## Overview

Alerts on large bets placed against the market consensus (buying at very low prices or selling at very high prices). Contrarian bets require strong conviction that the market is wrong.

## Rationale

- Markets generally aggregate information efficiently
- Betting against strong consensus requires believing you know something the market doesn't
- Large contrarian positions have asymmetric payoffs (small cost, large potential gain)
- Informed traders may exploit situations where public perception differs from reality

## Detection Logic

```
IF trade.side == "BUY"
   AND trade.price <= ContrarianMaxPrice
   AND trade.notional >= ContrarianMinNotional
THEN trigger ContrarianBet alert
```

### Algorithm

1. When a trade is detected
2. Check if it's a BUY at low price (or SELL at high price)
3. Low price = market thinks this outcome is unlikely
4. If notional exceeds threshold, flag as contrarian bet

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_CONTRARIAN_MAX_PRICE` | `0.10` | Maximum price for "contrarian" (10 cents) |
| `TRADE_CONTRARIAN_MIN_NOTIONAL` | `5000` | Minimum trade size (USD) |

## Alert Title

- Single: `"Contrarian Large Bet"`
- Combined examples:
  - `"Contrarian + New Wallet Bet"`
  - `"Contrarian + Low Activity Bet"`
  - `"Contrarian + High Win Rate Bet"`

## Difference from Extreme Odds

| Aspect | Contrarian Bet | Extreme Odds |
|--------|----------------|--------------|
| Max Price | 10 cents | 3 cents |
| Min Notional | $5,000 | $2,500 |
| Focus | Going against consensus | Betting on long-shots |

Contrarian is broader (10% vs 3%) but requires larger position size.

## Example Scenario

Market: "Will candidate X win the election?"
- Current price: $0.08 (8% implied probability)
- Wallet buys 100,000 shares at $0.08 = $8,000

This is contrarian because:
- The market thinks X has only 8% chance
- The trader is betting $8,000 that the market is wrong
- If correct, they win $100,000 - $8,000 = $92,000

## Market Psychology

```
Price    | Market Belief        | Contrarian Action
---------|---------------------|------------------
< 10%    | "Very unlikely"     | BUY = betting it happens
> 90%    | "Very likely"       | SELL = betting it doesn't
```

## Limitations

- Sophisticated traders may legitimately identify mispricings
- Hedging strategies may appear contrarian
- Market manipulation attempts (pump and dump)
- Liquidity provision at extreme prices

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Threshold: `cfg.ContrarianMaxPrice` and `cfg.ContrarianMinNotional`
