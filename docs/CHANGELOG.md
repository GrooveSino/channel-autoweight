# Changelog

Version summaries for the prototype. "Enabled" means the feature is on in the reference configuration (`config.example.json`); "off" means it ships disabled because replays did not show a clear win. Defaults change only when replays on recent shadow data are better across several horizons.

## v2.x

- **v2.02**: Fix a v1.96 regression where the current round's 1h error rate was lost from the on/off record, so the "persistent errors" disable counter never advanced. Rounds where every site probe (including the recheck) failed now also count as error rounds (`toggle.err_probe_fail`).
- **v2.01**: New diagnostic `tools/probe_log_gap.py`. It compares the probe first token with the real-log first token of the same round and flags probes that are unusually slow or fast.
- **v2.00**: On/off baseline fix (`toggle.skip_buffered_ttft`, enabled). A buffered probe's "first token" equals its total time, so it no longer feeds the bottom-third first-token baseline. The baseline falls back to the log first token, then to the direct-test median.

## v1.9x: autonomous on/off

- **v1.99**: `strategies.rank.mid_ratio` 0.6 -> 0.7 (enabled). Clearly slower channels drop to `low_w` instead of hovering at `mid_w`.
- **v1.98**: New `tools/toggle_audit.py`. It compares the site baseline with the same-path upstream baseline across rounds. A rise-confirmation re-evaluation was done and the feature stays off.
- **v1.97**: `newcomer.graduate_real_n60` (enabled). A new or re-enabled channel graduates only after some formal round has shown enough real 1h requests.
- **v1.96**: On/off decisions use real-user request counts (probe tokens excluded).
- **v1.95**: Same-path baseline for on/off. The bottom-third channels are also tested directly against their upstream, and both verdicts are recorded.
- **v1.94**: `watch.auto_skip`. The watch list skips channels that are already covered by the main probe or by the on/off direct tests.
- **v1.93**: The shadow first-token fallback for channels that were missing from formal probes (off). Optional `newcomer.max_age_h` (off).
- **v1.92**: `run.py --apply-toggle`. On/off actions run as a single transaction that updates channels and abilities, with the floors re-checked.
- **v1.91**: `toggle.min_log_n`. Log-based baseline values are used only with enough samples.
- **v1.90**: New `autoweight/toggle.py`, the autonomous on/off planner. It discovers disabled channels, runs direct upstream tests, and applies the rank-based and error-based disable rules with floors.

## v1.8x: diagnostics and stability

- **v1.89**: `newcomer.explore_w` (enabled). The exploration floor stops new channels from starving at weight 0.
- **v1.88**: `strategies.rank.co_lead_ratio`, co-leaders (off).
- **v1.87**: The report table no longer lists a watched channel twice.
- **v1.86**: Diagnostic `diag.probe_flip`. It flags channels whose first probe is repeatedly "buffered" while the recheck streams.
- **v1.85**: `strategies.rank.lead_trust`, a trusted-leader reference (off).
- **v1.84**: `newcomer.reset_gap_rounds`. A channel that comes back after a long absence counts as new again (off).
- **v1.83**: New `autoweight/upstream.py`, a direct upstream probe for disabled channels. It runs on the gateway host and the key never leaves that host.
- **v1.82**: Diagnostic `diag.shadow_flap`. It flags suggestions that oscillate.
- **v1.81**: Diagnostic `diag.reenable_check`. It reports when a disabled channel looks ready to come back.
- **v1.80**: Diagnostic `diag.zero_weight_leak`. It flags channels that still get traffic at weight 0, for example through sticky sessions.

## v1.7x: noise control

- **v1.79**: `score.timeout_slow`, which counts timeouts as slow requests too (off).
- **v1.78**: The write guard blocks only on drift of channels whose final value follows the system suggestion.
- **v1.77**: Newcomer detection uses the full formal-round history.
- **v1.76**: `small_sample` cap (off).
- **v1.75**: `score.log_ttft.adapt_full_n` (enabled). The more real first-token samples there are, the more the log first token is trusted.
- **v1.74**: Re-evaluated latency shrinkage; it stays off. Added regression tests.
- **v1.73**: `trust.err_10m_full_n`, sample-scaled 10m error severity (off).
- **v1.72**: `score.err_recover`, discounting short error bursts that have already recovered (off).
- **v1.71**: `score.client_err.mode=cross_channel` (enabled). Only client errors that follow one user across channels are excluded.
- **v1.70**: The gateway shadow comparison tool now handles shadow-mode runs correctly.

## v1.6x

- **v1.69**: `log_stream_veto.scope` (enabled, `both`). Real streaming traffic in the logs can veto a probe's "buffered" verdict for scoring and for trust.
- **v1.68**: Fix: a dry-run probe that is reused at apply time keeps its trust events.
- **v1.67**: `strategies.rank.low_traffic` tier parameters (off).
- **v1.66**: `trust_cap.ramp` (off).
- **v1.65**: Shadow review de-overlaps the 10m windows. Added a low-traffic reference verdict.
- **v1.64**: `tie_ratio` 0.9 -> 0.95 (enabled). Fewer flips into uniform weights at low traffic.
- **v1.63**: `tools/recheck_audit.py`. Replay can be cut by absolute time.
- **v1.62**: The minimum channel weight is 0 (previously 1).
- **v1.61**: `trust_cap.max_n60` (enabled). Only low-trust channels with low traffic are capped.
- **v1.60**: The watch list skips deleted channels and does not probe disabled ones.

