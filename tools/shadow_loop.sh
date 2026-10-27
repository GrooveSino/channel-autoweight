#!/usr/bin/env bash
# Shadow mode daemon loop: run shadow.py every INTERVAL seconds; flock against re-entry, timeout, one failure does not affect the next run.
# Start: tools/shadowctl.sh start   Stop: tools/shadowctl.sh stop   Status: tools/shadowctl.sh status
cd "$(dirname "$0")/.." || exit 1
mkdir -p data
INTERVAL=${INTERVAL:-300}; TMO=${TMO:-240}; LOG=data/shadow.log
while true; do
  start=$(date +%s)
  # v1.20: detect gaps (an idle host may be frozen; sleep spans the freeze and the wall clock jumps)
  [ -n "$last" ] && [ $(( start - last )) -gt $(( INTERVAL * 3 / 2 )) ] && echo "=== WARN gap $(( (start - last) / 60 ))m since last run (box frozen or loop stalled)" >>"$LOG"
  last=$start
  {
    echo "=== $(date '+%F %T %Z') run"
    flock -n data/.shadow.lock timeout -k 10 "$TMO" python3 shadow.py
    echo "=== exit=$? $(( $(date +%s) - start ))s"
  } >>"$LOG" 2>&1
  # rotate the log above 5MB
  [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 5000000 ] && mv -f "$LOG" "$LOG.1"
  # align to multiples of INTERVAL (clock aligned, 00/05/10...)
  # v1.21: sleep in wall-clock slices (<= 15s each). During a freeze a single long sleep is timed on the monotonic clock and keeps sleeping after thawing,
  # delaying the next run; with slices the loop notices within 15s that the target time has passed
  target=$(( $(date +%s) / INTERVAL * INTERVAL + INTERVAL ))
  while [ "$(date +%s)" -lt "$target" ]; do sleep $(( target - $(date +%s) < 15 ? target - $(date +%s) : 15 )); done
done
