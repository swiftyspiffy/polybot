# Asymmetric Exit Detection

## Overview

Identifies wallets that systematically exit winning positions faster than losing positions. This asymmetric behavior suggests the trader can distinguish winners from losers early, indicating potential informed trading.

## Rationale

- Random traders should exit winners and losers at similar rates
- Exiting winners quickly while holding losers is irrational (opposite of "cut losses, let winners run")
- But if you KNOW which positions will win, exiting them early locks in profit
- The asymmetry suggests the trader identifies winners before the market does

## Detection Logic

```
IF wallet.totalExits >= MinExitsForAsymmetric
   AND wallet.avgLossHoldDuration / wallet.avgWinHoldDuration >= AsymmetricThreshold
THEN trigger AsymmetricExit alert
```

### Algorithm

1. Track all position exits (sells that close or reduce positions)
2. For each exit, record:
   - Whether it was a winner (positive PnL) or loser (negative PnL)
   - How long the position was held
3. Calculate average hold duration for winners vs losers
4. If losers are held significantly longer, flag the wallet

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HEDGE_MIN_EXITS_FOR_ASYMMETRIC` | `5` | Minimum exits to detect pattern |
| `HEDGE_ASYMMETRIC_THRESHOLD` | `2.0` | Ratio threshold (losers held 2x longer) |

## Alert Title

- Single: `"Asymmetric Exit Pattern"`
- Combined examples:
  - `"Hedge Removal + Asymmetric Exit Pattern"`
  - `"Asymmetric Exit + High Win Rate"`

## Data Structure

```go
type AsymmetricExitStats struct {
    Wallet              string
    WinningExits        int
    LosingExits         int
    TotalWinHoldTime    float64  // seconds
    TotalLossHoldTime   float64
    AvgWinHoldDuration  float64
    AvgLossHoldDuration float64
}

type ExitRecord struct {
    ConditionID   string
    Outcome       string
    ExitPrice     float64
    AvgEntryPrice float64
    Size          float64
    RealizedPnl   float64
    IsWinner      bool
    HoldDuration  float64  // seconds
    ExitedAt      time.Time
}
```

## Example Scenario

Wallet's exit history:
| Position | Type | Hold Time | PnL |
|----------|------|-----------|-----|
| Market A | Winner | 2 hours | +$500 |
| Market B | Winner | 3 hours | +$300 |
| Market C | Loser | 8 hours | -$400 |
| Market D | Winner | 1 hour | +$200 |
| Market E | Loser | 12 hours | -$600 |

Analysis:
- Avg winner hold: (2+3+1)/3 = 2 hours
- Avg loser hold: (8+12)/2 = 10 hours
- Ratio: 10/2 = 5x

With threshold of 2.0, this wallet is flagged because losers are held 5x longer than winners.

## Behavioral Interpretation

| Pattern | Hold Time Ratio | Interpretation |
|---------|-----------------|----------------|
| Normal | ~1.0x | Random/uninformed trading |
| Disposition Effect | <1.0x | Sells winners, holds losers (common bias) |
| **Asymmetric** | >2.0x | Exits winners early (potential informed) |

Most traders exhibit the "disposition effect" - selling winners too early and holding losers too long. A wallet that does the OPPOSITE and holds losers much longer than winners is unusual.

## Why This Works

If a trader knows which positions will win:
1. They can exit winning positions immediately after entry (quick profit)
2. They hold losing positions hoping for recovery (but they know they'll lose)
3. The asymmetry reveals their foreknowledge

## Limitations

- Small sample sizes produce unreliable statistics
- Some trading strategies legitimately have asymmetric exits
- Volatility can cause winners to be exited early on spikes
- Position sizing affects hold time decisions

## Code Location

- Exit tracking: `internal/app/hedge_tracker.go`
- Stats calculation: `HedgeTracker.recordExitInternal()`
- Detection: `HedgeTracker.ShouldAlertAsymmetric()`