## v1.5x: workload-aware latency

- **v1.59**: `tools/buffer_audit.py`, an audit of probe "buffered" verdicts against real traffic.
- **v1.58**: `score.client_err`, separating client (4xx) errors from channel faults.
- **v1.57**: `capacity.r_mode=rest` (enabled). Fixes a capacity cap that looped too tight.
- **v1.56**: `log_stream_veto` and real-traffic streaming features.
- **v1.55**: `tools/error_timeline.py`, an error timeline per channel with error classes.
- **v1.54**: `tools/pbest.py`, a bootstrap estimate of `P(best)`.
- **v1.53**: `score.log_ttft` (enabled). The real-log first token is blended with the probe first token.
- **v1.52**: `score.workload_match` (enabled). The latency term is blended with workload-matched ratios.
- **v1.51**: `tools/site_compare.py`, comparing the prototype with the gateway's built-in shadow module.
- **v1.50**: Workload-matched evaluation (`exp_mr`) in the replay tools.

## v1.4x: newcomers and capacity

- **v1.49**: `tools/workload_match.py`. Replay restores the newcomer history.
- **v1.48**: `capacity`, a concurrency capacity cap.
- **v1.47**: Newcomer onboarding. Channels are auto-discovered, new ones start at low trust, and they are capped until real traffic arrives. Fixed a soft-cap rerun loop.
- **v1.46**: `trust_cap`, a low-trust cap.
- **v1.45**: `tools/override_eval.py`, a scorecard of manual review vs the system suggestion.
- **v1.44**: Continuous tiers `strategies.rank.ramp` (off).
- **v1.43**: Formal rounds take trust from the latest shadow point (enabled).
- **v1.42**: Asymmetric 10m window weight (off).
- **v1.41**: Write safety: `--final` (must cover every channel) and `--expect-system`.
- **v1.40**: `band_cap_aware` (off).

## v1.3x: probabilistic scoring experiments

- **v1.39**: Concurrency load discount (off).
- **v1.38**: Small-sample confidence discount (off).
- **v1.37**: Shadow review compares by score and reports real traffic share.
- **v1.36**: `score.slow_burst`, sudden-slowdown detection (enabled).
- **v1.35**: Rise confirmation limited to small-sample channels (off).
- **v1.34**: Rise confirmation (off). Fixed the buffered streak count.
- **v1.33**: Fixed the shadow probe age.
- **v1.32**: Shadow trust counts each probe event only once.
- **v1.31**: Buffering probability from a Beta posterior (off).
- **v1.30**: Workload comparability guard (off).

## v1.2x: shadow mode matures

- **v1.29**: Bayesian slow-request severity (off).
- **v1.28**: Shadow trust de-overlaps 1h events.
- **v1.27**: Sample-size-adaptive 10m window weight (off).
- **v1.26**: `tools/share_audit.py`, a new-session share vs weight share audit (sticky sessions).
- **v1.25**: Evidence-based soft-cap relaxation (off).
- **v1.24**: Asymmetric score smoothing (off).
- **v1.23**: Tier hysteresis `band` (off). Added the shadow replay tool.
- **v1.22**: Length-normalised latency (off).
- **v1.21**: Gap backfill for the shadow mode. Wall-clock sliced sleeps for hosts that get frozen.
- **v1.20**: Manual override validation, shadow coverage and de-duplication.

## v1.1x: trust and shadow mode

- **v1.19**: Shadow mode: read-only suggestions every 5 minutes.
- **v1.18**: Trust with slow rise and fast fall.
- **v1.17**: Markdown report table.
- **v1.16**: Soft-cap review in `evaluate.py --caps`.
- **v1.15**: Leader hysteresis (off).
- **v1.14**: Stability comparison between dry runs and formal rounds.
- **v1.13**: Watch list for suspicious or disabled channels.
- **v1.12**: Probe result reuse.
- **v1.11**: Recovery observation cap (off).
- **v1.10**: `verify.py`, a post-write consistency check.

## v1.0 - v1.9: foundations

- **v1.9**: Diagnostics: channel set changes and buffered streaks.
- **v1.8**: Empty successes count towards the bad-request rate. Added a penalty when every probe failed.
- **v1.7**: Probe anomaly classification.
- **v1.6**: Traffic-mix diagnostics: real users vs probes, token concentration.
- **v1.5**: Probe-token rows are excluded from real-user metrics.
- **v1.4**: Outlier first-token recheck.
- **v1.3**: Small-sample latency shrinkage (off). Added `evaluate.py`.
- **v1.2**: 10m error burst factor.
- **v1.1**: Three-state streaming verdict.
- **v1.0**: First version: collection, probing, scoring, rank and proportional strategies, soft caps, error alerts, dry run and `--apply`, round records, review, backtest, unit tests.
