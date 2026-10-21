# termemu

A small Go library for emulating a terminal and rendering output through a pluggable frontend. It runs commands inside a PTY (or a fallback pipe backend) and exposes screen state, colors, and view flags.

## Features

- PTY-backed command execution with screen emulation
- Pluggable `Frontend` interface for rendering or collecting output
- Reusable PTY backend — run multiple commands through the same PTY
- Scrollback buffer with stable row IDs and reflow-on-read
- Screen accessors for plain text, ANSI output, and styled lines
- `Color` value type with 256-color, bright, and RGB support; truecolor is
  advertised to children via `COLORTERM=truecolor` (DA1 has no truecolor
  parameter)
- Minimal backend abstraction for PTY or no-PTY environments
- Rune-per-cell or grapheme-cluster text tokenization
- Mouse reporting (X10/UTF-8/SGR encodings)
- Kitty keyboard protocol mode parsing and key encoding support
- Synchronized output (`DECSET/DECRST 2026`) exposed as `VFSynchronizedOutput`
- OSC 8 hyperlinks tracked per span (`Span.Link`)
- OSC 52 clipboard write path via the optional `ClipboardSink` interface
- OSC 6/7 working-directory announcements mapped to `VSCurrentDirectory`

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
	if err := backend.Attach(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "Attach error:", err)
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "cmd.Start error:", err)
		return
	}

	term := termemu.NewWithMode(&termemu.EmptyFrontend{}, backend, termemu.TextReadModeRune)
	if term == nil {
		fmt.Println("failed to create terminal")
		return
	}

	_ = cmd.Wait()

	// Dump the screen contents to stdout.
	termemu.PrintTerminal(term)
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

func (loggingFrontend) Bell() {}
func (loggingFrontend) RegionChanged(r termemu.Region, _ termemu.ChangeReason) {
	fmt.Printf("region changed: (%d,%d)-(%d,%d)\n", r.X, r.Y, r.X2, r.Y2)
}
func (loggingFrontend) ScrollLines(y int) {}
func (loggingFrontend) CursorMoved(x, y int) {}
func (loggingFrontend) StyleChanged(s termemu.Style) {}
func (loggingFrontend) ViewFlagChanged(v termemu.ViewFlag, v2 bool) {}
func (loggingFrontend) ViewIntChanged(v termemu.ViewInt, v2 int) {}
func (loggingFrontend) ViewStringChanged(v termemu.ViewString, v2 string) {}

func main() {
	backend := &termemu.PTYBackend{}
	cmd := exec.Command("sh", "-c", "printf '\\e[31mred\\e[0m' && echo")
	if err := backend.Attach(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "Attach error:", err)
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "cmd.Start error:", err)
		return
	}
	term := termemu.NewWithMode(loggingFrontend{}, backend, termemu.TextReadModeRune)
	_ = cmd.Wait()

	for y := 0; y < 1; y++ {
		fmt.Println(term.ANSILine(y))
	}
}
```

### Interactive terminal with TTY frontend

```go
package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"github.com/creack/termios/raw"
	"github.com/ricochet1k/termemu"
)

func main() {
	tios, err := raw.MakeRaw(os.Stdin.Fd())
	if err != nil {
		panic(err)
	}
	defer raw.TcSetAttr(os.Stdin.Fd(), tios)

	tty := termemu.NewTTYFrontend(os.Stdout)
	cmd := exec.Command("sh")
	backend := &termemu.PTYBackend{}
	backend.Attach(cmd)
	cmd.Start()
	term := termemu.NewWithMode(tty, backend, termemu.TextReadModeRune)

	resize := func() {
		ws, _ := pty.GetsizeFull(os.Stdout)
		w, h := int(ws.Cols), int(ws.Rows)
		term.Resize(w, h)
		tty.Attach(termemu.Region{X: 0, Y: 0, X2: w, Y2: h})
	}
	resize()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	inputCh := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				inputCh <- chunk
			}
			if err != nil {
				return
			}
		}
	}()

	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	for {
		select {
		case b := <-inputCh:
			if len(b) > 0 {
				term.Write(b)
			}
		case <-sigCh:
			resize()
		case <-doneCh:
			tty.Detach()
			return
		}
	}
}
```

### Reusable PTY (multiple commands)

```go
backend := &termemu.PTYBackend{}

cmd1 := exec.Command("sh", "-c", "echo first")
backend.Attach(cmd1)
cmd1.Start()
cmd1.Wait()

