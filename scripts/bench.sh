#!/usr/bin/env bash
# Run termemu benchmarks, optionally with CPU/heap profiles.
#
# Usage:
#   scripts/bench.sh                      # all benchmarks, table output
#   scripts/bench.sh 'TerminalWrite'      # benchmarks matching a regex
#   scripts/bench.sh . 5s                 # benchtime
#   PROFILE=1 scripts/bench.sh Scrollback # also write cpu.pprof/mem.pprof and print pprof top
#
# Results are also saved as benchstat-compatible text in /tmp/termemu-bench.txt.
set -euo pipefail
cd "$(dirname "$0")/.."

pattern="${1:-.}"
benchtime="${2:-200ms}"
out="${BENCH_OUT:-/tmp/termemu-bench.txt}"

args=(-run '^$' -bench "$pattern" -benchmem -benchtime "$benchtime" -count "${BENCH_COUNT:-1}" .)

if [[ "${PROFILE:-0}" == "1" ]]; then
  args+=(-cpuprofile cpu.pprof -memprofile mem.pprof)
fi

go test "${args[@]}" | tee "$out"

if [[ "${PROFILE:-0}" == "1" ]]; then
  echo
  echo "== CPU top =="
  go tool pprof -top -nodecount=25 -sample_index=cpu cpu.pprof
  echo
  echo "== allocated heap top =="
  go tool pprof -top -nodecount=25 -sample_index=alloc_space mem.pprof
fi
