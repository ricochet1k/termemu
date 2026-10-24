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
- Mouse reporting (X10/UTF-8/SGR/urxvt encodings)
- Kitty keyboard protocol mode parsing and key encoding support
- Synchronized output (`DECSET/DECRST 2026`) exposed as `VFSynchronizedOutput`
- OSC 8 hyperlinks tracked per span (`Span.Link`)
- OSC 52 clipboard write path via the optional `ClipboardSink` interface
- OSC 6/7 working-directory announcements mapped to `VSCurrentDirectory`
- 256-color palette and default fg/bg/cursor colors as terminal state
  (`OSC 4`/`10`/`11`/`12`, resettable via `104`/`110`–`112`, queryable by the
  child), resolved by frontends through `LockedView.PaletteColor` etc.
- Shell integration: `OSC 133` prompt marks (`ShellIntegration`) and
  `OSC 9`/`777` notifications (`NotificationSink`)
- Screen snapshots (`Snapshot`/`Restore`) for saving and replaying terminal state

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

func (loggingFrontend) Bell(v termemu.LockedView) {}
func (loggingFrontend) RegionChanged(v termemu.LockedView, r termemu.Region, _ termemu.ChangeReason) {
	fmt.Printf("region changed: (%d,%d)-(%d,%d)\n", r.X, r.Y, r.X2, r.Y2)
}
func (loggingFrontend) ScrollLines(v termemu.LockedView, delta int) {}
func (loggingFrontend) CursorMoved(v termemu.LockedView, x, y int) {}
func (loggingFrontend) StyleChanged(v termemu.LockedView, s termemu.Style) {}
func (loggingFrontend) ViewFlagChanged(v termemu.LockedView, flag termemu.ViewFlag, value bool) {}
func (loggingFrontend) ViewIntChanged(v termemu.LockedView, flag termemu.ViewInt, value int) {}
func (loggingFrontend) ViewStringChanged(v termemu.LockedView, flag termemu.ViewString, value string) {}

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

### Adopting an existing pty

If something else already opened the pty pair, hand it to the backend with
`Adopt` (or `AdoptMaster`). The backend takes ownership of the files it is
given: `Close` (also via `Terminal.Close`) closes them. Call `Adopt` before
constructing the Terminal or opening the backend.

```go
master, slave := existingFiles() // both are *os.File
backend := &termemu.PTYBackend{}
if err := backend.Adopt(master, slave); err != nil {
    // nil master, closed/already-open backend, or already has a pty
}
term := termemu.New(&termemu.EmptyFrontend{}, backend)

// Raw file descriptors can be wrapped as files first:
slave := os.NewFile(uintptr(fd), "pts")
_ = backend.Adopt(master, slave)
// Master only (no Attach; Read surfaces EOF/EIO instead of reuse):
_ = backend.AdoptMaster(master)
```

With `slave == nil` (from `AdoptMaster`), `Open` and `Attach` return an error
because there is no slave to attach a command to. `Read` then does a plain
master read and surfaces EOF/EIO so the read loop can exit; it does not wait
for reuse. `Write`, `SetSize`, `Close`, `PID`, and `Wait` behave as usual
(`PID` is 0 and `Wait` errors since no command was attached).

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

// Scrollback has its own snapshot/restore, separate from Terminal.Snapshot:
snap := sb.Snapshot()
if err := sb.Restore(snap); err != nil {
    // errors.Is(err, termemu.ErrScrollbackSnapshotInvalid)
}
```

Only scrolls starting at the top of the screen enter scrollback;
scrolling inside a mid-screen scroll region (CSI r margins) never does.
Returned rows own copies of their spans, so mutating them is safe.
ScrollIDs are monotonic across `Clear` (no ID is ever reused). Do not call
`Terminal.Scrollback` methods from inside a `Frontend` callback (deadlock:
callbacks already hold the terminal lock). Inside a callback use
`v.Scrollback()`, which returns the no-lock `LockedScrollback`
(`Range`, `Row`, `LogicalLine`, `MovePos`, `Len`). From a callback, use
`v.Snapshot()` and `v.Scrollback().Snapshot()` for deep copies;
`Terminal.Snapshot` and `*Scrollback.Snapshot` lock and would deadlock
there.

To avoid polling, implement the optional `termemu.ScrollbackListener`
extension on your frontend. It is called once per capture batch, with the
terminal lock held, and receives the newly appended `[first, next)` IDs:

```go
type myFrontend struct{ *termemu.EmptyFrontend }

