#!/bin/bash
# Run the tmux integration test

set -eo pipefail

cd "$(dirname "$0")"

# Build the test
echo "Building test..."
go build ./examples/tmux_integration_test

# Check if we're in tmux
if [ -z "$TMUX" ]; then
    echo "Not in tmux, starting a session..."
    # Start tmux, run the test, redirect stderr to file, then cat it
    tmux new-session "./tmux_integration_test $* 2>integration_test_results.txt"
    cat integration_test_results.txt
    echo ''
    echo 'Results saved to integration_test_results.txt'
else
    echo "Running in existing tmux session..."
    ./tmux_integration_test "$@" 2>integration_test_results.txt
    cat integration_test_results.txt
    echo ""
    echo "Results saved to integration_test_results.txt"
fi
