#!/bin/bash
# Run the opt-in differential test against a private tmux server.
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v tmux >/dev/null 2>&1; then
    echo "tmux is required for the differential test" >&2
    exit 1
fi

TERMEMU_TMUX_DIFF=1 go test -run '^TestDifferentialTmux' -v "$@" .