func (f *myFrontend) ScrollbackRowsAppended(v termemu.LockedView, first, next termemu.ScrollID) {
    sb := v.Scrollback() // callback already holds the lock
    for id := first; id < next; id++ {
        if row, ok := sb.Row(id); ok {
            fmt.Println(row.PlainTextString())
        }
    }
}
```

Alternate-screen output never produces rows and `Clear()` does not call the
listener. Do not retain the view beyond the callback.

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

`Close`, `Done`, and `Err` do not take the terminal state lock, so they can be
called from a `Frontend` callback (which already holds it) without deadlocking.

`PTYBackend.Close()` (also called by `Terminal.Close()`) closes the pty pair;
later `Read`/`Write`/`Attach` fail with `io.ErrClosedPipe` instead of
reopening it. `Resize(w, h)` emits one full-screen `RegionChanged` with the
`CRResize` reason so frontends know to repaint at the new size. Shrinking the
height on the main screen pushes the lost top rows into scrollback and shifts
the surviving content up; resizing the alt screen only resizes it.

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

Locking: plain read methods (`Size`, `CursorPos`, `Line`, `ANSILine`,
`StyledLine`, `StyledLines`, `GetViewFlag/Int/String`) take the terminal lock
themselves, so they are safe from any goroutine. For atomic multi-reads use
`term.View(func(v termemu.LockedView) { ... })`. Every `Frontend` callback
also receives a `LockedView` as its first argument; read state through it
(including `v.Scrollback()`) instead of the locking methods, which would
deadlock from a callback.

```go
term.View(func(v termemu.LockedView) {
    w, h := v.Size()
    line := v.Line(0)
    _, _ = w, h, line
})
```

### Pause and process handoff

`Terminal.Pause(ctx)` parks the read loop and returns a `Handoff` containing
screen and scrollback snapshots, the terminal size, and the reader/parser
state (bytes already read but not applied, plus any partial escape command).
`Terminal.Resume()` continues a same-process paused loop.

To move a terminal to another process, the old process pauses, sends the
backend file descriptors, then closes/exits. The new process adopts the fds
and creates a terminal with `NewFromHandoff`, which restores everything before
starting the read loop:

```go
// Old process: pause, then send h and the fds (SCM_RIGHTS, cmd.ExtraFiles,
// or your IPC of choice).
h, err := term.Pause(ctx)
master, slave := backend.Master(), backend.Slave() // owned by backend
// ... transfer master/slave and h ...
_ = term.Close() // now safe to close/exit; Resume is no longer needed

