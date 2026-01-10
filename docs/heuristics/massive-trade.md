# Massive Trade Detection

## Overview

Whale alerts for very large trades regardless of other factors. Any trade above a significant threshold is noteworthy simply due to its size and potential market impact.

## Rationale

- Very large trades move markets and deserve attention
- Whales often have more information or conviction than average traders
- Large positions concentrate risk, suggesting high confidence
- Market impact of massive trades affects other participants

## Detection Logic

```
IF trade.notional >= MassiveTradeMinNotional
THEN trigger MassiveTrade alert
```

### Algorithm

1. When any trade is detected
2. Check if notional value exceeds the massive trade threshold
3. If so, immediately flag regardless of other factors

This is the simplest heuristic - pure size-based detection.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_MASSIVE_MIN_NOTIONAL` | `50000` | Minimum trade size (USD) for whale alert |

## Alert Title

- Single: `"Massive Trade"`
- Combined examples:
  - `"Massive Trade + High Win Rate"`
  - `"Massive Trade + Low Activity"`
  - `"Massive Trade + New Wallet"`

## Size Tiers

| Notional | Classification | Alert Level |
|----------|----------------|-------------|
| $4,000+ | Standard | Regular heuristics apply |
| $10,000+ | Large | New wallet threshold |
| $50,000+ | Massive | Always alerts |
| $100,000+ | Whale | Critical attention |

## Example Scenario

A wallet executes:
- Buy 100,000 shares at $0.65 = $65,000

This triggers immediately because:
- $65,000 > $50,000 threshold
- No other checks needed

## Market Impact Considerations

Massive trades can:
- Move prices significantly
- Trigger other traders' stop-losses
- Signal institutional interest
- Create arbitrage opportunities
- Indicate imminent news or events

## Limitations

- Market makers may legitimately trade large sizes
- Institutional rebalancing causes large trades
- OTC (over-the-counter) deals may appear on-chain
- Large trades don't necessarily indicate informed trading

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Simple check: `notional >= cfg.MassiveTradeMinNotional`
