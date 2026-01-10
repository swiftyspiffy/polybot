# Low Activity Wallet Detection

## Overview

Identifies wallets that have traded in very few unique markets but are making large trades. The hypothesis is that informed traders may use fresh or low-activity wallets to avoid detection.

## Rationale

- Insiders or informed traders often create new wallets to avoid linking suspicious trades to their main accounts
- A wallet with minimal trading history making a significant bet may indicate non-public information
- Low activity combined with large position sizes is a classic pattern of informed trading

## Detection Logic

```
IF wallet.uniqueMarkets <= MaxMarketsForLow
   AND trade.notional >= MinNotional
THEN trigger LowActivity alert
```

### Algorithm

1. When a trade above the minimum notional threshold is detected
2. Fetch the wallet's trading history via the Data API
3. Count the number of unique markets (condition IDs) the wallet has traded
4. If the count is at or below the threshold, flag the trade

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TRADE_MIN_NOTIONAL` | `4000` | Minimum trade size (USD) to evaluate |
| `TRADE_MAX_MARKETS_FOR_LOW` | `5` | Maximum unique markets to be considered "low activity" |

## Alert Title

- Single: `"Low Activity Wallet"`
- Combined examples:
  - `"Low Activity + High Win Rate"`
  - `"Low Activity + Extreme Odds Bet"`
  - `"Low Activity + Rapid Trading"`

## Data Sources

- **Wallet Activity**: `GET /activity?user={address}` from Data API
- Returns list of trades with market/condition information

## Limitations

- New legitimate traders will trigger this alert
- Some experienced traders use multiple wallets legitimately
- Market makers or arbitrageurs may appear as low activity on specific markets

## Code Location

- Detection: `internal/app/trade_monitor.go` - `shouldAlert()` method
- Wallet stats: `internal/app/wallet_tracker.go` - `GetWalletStats()`
