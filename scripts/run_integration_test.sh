#!/bin/bash
# Run the tmux integration test (examples/tmux).

set -eo pipefail

cd "$(dirname "$0")/.."

bin=scripts/tmux_integration_test
results=scripts/integration_test_results.txt

echo "Building test..."
go build -o "$bin" ./examples/tmux

if [ -z "$TMUX" ]; then
    echo "Not in tmux, starting a session..."
    tmux new-session "$bin $* 2>$results"
else
    echo "Running in existing tmux session..."
    "$bin" "$@" 2>"$results"
fi

cat "$results"
echo ''
echo "Results saved to $results"
