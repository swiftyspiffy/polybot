# Extreme Odds Detection

## Overview

Alerts on large bets placed at extreme low prices (long-shot odds). Buying significant positions at 3% or less suggests the trader believes the market is significantly mispriced.

## Rationale

- Extreme low prices represent outcomes the market considers highly unlikely
- Large bets on long-shots require conviction that the market is wrong
- Informed traders may exploit mispriced long-shots before information becomes public
- The asymmetric payoff (betting $1 to win $30+) makes these bets attractive for informed traders

## Detection Logic

```
IF trade.price <= ExtremeLowPrice
   AND trade.side == "BUY"
   AND trade.notional >= ExtremeMinNotional
THEN trigger ExtremeBet alert
```

### Algorithm

1. When a trade is detected
2. Check if it's a BUY order (selling at extreme prices is less suspicious)
3. Check if price is at or below the extreme threshold (default: 3 cents)
4. Check if notional value exceeds minimum threshold
5. If all conditions met, flag the trade

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_EXTREME_LOW_PRICE` | `0.03` | Price threshold (3 cents = 3% implied probability) |
| `TRADE_EXTREME_MIN_NOTIONAL` | `2500` | Minimum trade size (USD) for extreme odds |

## Alert Title

- Single: `"Extreme Odds Bet"`
- Combined examples:
  - `"Low Activity + Extreme Odds Bet"`
  - `"High Win Rate + Extreme Odds Bet"`
  - `"New Wallet + Extreme Odds Bet"`

## Price Interpretation

| Price | Implied Probability | Market View |
|-------|---------------------|-------------|
| $0.03 | 3% | Highly unlikely |
| $0.05 | 5% | Very unlikely |
| $0.10 | 10% | Unlikely |

## Example Scenario

A wallet buys 50,000 shares at $0.02:
- Cost: $1,000
- Potential payout if correct: $50,000 (50x return)
- Market implies only 2% chance of winning
- If the trader has information, this is extremely profitable

## Limitations

- Some traders specialize in finding mispriced long-shots legitimately
- Volatility around news events can cause temporary mispricings
- Hedging strategies may involve buying extreme odds as insurance
- Low liquidity can cause temporary price dislocations

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Price check: Compares `event.Price` against `cfg.ExtremeLowPrice`
