# termemu

A small Go library for emulating a terminal and rendering output through a pluggable frontend. It runs commands inside a PTY (or a fallback pipe backend) and exposes screen state, colors, and view flags.

## Features

- PTY-backed command execution with screen emulation
- Pluggable `Frontend` interface for rendering or collecting output
- Screen accessors for plain text, ANSI output, and styled lines
- Minimal backend abstraction for PTY or no-PTY environments

## Requirements

- Go 1.24+

## Install

```bash
go get github.com/ricochet1k/termemu
```

## Usage

### Run a command and print the rendered screen

```go
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ricochet1k/termemu"
)

func main() {
	// Use the EmptyFrontend if you don't need UI callbacks.
	term := termemu.New(&termemu.EmptyFrontend{})
	if term == nil {
		fmt.Println("failed to create terminal")
		return
	}

	cmd := exec.Command("sh", "-c", "printf 'Hello World'")
	if err := term.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error:", err)
		return
	}
	_ = cmd.Wait()

	// Dump the screen contents to stdout.
	term.PrintTerminal()
}
```

### Capture redraws with a custom frontend

```go
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ricochet1k/termemu"
)

type loggingFrontend struct{}

func (loggingFrontend) Bell()                                     {}
func (loggingFrontend) RegionChanged(r termemu.Region, _ termemu.ChangeReason) {
	fmt.Printf("region changed: (%d,%d)-(%d,%d)\n", r.X, r.Y, r.X2, r.Y2)
}
func (loggingFrontend) ScrollLines(y int)                         {}
func (loggingFrontend) CursorMoved(x, y int)                      {}
func (loggingFrontend) ColorsChanged(f, b termemu.Color)          {}
func (loggingFrontend) ViewFlagChanged(v termemu.ViewFlag, v2 bool) {}
func (loggingFrontend) ViewIntChanged(v termemu.ViewInt, v2 int)   {}
func (loggingFrontend) ViewStringChanged(v termemu.ViewString, v2 string) {}

func main() {
	term := termemu.New(loggingFrontend{})
	cmd := exec.Command("sh", "-c", "printf '\\e[31mred\\e[0m' && echo")
	if err := term.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error:", err)
		return
	}
	_ = cmd.Wait()

	for y := 0; y < 1; y++ {
		fmt.Println(term.ANSILine(y))
	}
}
```

## API highlights

- `termemu.New(frontend)` creates a terminal with a PTY backend.
- `termemu.NewWithBackend(frontend, backend)` allows swapping in `NoPTYBackend`.
- `termemu.NewWithMode(frontend, mode)` selects rune-per-cell or grapheme-cluster reading.
- `termemu.NewWithBackendMode(frontend, backend, mode)` combines backend swap with mode selection.
- `Terminal.StartCommand(*exec.Cmd)` runs a command within the PTY.
- `Terminal.Line(y)` and `Terminal.ANSILine(y)` read screen contents.
- `Terminal.Resize(w, h)` updates the PTY and internal screen size.

## Testing

```bash
go test ./...
```

## License

MIT. See `LICENSE`.
