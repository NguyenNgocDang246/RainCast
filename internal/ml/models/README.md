Models exported by `scripts/ml/train.py` (see COMMANDS.md). cmd/backtest
reads them from here (`-ml-dir`); they are not built into any binary, and
being several MB each they are not committed (.gitignore).

- `<set>_fold<k>.json`: trained without fold k; the backtest scores fold k with it.
- `<set>.json`: trained on every region.
- `parity.json`: rows and the probabilities LightGBM gave them, checked by the Go tests against `<set>.json` of the set named in it (the first trained).
- `summary.json`: validation CSI per strength and lead.
