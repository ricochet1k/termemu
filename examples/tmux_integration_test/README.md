# Tmux Integration Test

This binary tests the termemu library by comparing its output against tmux's rendering.

## Concept

The test sends escape sequences through three different paths:

1. **Raw through tmux**: Sends the escape sequence directly to tmux via `printf`
2. **Through termemu library**: Processes the sequence through termemu, extracts the ANSI output using `ANSILine()`, then sends to tmux
3. **Through TTY frontend**: Processes the sequence through termemu with the TTY frontend, captures the ANSI output it generates, then sends to tmux

All three outputs are normalized by using `tmux capture-pane -p -e` to capture the rendered screen with escape sequences. This ensures we're comparing apples to apples.

## Requirements

- Must be run inside a tmux session
- tmux must be available in PATH

## Usage

Start a tmux session:
```bash
tmux new-session
```

Then run the test:
```bash
go run ./examples/tmux_integration_test
```

Or build and run:
```bash
go build ./examples/tmux_integration_test
./tmux_integration_test
```

## Options

- `-width N` - Set terminal width (default: 80)
- `-height N` - Set terminal height (default: 24)
- `-verbose` - Show all outputs, not just failures
- `-test NAME` - Run only the named test

## Examples

Run with verbose output:
```bash
./tmux_integration_test -verbose
```

Run a specific test:
```bash
./tmux_integration_test -test colors
```

Test with a smaller terminal:
```bash
./tmux_integration_test -width 40 -height 10
```

## Test Cases

The test includes various terminal escape sequences:
- Simple text output
- Newlines and line breaks
- ANSI colors (basic and 256-color)
- Bold, underline, and reverse video
- Cursor movement and positioning
- Line and screen clearing
- Cursor save/restore
- Tabs and backspace
- Carriage returns

## Exit Codes

- `0` - All tests passed
- `1` - One or more tests failed or couldn't run outside tmux