// New process: wrap received fds and adopt them, then rebuild.
backend := &termemu.PTYBackend{}
if err := backend.Adopt(os.NewFile(masterFD, "ptmx"), os.NewFile(slaveFD, "pts")); err != nil {
    // handle
}
term, err := termemu.NewFromHandoff(frontend, backend, h)
```

`PTYBackend.Master()` and `Slave()` return the owned files (`Slave()` may be
nil after `AdoptMaster`). The sender may close its copies after transferring
them, because the descriptor is duplicated into the receiving process.
`NewFromHandoff` validates the backend and size; if the receiving terminal has
a different size, set `h.Size` to the target size before calling it. The
snapshot content is resized into that size. On a restore error it closes the
backend. Use `AdoptMaster` when only the master is available (no
`Attach`/reuse after that).

Pause interrupts a blocked read by setting a deadline through the optional
`SetReadDeadline` method (`PTYBackend`, `NoPTYBackend` when its reader supports
deadlines such as `*os.File`, and `TeeBackend`). A backend that does not
support deadlines returns `errors.ErrUnsupported`; `Pause` then waits for the
next byte or `ctx` cancellation. Always pass a context if pause must be
bounded.

```go
h, err := term.Pause(ctx) // leaves the loop paused
if err != nil { /* cancelled or loop already stopped */ }
if err := term.Resume(); err != nil { /* ... */ }
```

### Snapshots

`Snapshot()` deep-copies both screen buffers plus cursor, style, margins,
auto-wrap, wrapped flags, view flags/ints/strings, keyboard modes, and the
active OSC 8 link. The scrollback buffer and backend/process state are not
included. `Restore()` accepts snapshots of any size: it resizes the screens
(and the backend) to the snapshot dimensions, truncating or padding lines, and
emits a full-screen `RegionChanged` with `CRRestore` so frontends repaint.

From a `Frontend` callback or inside `Terminal.View`, use `v.Snapshot()` and
`v.Scrollback().Snapshot()` instead: they are the no-lock variants of
`Terminal.Snapshot()` and `Scrollback.Snapshot()`. `Restore` has no view
variant (the view is read-only).

```go
snap := term.Snapshot()
// ... run more output or otherwise mutate the screen ...
if err := term.Restore(snap); err != nil {
    // errors.Is(err, termemu.ErrSnapshotSizeMismatch) for an invalid snapshot
}
```

Restoring does not touch the scrollback buffer, and a snapshot stays
independent of the terminal after it is taken. Use `Scrollback.Snapshot()`
and `Scrollback.Restore()` separately when scrollback history must be saved;
they preserve row IDs and keep the ring's `Base`/`Next` monotonic.

New screens start with auto-wrap (DECAWM, `CSI ? 7`) enabled, matching xterm.

### Serialization

All public value types (`Style`, `Span`, `Line`, `Region`, `Pos`, `Row`,
`ScrollbackSnapshot`, `ScreenState`, and `Snapshot`) round-trip through both
`encoding/json` and `encoding/gob`. `Style` and the keyboard-mode state inside
`Snapshot` use custom JSON/gob encoders; `Style`'s JSON shape is
`{"fg":<uint32>,"bg":<uint32>,"underlineColor":<uint32>}`, where the numbers
are the packed internal representation (not palette indexes), not `{}`.

### Modern terminal features

- **Synchronized output:** `DECSET/DECRST 2026` sets/clears the
  `VFSynchronizedOutput` view flag. The core performs no batching itself;
  frontends may defer repaints while the flag is set.
- **Hyperlinks:** `OSC 8 ; params ; URI` starts a link, an empty URI ends
  it. Spans written while a link is active carry it in `Span.Link`
  (preserved through wrapping, scrollback, and line slicing; blank/erased
  spans never carry a link). The TTY frontend does not re-emit OSC 8.
- **Clipboard:** if the frontend implements
  `ClipboardSink` (`Clipboard(v LockedView, selection string, data []byte)`), OSC 52
  payloads are base64-decoded and forwarded with the requested selection.
  Queries (`data == "?"`) are never answered or forwarded, and malformed
  base64 is ignored. Called with the terminal lock held and a `LockedView`:
  read through the view, do not call locking terminal methods from
  `Clipboard`.
- **Working directory:** `OSC 7` (and legacy `OSC 6`) set
  `VSCurrentDirectory`. `OSC 7` no longer sets `VSCurrentFile`; no OSC
  sequence sets it.

## API highlights

- `NewWithMode(frontend, backend, mode, opts...)` — creates a terminal. Supports `WithScrollback(rows)`.
- `New(frontend, backend)` — shorthand for `NewWithMode(…, TextReadModeGrapheme)`.
- `NewNoPTYBackend(reader, writer)` — creates a backend from provided pipes.
- `PTYBackend.Attach(*exec.Cmd)` — wires a command into the PTY. Reusable.
- `PTYBackend.Adopt(master, slave)` / `AdoptMaster(master)` — adopt and own an existing pty pair (slave may be nil).
- `Terminal.Pause(ctx)` / `Terminal.Resume()` — park/capture and continue a
  terminal's read loop, including buffered bytes and a partial escape command.
- `NewFromHandoff(frontend, backend, handoff, opts...)` — restore a paused
  terminal in another process (after adopting the transferred backend fds).
- `PTYBackend.Master()/Slave()/SetReadDeadline()` — access handoff fds and
  interrupt a blocked read for pausing.
- `Terminal.Close()/Done()/Err()` — shut down the read loop and observe how it exited.
- `Terminal.Scrollback()` — returns the scrollback buffer.
- `Scrollback.Snapshot()/Restore(ScrollbackSnapshot)` — deep-copy and restore scrollback history (separate from terminal snapshots; callback-safe snapshot via `LockedScrollback.Snapshot()`).
- `Terminal.Snapshot()/Restore(Snapshot)` — deep-copy and restore terminal screen state (restores at the snapshot size; scrollback excluded; callback-safe snapshot via `LockedView.Snapshot()`).
- `PrintTerminal(t Terminal)` / `PrintTerminalTo(w, t)` — debug printing of screen contents.
- `NewTTYFrontend(out)` — terminal frontend that writes ANSI to output.

## Testing

```bash
go test ./...
```

### Differential testing against tmux

The opt-in `TestDifferentialTmux` starts a private tmux server (it does not
need to run inside tmux), renders a sequence in tmux, renders the same sequence
through termemu, feeds termemu's screen back through tmux for canonical
capture, and compares the screen and cursor. It needs `tmux` installed:

```bash
scripts/run_tmux_diff.sh
# or
TERMEMU_TMUX_DIFF=1 go test -run '^TestDifferentialTmux' -v .
```

The curated corpus compares styled screens and cursor positions; a seeded
random corpus compares visible text and cursor positions. The harness normalizes
both paths through tmux, including tab expansion and blank-line attributes.

## License

MIT. See `LICENSE`.
