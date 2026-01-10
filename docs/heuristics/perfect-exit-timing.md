# Perfect Exit Timing Detection

## Overview

Tracks wallets that consistently exit positions near local price peaks. If someone sells and the price drops significantly after, they timed their exit well. Doing this repeatedly suggests foreknowledge of price movements or outcomes.

## Rationale

- Timing the top is extremely difficult in prediction markets
- Consistent exits near peaks suggests more than luck
- Informed traders may exit just before negative news
- Even small timing edges compound over many trades
- A pattern of perfect exits is a strong insider signal

## Detection Logic

### Recording Exits

```
FOR each SELL trade:
   Record exit_price, timestamp, and market details
   Add to pending exits for later verification
```

### Timing Verification (Delayed)

```
After PerfectExitCheckDelay (default: 24h):
   Fetch current_price for that market
   timing_score = exit_price / max(exit_price, price_now)
   Store timing_score for wallet statistics
```

### Alert Trigger

```
IF wallet.verified_exits >= PerfectExitMinExits
   AND wallet.avg_timing_score >= PerfectExitMinScore
THEN trigger PerfectExitTiming alert on next trade
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PATTERN_TRACKER_GIST_ID` | - | Gist ID for persistence |
| `PERFECT_EXIT_CHECK_DELAY` | `24h` | Wait time before checking price |
| `PERFECT_EXIT_MIN_EXITS` | `5` | Minimum verified exits to analyze |
| `PERFECT_EXIT_MIN_SCORE` | `0.90` | Minimum average timing score (90%) |
| `PERFECT_EXIT_CHECK_INTERVAL` | `1h` | How often to verify pending exits |

## Alert Title

- Single: `"Perfect Exit Timing"`
- Combined examples:
  - `"Perfect Exit Timing + High Win Rate"`
  - `"Perfect Exit Timing + Asymmetric Exit"`

## Example Scenario

Exit timing analysis for wallet 0xABC:

| Exit Date | Exit Price | Price 24h Later | Timing Score |
|-----------|------------|-----------------|--------------|
| Jan 1 | $0.75 | $0.60 | 1.00 |
| Jan 5 | $0.82 | $0.78 | 0.95 |
| Jan 8 | $0.68 | $0.55 | 1.00 |
| Jan 12 | $0.91 | $0.85 | 0.93 |
| Jan 15 | $0.73 | $0.70 | 0.96 |

Average timing score: 0.97 (97% of peak on average)

Analysis:
- 5 exits analyzed
- Average timing score 97%
- Consistently exits near local tops
- Strong indicator of informed trading

## Timing Score Calculation

```go
// Score of 1.0 means exit was at or above peak
// Score of 0.9 means exited within 10% of peak
func timingScore(exitPrice, priceAfter float64) float64 {
    if priceAfter >= exitPrice {
        return 1.0  // Still at or below exit price
    }
    return exitPrice / priceAfter  // May be > 1 if price dropped
}
```

## Background Verification

The system uses a background goroutine to verify exits:

1. Runs every `PerfectExitCheckInterval` (default: 1 hour)
2. Checks pending exits older than `PerfectExitCheckDelay`
3. Fetches current market price
4. Calculates timing score
5. Updates wallet's timing statistics
6. Cleans up verified exits after 7 days

## Alert Data

```go
type ExitTimingStats struct {
    Wallet           string
    VerifiedExits    int       // Number of exits analyzed
    TotalTimingScore float64
    AvgTimingScore   float64   // Average across all exits
    PerfectExits     int       // Exits with score >= 0.95
}
```

## Limitations

- Markets can move after exits for unrelated reasons
- News events can cause sudden price changes
- Lucky timing happens occasionally
- 24-hour delay may miss short-term price movements
- Price verification depends on market still being active

## Future Improvements

- Track prices at multiple intervals (1h, 6h, 24h)
- Weight recent exits more heavily
- Consider trade size in scoring
- Cross-reference with market resolution outcomes

## Code Location

- Pattern tracking: `internal/app/pattern_tracker.go`
- Exit recording: `PatternTracker.recordExit()`
- Verification: `PatternTracker.runExitTimingChecker()` goroutine
- Statistics: `PatternTracker.GetExitTimingStats()`
