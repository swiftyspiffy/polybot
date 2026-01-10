# Detection Heuristics

Polybot uses multiple heuristics to identify potentially suspicious trading patterns. Each heuristic focuses on a different behavioral signal that may indicate informed trading.

## Heuristic Overview

| Heuristic | Signal | Key Threshold |
|-----------|--------|---------------|
| [Low Activity Wallet](low-activity-wallet.md) | Few markets traded | ≤5 markets |
| [High Win Rate](high-win-rate.md) | Consistent winning | ≥90% on 5+ positions |
| [Extreme Odds](extreme-odds.md) | Betting on long-shots | ≤3% price |
| [Rapid Trading](rapid-trading.md) | Multiple trades quickly | 3+ trades in 5 min |
| [New Wallet](new-wallet.md) | Fresh account, large bet | ≤1 market, $10k+ |
| [Contrarian Bet](contrarian-bet.md) | Against consensus | ≤10% price |
| [Massive Trade](massive-trade.md) | Whale activity | $50k+ |
| [Contrarian Winner](contrarian-winner.md) | Proven track record | 3+ wins, 70% rate |
| [Copy Trading](copy-trading.md) | Following leaders | 3+ copies in 10 min |
| [Hedge Removal](hedge-removal.md) | Removing hedge protection | 50%+ sold |
| [Asymmetric Exit](asymmetric-exit.md) | Faster winner exits | 2x hold time ratio |
| [Conviction Doubling](conviction-doubling.md) | Adding to losing positions | 10%+ underwater |
| [Perfect Exit Timing](perfect-exit-timing.md) | Exits near price peaks | ≥90% timing score |
| [Stealth Accumulation](stealth-accumulation.md) | Gradual position building | 3+ trades, $10k+ |

## Heuristic Categories

### Wallet-Based
Focus on the trader's history and characteristics:
- Low Activity Wallet
- High Win Rate
- New Wallet
- Contrarian Winner

### Trade-Based
Focus on the specific trade characteristics:
- Extreme Odds
- Contrarian Bet
- Massive Trade

### Behavioral Patterns
Focus on trading behavior over time:
- Rapid Trading
- Copy Trading
- Hedge Removal
- Asymmetric Exit
- Conviction Doubling
- Perfect Exit Timing
- Stealth Accumulation

## Alert Combinations

Alerts are more significant when multiple heuristics trigger on the same trade. Common high-signal combinations:

| Combination | Significance |
|-------------|-------------|
| Low Activity + High Win Rate | Strong: new account winning consistently |
| Contrarian + New Wallet | Strong: fresh account betting against consensus |
| Massive Trade + High Win Rate | Strong: big bet from proven winner |
| Hedge Removal + High Win Rate | Very Strong: informed hedge unwinding |
| Proven Contrarian + Contrarian Bet | Very Strong: repeat contrarian behavior |
| Conviction Doubling + High Win Rate | Very Strong: adding to losing position by proven winner |
| Perfect Exit Timing + Asymmetric Exit | Very Strong: consistently exits well |
| Stealth Accumulation + Low Activity | Strong: quiet wallet accumulating silently |
| Stealth Accumulation + Contrarian Bet | Very Strong: hidden contrarian accumulation |

## Configuration

All heuristics are configurable via environment variables. See the main [README](../../README.md) for the full configuration reference.

## Adding New Heuristics

To add a new heuristic:

1. Implement detection logic in `internal/app/trade_monitor.go` or create a new tracker
2. Add alert reason to `clients/notifier/notifier.go`
3. Add alert titles to `clients/discord/discord.go` and `clients/telegram/telegram.go`
4. Add configuration to `config/config.go`
5. Create documentation in `docs/heuristics/`
6. Add tests