// Same PTY, same terminal state:
cmd2 := exec.Command("sh", "-c", "echo second")
backend.Attach(cmd2)
cmd2.Start()
```

### Scrollback

```go
sb := term.Scrollback()
base, next := sb.Range()
for id := base; id < next; id++ {
    row, ok := sb.Row(id)
    if ok {
        fmt.Println(row.PlainTextString())
    }
}

// Reflown at current terminal width:
logical := sb.LogicalLine(ScrollID(3))
for _, row := range logical {
    fmt.Println(row.PlainTextString())
}

// Row counts and clearing (CSI 3 J also clears; screen untouched):
fmt.Println(sb.Len())
sb.Clear()
```

Only scrolls starting at the top of the screen enter scrollback;
scrolling inside a mid-screen scroll region (CSI r margins) never does.
Returned rows own copies of their spans, so mutating them is safe.
ScrollIDs are monotonic across `Clear` (no ID is ever reused). Do not call
scrollback methods from inside a `Frontend` callback (deadlock: callbacks
already hold the terminal lock).

## Lifecycle

Terminals own a background read-loop goroutine. Shut them down explicitly:

```go
term := termemu.NewWithMode(&termemu.EmptyFrontend{}, backend, termemu.TextReadModeRune)
// ... run commands ...
_ = term.Close()  // idempotent; unblocks Read so the loop exits promptly
<-term.Done()     // closed when the read loop exits
if err := term.Err(); err != nil {
    // non-nil: the loop died on a backend error (io.EOF is clean, Err() == nil)
}
```

`PTYBackend.Close()` (also called by `Terminal.Close()`) closes the pty pair;
later `Read`/`Write`/`Attach` fail with `io.ErrClosedPipe` instead of
reopening it. `Resize(w, h)` emits one full-screen `RegionChanged` with the
`CRResize` reason so frontends know to repaint at the new size.

`PTYBackend.Attach()` injects `TERM=xterm-256color` and `COLORTERM=truecolor`
into the child environment by default. Truecolor is advertised via
`COLORTERM` because DA1 has no truecolor parameter. Set `PTYBackend.AutoEnv`
to override the list, or to an empty non-nil slice to disable injection:

```go
backend := &termemu.PTYBackend{AutoEnv: []string{"TERM=screen-256color"}}
backend := &termemu.PTYBackend{AutoEnv: []string{}} // inject nothing
```

Injected entries replace same-key values already present in `cmd.Env`;
other environment variables are left alone.

Locking: `Size`, `Line`, `ANSILine`, `StyledLine`, and `StyledLines` require
the caller to hold the terminal lock (`WithLock`); `Scrollback` methods take
it themselves and therefore must not be called from `Frontend` callbacks.

### Modern terminal features

- **Synchronized output:** `DECSET/DECRST 2026` sets/clears the
  `VFSynchronizedOutput` view flag. The core performs no batching itself;
  frontends may defer repaints while the flag is set.
- **Hyperlinks:** `OSC 8 ; params ; URI` starts a link, an empty URI ends
  it. Spans written while a link is active carry it in `Span.Link`
  (preserved through wrapping, scrollback, and line slicing; blank/erased
  spans never carry a link). The TTY frontend does not re-emit OSC 8.
- **Clipboard:** if the frontend implements
  `ClipboardSink` (`Clipboard(selection string, data []byte)`), OSC 52
  payloads are base64-decoded and forwarded with the requested selection.
  Queries (`data == "?"`) are never answered or forwarded, and malformed
  base64 is ignored. Called with the terminal lock held: do not call back
  into the terminal from `Clipboard`.
- **Working directory:** `OSC 7` (and legacy `OSC 6`) set
  `VSCurrentDirectory`. `OSC 7` no longer sets `VSCurrentFile`; no OSC
  sequence sets it.

## API highlights

- `NewWithMode(frontend, backend, mode, opts...)` — creates a terminal. Supports `WithScrollback(rows)`.
- `New(frontend, backend)` — shorthand for `NewWithMode(…, TextReadModeRune)`.
- `NewNoPTYBackend(reader, writer)` — creates a backend from provided pipes.
- `PTYBackend.Attach(*exec.Cmd)` — wires a command into the PTY. Reusable.
- `Terminal.Close()/Done()/Err()` — shut down the read loop and observe how it exited.
- `Terminal.Scrollback()` — returns the scrollback buffer.
- `PrintTerminal(t Terminal)` / `PrintTerminalTo(w, t)` — debug printing of screen contents.
- `NewTTYFrontend(out)` — terminal frontend that writes ANSI to output.

## Testing

```bash
go test ./...
```

## License

MIT. See `LICENSE`.
