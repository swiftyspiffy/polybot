# New Wallet Detection

## Overview

Alerts when brand new wallets (with minimal or no prior trading history) make large initial bets. Fresh wallets making significant trades may indicate informed traders creating clean accounts.

## Rationale

- Informed traders often create new wallets to avoid linking suspicious activity to known accounts
- A wallet's first significant trade is particularly noteworthy if it's large
- New wallets have no track record, making it harder to assess their credibility
- The combination of no history + large bet size is a strong signal

## Detection Logic

```
IF wallet.uniqueMarkets <= NewWalletMaxMarkets
   AND trade.notional >= NewWalletMinNotional
THEN trigger NewWallet alert
```

### Algorithm

1. When a trade above the new wallet notional threshold is detected
2. Fetch the wallet's complete trading history
3. Count unique markets the wallet has ever traded
4. If the wallet has traded in 0-1 markets and is making a large bet, flag it

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_NEW_WALLET_MAX_MARKETS` | `1` | Maximum prior markets to be "new" (0 or 1) |
| `TRADE_NEW_WALLET_MIN_NOTIONAL` | `10000` | Minimum trade size (USD) for new wallet alert |

## Alert Title

- Single: `"New Wallet Large Bet"`
- Combined examples:
  - `"New Wallet + Extreme Odds Bet"`
  - `"New Wallet + Low Activity"` (redundant but possible)
  - `"Massive Trade + New Wallet"`

## Difference from Low Activity

| Aspect | New Wallet | Low Activity |
|--------|------------|--------------|
| Max Markets | 1 | 5 |
| Min Notional | $10,000 | $4,000 |
| Focus | First-time large traders | Infrequent traders |

New Wallet is a stricter subset focused on completely fresh accounts making very large bets.

## Example Scenario

A wallet with zero prior trades suddenly:
- Buys 20,000 shares at $0.55 = $11,000

This triggers the new wallet alert because:
- Prior markets = 0 (≤1 threshold)
- Notional = $11,000 (≥$10,000 threshold)

## Limitations

- Legitimate new users may start with large trades
- Wallet may have activity on other chains not visible to Polymarket
- Some experienced traders genuinely use new wallets for organization
- Airdrops or promotions may cause influx of new wallets

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Wallet stats: `internal/app/wallet_tracker.go` - `GetWalletStats()`
