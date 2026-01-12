# termemu

A small Go library for emulating a terminal and rendering output through a pluggable frontend. It runs commands inside a PTY (or a fallback pipe backend) and exposes screen state, colors, and view flags.

## Features

- PTY-backed command execution with screen emulation
- Pluggable `Frontend` interface for rendering or collecting output
- Screen accessors for plain text, ANSI output, and styled lines
- Minimal backend abstraction for PTY or no-PTY environments
- Rune-per-cell or grapheme-cluster text tokenization
- Mouse reporting (X10/UTF-8/SGR encodings)
- Kitty keyboard protocol mode parsing and key encoding support

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
	backend := &termemu.PTYBackend{}
	cmd := exec.Command("sh", "-c", "printf 'Hello World'")
	if err := backend.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error:", err)
		return
	}

	term := termemu.NewWithMode(&termemu.EmptyFrontend{}, backend, termemu.TextReadModeRune)
	if term == nil {
		fmt.Println("failed to create terminal")
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
	backend := &termemu.PTYBackend{}
	cmd := exec.Command("sh", "-c", "printf '\\e[31mred\\e[0m' && echo")
	if err := backend.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error:", err)
		return
	}
	term := termemu.NewWithMode(loggingFrontend{}, backend, termemu.TextReadModeRune)
	_ = cmd.Wait()

	for y := 0; y < 1; y++ {
		fmt.Println(term.ANSILine(y))
	}
}
```

## API highlights

- `termemu.NewWithMode(frontend, backend, mode)` creates a terminal with the provided backend.
- `termemu.NewNoPTYBackend(reader, writer)` creates a backend from provided pipes.
- `PTYBackend.StartCommand(*exec.Cmd)` runs a command within a PTY backend.
- `Terminal.Line(y)` and `Terminal.ANSILine(y)` read screen contents.
- `Terminal.Resize(w, h)` updates the PTY and internal screen size.

## Testing

```bash
go test ./...
```

## License

MIT. See `LICENSE`.
