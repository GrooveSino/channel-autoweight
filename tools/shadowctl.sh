#!/usr/bin/env bash
# Shadow mode control: start | stop | status | restart
cd "$(dirname "$0")/.." || exit 1
mkdir -p data
PIDF=data/shadow_loop.pid
running() { [ -f $PIDF ] && kill -0 "$(cat $PIDF)" 2>/dev/null; }
case "$1" in
  start) running && { echo "already running pid $(cat $PIDF)"; exit 0; }
         setsid nohup tools/shadow_loop.sh >/dev/null 2>&1 & echo $! > $PIDF; echo "started pid $(cat $PIDF)";;
  stop)  running && { kill -- -"$(cat $PIDF)" 2>/dev/null || kill "$(cat $PIDF)"; rm -f $PIDF; echo stopped; } || echo "not running";;
  restart) "$0" stop; sleep 1; "$0" start;;
  status) running && echo "running pid $(cat $PIDF)" || echo "not running"; tail -n 12 data/shadow.log 2>/dev/null;;
  *) echo "usage: $0 start|stop|status|restart"; exit 1;;
esac
