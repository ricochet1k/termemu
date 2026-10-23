# AGENTS

This file provides build/test commands and code style guidance for agents working in this repo.
Follow these instructions unless the user gives higher-priority directions.
Keep this file up-to-date. If you have to search for something, add it to the Repository layout. If the layout is out of date, fix it. If I have to issue corrections, record them here in AGENTS.md.

## Quick facts
- Module: `github.com/ricochet1k/termemu`
- Go version: 1.24+ (see `go.mod`)
- Primary language: Go
- `PTYBackend.Attach` injects `TERM=xterm-256color` and
  `COLORTERM=truecolor` by default. Truecolor is advertised via `COLORTERM`
  because DA1 has no truecolor parameter; the emulator itself renders SGR
  38;2/48;2 RGB. `PTYBackend.AutoEnv` overrides the injected list (empty
  non-nil slice disables it); injected keys replace same-key entries in
  `cmd.Env`.

## Repository layout
- Core library code lives at repo root (package `termemu`). This flat
  layout is intentional and idiomatic for a single-package Go library.
- Examples live under `examples/`: `simple`, `tty`, `unicode_test`,
  `esctest` (interactive escape debugger), and `tmux` (tmux comparison
  harness).
- Reference docs live under `docs/` (e.g. `keyboard-protocol.rst`).
- Helper scripts live under `scripts/`; generated test artifacts
  (`scripts/tmux_integration_test`, `scripts/integration_test_results.txt`)
  are gitignored.
- Test helpers and benchmarks live in `_test.go` files at root.
- `backend.go` holds `Backend`, `PTYBackend`, `NoPTYBackend`, `TeeBackend`.
  `backend_unix.go` / `backend_other.go` hold `setControllingTTY` (the only
  platform-specific child-session setup).
- `terminal.go` (coordinator + scrollback wiring), `handoff.go` (Pause/
  Resume/NewFromHandoff and reader state), `screen.go` (the only screen
  implementation), `scrollback.go` (scrollback ring buffer + reflow),
  `escapes.go` (ANSI parser), `grapheme_reader.go` (grapheme tokenizer),
  `style.go` (colors/modes), `keys.go` (input encoding), `tty_frontend.go`
  (ANSI renderer frontend).
- `serialize_test.go` covers JSON/gob round-trips for the public value types.

## Serialization
All public value types (`Style`, `Span`, `Line`, `Region`, `Pos`, `Row`,
`ScrollbackSnapshot`, `ScreenState`, `Snapshot`, `ReaderState`, `Handoff`)
round-trip through
`encoding/json` and `encoding/gob`. `Style` and `keyboardMode` implement
custom `MarshalJSON`/`UnmarshalJSON` and `GobEncode`/`GobDecode` over exported
shadow structs, so `Snapshot.KeyboardMain/KeyboardAlt` serialize even though
their type is unexported. `Style` encodes its three packed uint32 components
(`fg`, `bg`, `underlineColor`) losslessly; those numbers are the internal
representation, not palette indexes. Keep the in-memory layout unchanged when
touching these types, and extend `serialize_test.go` when adding new public
value types or fields.

## Pause and process handoff
`Terminal.Pause(ctx)` parks the read loop, snapshots the reader/parser state
(`ReaderState`), the screen (`Snapshot`), and scrollback, and returns a
`Handoff`. `Resume()` continues the same-process loop. A new process adopts the
backend fds (`PTYBackend.Master()`/`Slave()` transferred with SCM_RIGHTS or
`cmd.ExtraFiles`), wraps them with `Adopt`/`AdoptMaster`, and calls
`NewFromHandoff`, which creates the terminal without reading, resizes/restores
snapshot + scrollback, then starts the loop from `ReaderState` (buffered bytes
and partial ESC command included). `Handoff.Size` is the target size: if the
receiving window differs, set it before calling `NewFromHandoff` and the
snapshot content is resized to fit. The old process pauses, sends the fds, then
closes/exits; the sender may close its copies after transfer because the fd is
duplicated into the receiver.

`GraphemeReader` buffers, so `ptyReadLoop` passes the backend directly (no
`bufio.Reader` read-ahead). `GraphemeState` / `GraphemeReader.SnapshotState` /
`NewGraphemeReaderFromState` preserve the tokenizer buffer and state.
`readEscapeCommand(r, partial)` replays a partial command before reading more
and returns consumed bytes on error. `readLoopState` holds the reader plus
`pendingCmd`; `ptyReadLoop` starts from a `ReaderState` and continues a
pending command before normal reads.

Pause sets a read deadline through the optional `SetReadDeadline` method so a
blocked read wakes immediately; the loop recognizes `os.ErrDeadlineExceeded`
while paused and parks instead of exiting. `PTYBackend`, `NoPTYBackend` (when
its reader supports deadlines), and `TeeBackend` implement it. A backend that
returns `errors.ErrUnsupported` cannot interrupt a blocked read, so `Pause`
waits for the next byte or ctx cancellation. `Close` wakes a paused loop with
an abort signal before closing the backend so `Done()` still closes cleanly.

## Build, lint, and test commands
Use these exact commands unless the user asks otherwise.

### Build
- Build all packages: `go build ./...`
- Build a specific package: `go build ./path/to/pkg`
- Build examples:
  - `go build ./examples/simple`
  - `go build ./examples/tty`
  - `go build ./examples/unicode_test`
  - `go build ./examples/esctest`
  - `go build ./examples/tmux`

### Test
- Run all tests: `go test ./...`
- Run tests for a package: `go test ./path/to/pkg`
- Run a single test (exact name): `go test ./path/to/pkg -run '^TestName$'`
- Run a single subtest: `go test ./path/to/pkg -run '^TestName$/SubtestName$'`
- Run all tests matching a prefix: `go test ./path/to/pkg -run '^TestName'`
- Run with verbose output: `go test -v ./path/to/pkg`

### Benchmarks
- Run all benchmarks: `go test ./... -run '^$' -bench .`
- Run a single benchmark: `go test ./path/to/pkg -run '^$' -bench '^BenchmarkName$'`

### Lint / static checks
CI (`.github/workflows/`) runs `gofmt -l .`, `go vet ./...`, `golangci-lint`
and `go test -race ./...` on Linux, macOS and Windows. Locally use:
- `gofmt -l .` (must print nothing) and `gofmt -w <files>`
- `go vet ./...`
- `GOOS=windows go build ./...` (the library must at least compile for
  Windows even though `creack/pty` returns `ErrUnsupported` there at runtime)

## Code style guidelines
Follow standard Go conventions and the patterns in this repo.
Keep changes minimal and consistent with existing code.

### Formatting
- Always `gofmt` any Go files you edit.
- Tabs are used for indentation (gofmt handles this).
- Keep line lengths reasonable; avoid complex inline expressions.

### Imports
- Use standard `gofmt` import grouping.
- No extra blank lines between stdlib imports in this repo.
- Prefer explicit imports; avoid dot-imports.

### Naming conventions
- Use Go-style CamelCase for exported identifiers.
- Use lowerCamelCase for unexported identifiers.
- Keep names descriptive and avoid single-letter names unless idiomatic (e.g., loop indices).
- Keep const/var names short but meaningful (e.g., `termStr`, `viewFlags`).

### Types and zero values
- Prefer zero-value-friendly structs.
- Keep struct fields grouped logically (public API first, internal state after).
- Use slices/maps with explicit `make` sizes when counts are known.

### Error handling
- Return errors early; avoid nested conditionals.
- Use `errors.New` for static errors and `fmt.Errorf` for formatted messages.
- Preserve underlying errors when relevant (use `%w`).
- Don’t swallow errors unless intentionally ignored and documented in code.

### Concurrency and locking
- Terminal state uses a mutex; lock/unlock around shared state access.
- Plain terminal read methods (`Size`, `CursorPos`, `Line`, `ANSILine`,
  `StyledLine`, `StyledLines`, `GetViewFlag/Int/String`) lock themselves and
  are safe from any goroutine.
- Use `Terminal.View(func(LockedView) { ... })` for atomic multi-reads. The
  view is valid only inside the call; do not retain it.
- Avoid launching goroutines that outlive their owners unless required.

#### Data race prevention (critical!)
The terminal has a background read loop goroutine (`ptyReadLoop`) that continuously writes to screen state.
Tests and applications may read state concurrently because the plain read
methods take the lock themselves.

**Why `ptyReadOne` can't hold the lock for its entire duration:**
The function does blocking I/O reads (`ReadPrintableBytes`, `ReadByte`, `handleCommand`).
Holding the lock during blocking reads would deadlock if anyone tries to access the terminal.
Instead, it acquires the lock around each state mutation, then releases before the next read.

**Frontend callback contract:**
All `Frontend` interface methods are called with the terminal lock already held
and receive a `LockedView` as their first argument. Read terminal state through
that view (`Size`, `Line`, `Scrollback`, ...); its methods do not lock. See
godoc on `Frontend` interface in `frontend.go`.
Never call the locking read methods (plain `Size`/`Line`/... or the
`Terminal.Scrollback` reader) from a callback or `View`; that would deadlock.

**Reading scrollback:**
- Ordinary code uses `term.Scrollback()`; its methods (`Range`, `Row`,
  `LogicalLine`, `MovePos`, `Len`, `Clear`) take the lock themselves.
- Callbacks use `v.Scrollback()`, which returns the no-lock
  `LockedScrollback` counterpart (`Range`, `Row`, `LogicalLine`, `MovePos`,
  `Len`). Use it only for the duration of the callback.

The `PrintTerminal(t Terminal)` debug helper uses `View` itself.

**How to read in tests:**
```go
// Plain single reads lock internally:
line := term.Line(0)

// Atomic multi-read: use View.
var line string
var w, h int
term.View(func(v LockedView) {
    line = v.Line(0)
    w, h = v.Size()
})
```

**When adding new terminal read methods:**
1. Add a matching `LockedView` method that does not lock, and
2. Make the plain `Terminal` method lock and delegate to the view.

Run `go test -race ./...` to detect races before merging.

### Functions and methods
- Keep functions focused; split large functions where clear boundaries exist.
- Prefer early returns for guard conditions.
- Maintain method receiver names consistent with existing files (`t` for terminal, etc.).

### Packages and exports
- Public APIs should stay stable; avoid breaking changes unless required.
- Add GoDoc comments for new exported types/functions.
- Keep example code in `examples/` minimal and compilable.

### Tests
- Name tests `TestXxx` and subtests with clear, concise labels.
- Use table-driven tests for sets of related cases.
- Prefer helper functions in `_test.go` files to reduce duplication.
- Keep tests deterministic (no timing-dependent flakes).

## Working with examples
- Examples compile as separate `main` packages.
- Run with `go run ./examples/<name>` to validate behavior.

## Documentation updates
- Update `README.md` only when public usage changes.
- Avoid adding new docs unless requested by the user.

## Agent tips
- Prefer edits that maintain existing API shape.
- Keep changes localized to the smallest necessary files.
- Avoid adding new dependencies unless explicitly requested.
- The codebase uses extensive bit-packing for performance (Style, Mode enums)
- Span-based rendering (`spanScreen`) is the only screen implementation; it
  optimizes memory and allocations for large screens
- Wide characters (CJK, emoji) need special handling (splitSpan)
- Text merging (combining characters) requires careful cursor position handling
- Frontend callbacks happen with lock held and get a `LockedView`; keep them fast
- PTY operations are platform-specific (uses creack/pty); cross-compile to
  Windows after backend changes because `syscall.SysProcAttr` fields differ
- When optimizing, focus on hot paths: writeString, replaceRange, eraseRegion, scroll

## Overview

termemu is a Go library for terminal emulation. It runs commands inside a PTY (or fallback pipe backend), parses ANSI escape sequences, and exposes screen state through a pluggable Frontend interface. The library handles Unicode grapheme clusters, wide characters, SGR color modes, mouse reporting, and the Kitty keyboard protocol.

## Architecture

### Core Components

**Terminal → Screen → Frontend flow:**
1. `Terminal` is the main entry point that manages PTY I/O, holds dual screens (main/alt), and coordinates locking
2. `screen` interface (implemented by `spanScreen`) maintains the character grid, cursor position, and styles
3. `Frontend` interface receives callbacks for rendering events (region changes, cursor moves, style changes)

### Key Abstractions

**Backend** (backend.go): Provides I/O abstraction
- `PTYBackend`: Real PTY using github.com/creack/pty. One pty pair is opened
  lazily and shared by every command, so commands can be run back-to-back on the
  same terminal. The master survives child exits; on Darwin a child that was a
  session leader with the pty as its controlling terminal revokes the parent's
  slave descriptors when it exits, so every `Attach` after the first reopens
  the slave by path via `openSlave()` (`backend_unix.go`). `Attach(cmd)` wires
  stdio + `SysProcAttr`; the caller runs `cmd.Start()`. `Read` treats `EIO` (current child exited) as "wait for the
  next attached command" instead of EOF, which is what makes reuse work.
  `Close()` closes master+slave, wakes blocked `Read`s with
  `io.ErrClosedPipe`, and stops the EIO retry; later `Read`/`Write`/`Attach`
  fail instead of reopening the pair. Treats both `syscall.EIO` (Linux) and
  `io.EOF` (Darwin) from the master as "current child exited", waiting for
  the next `Attach`; `NoPTYBackend` EOF stays terminal. `PID()` and `Wait()`
  expose the most recently attached command (never also call `cmd.Wait()`).
  `Adopt(master, slave)` / `AdoptMaster(master)` take ownership of an existing
  pty pair before use; `slave` may be nil (then `Open`/`Attach` error and
  `Read` surfaces EOF/EIO instead of waiting for reuse). Use
  `os.NewFile(fd, "pts")` for raw fds. Adopt rejects a nil master, a closed
  backend, or one that already has a pty.
- `NoPTYBackend`: Pipes for non-PTY environments (EOF is terminal; still
  ends the read loop cleanly with `Err() == nil`)
- `TeeBackend`: Wraps a backend to duplicate reads (`Close` forwards)

**Terminal** (terminal.go): Main coordinator
- Owns main and alternate screens
- Runs `ptyReadLoop()` goroutine to read from backend
- Manages view flags/ints/strings (cursor visibility, mouse mode, window title, etc.)
- Provides self-locking read methods plus `View(func(LockedView))` for atomic reads
- `Snapshot()` deep-copies both screens plus view flags/ints/strings, keyboard
  modes, and the active OSC 8 link; it excludes scrollback and backend state
- `Restore(Snapshot)` replaces that state, resizing both screens and the
  backend to the snapshot dimensions (lines are truncated/padded), emits one
  full-screen `RegionChanged` with `CRRestore` plus cursor/style callbacks,
  and returns `ErrSnapshotSizeMismatch` only for invalid screen dimensions.
  Scrollback is untouched by both methods.
- Callback-safe variants: `LockedView.Snapshot()` and
  `LockedScrollback.Snapshot()` do not lock (zero views return empty
  snapshots). `Terminal.Snapshot` / `*Scrollback.Snapshot` lock and must not be
  called from a Frontend callback or `View`. There is no view Restore
  variant; the view is read-only.
- Handles keyboard input via `SendKey()`
- Lifecycle: `Close()` closes the backend so the read loop exits (idempotent,
  never blocks and never takes the terminal lock); `Done()` closes on loop
  exit; `Err()` reports the loop error (nil for clean EOF) without taking the
  terminal lock. Loop/pause/close state lives under `lifecycleMu`, so
  `Close`/`Err` are safe inside callbacks. `Resize()` emits one full-screen `RegionChanged` with
  `CRResize` so frontends repaint at the new size. A height shrink on the main
  screen pushes the top `oldH-newH` rows into scrollback (without extra scroll
  callbacks) and shifts remaining content up; the alt screen only resizes and
  never captures. Growing height does not pull rows back out of scrollback.

**Screen** (screen.go): Character grid management
- `spanScreen` is the only implementation (the old cell-based `gridScreen` was
  deleted)
- `spanScreen` represents the screen as a list of `spanLine`s, each containing `[]Span`
- Spans optimize storage: `Span{Width: 80}` (empty Text) for 80 spaces vs storing 80 cells
- A parallel `wrapped []bool` marks rows that continue the previous row due to
  auto-wrap; it feeds scrollback logical lines and is cleared by explicit cursor
  positioning / full-width erase
- `setSize` keeps every line exactly the new width (a wide cluster dropped by
  truncation is padded back), clamps the cursor/saved cursor and scroll margins
  into the new bounds. `scroll` clamps `|dy|` to the region height and ignores
  invalid margins (`top > bottom`).
- Handles complex operations: scrolling, wide character overlaps, grapheme cluster splitting
- Screen size, cursor position, scroll margins, auto-wrap behavior. Auto-wrap
  (DECAWM) defaults to on, as in xterm. Writes use deferred wrap: filling the
  last cell leaves the cursor there with `pendingWrap` set; the next printable
  write (or one whose cell would not fit) wraps first via `wrapLine()`. CR, LF,
  VT, FF, BS, TAB, explicit cursor moves, DECSC/DECRC, setSize, RIS/DECSTR, and
  DECALN clear or restore `pendingWrap`; DECAWM off clamps and never sets it.
- `snapshotState()`/`restoreState()` implement the internal `screen` hooks used
  by `Terminal.Snapshot`/`Restore`: they deep-copy/rebuild `spanLine`s
  (including zero-width spans and links), the `wrapped` flags, cursor, pending
  wrap, saved cursor, style, margins, and auto-wrap. `restoreState` emits no
  callbacks; the terminal owns the repaint notifications.
- `setScrollCapture` installs a callback invoked (with the terminal lock held)
  before rows scroll off the top, passing the lost rows and their wrapped flags

**Escape Sequence Parsing** (escapes.go):
- `ptyReadLoop(ReaderState)` reads from backend via `GraphemeReader`; it starts
  from captured state and first continues `pendingCmd` when present
- `ptyReadOne(readLoopState)` buffers one complete ESC command with
  `readEscapeCommand` *before* taking the terminal lock, then dispatches
  `handleCommand` from an in-memory `bytes.Reader`. A child that stops
  mid-sequence therefore never blocks `Snapshot`/`Restore` (or any other lock
  user) on backend I/O. On an ESC read error the bytes consumed so far are
  stored in `pendingCmd` for pause/handoff resume.
- Distinguishes printable text runs from control codes
- Parses CSI sequences (cursor movement, SGR colors, screen manipulation)
- Parses OSC sequences: 0/2 window title, 6/7 working directory
  (`VSCurrentDirectory`; OSC 7 no longer sets `VSCurrentFile`), 8 hyperlinks
  (tracked URI pushed to both screens via internal `setLink`), 52 clipboard
  writes (base64-decoded, forwarded to `ClipboardSink`; queries dropped)
- `DECSET/DECRST 2026` sets/clears `VFSynchronizedOutput`
- Parses keyboard protocol mode switches

**Style System** (style.go):
- `Style` struct uses bit-packing: 3 uint32 fields (fg, bg, underlineColor)
- Each uint32: bit 31 = color type (256-color vs RGB), bits 24-30 = mode bits, bits 0-23 = color data
- Modes: Bold, Dim, Italic, Underline, Blink, Reverse, Invisible, Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink
- `ANSIEscape()` generates SGR sequences for rendering
- `ANSIEscapeFrom(prev)` emits minimal diffs; a component returning to
  default emits `SGR 39` (fg) / `SGR 49` (bg) unless a mode change already
  forced a full reset

**Grapheme Handling** (grapheme_reader.go):
- `GraphemeReader` wraps an io.Reader and tokenizes text
- Two modes: `TextReadModeRune` (1 rune = 1 token) or `TextReadModeGrapheme` (1 grapheme cluster = 1 token)
- Handles combining characters, zero-width joiners, emoji sequences
- Returns merge tokens for combining chars that should merge into previous cell
- Cluster widths are measured standalone (`standaloneGraphemeWidth`) when a
  cluster starts after a read boundary or a regional indicator. uniseg's
  carried property otherwise makes the next width depend on the previous
  cluster (e.g. `e\u0301` then `x`, or a lone regional indicator read
  byte-at-a-time). Within a contiguous buffer its lookahead state is safe
- Printable ASCII uses a fast path that skips uniseg (the common case); a
  one-cell ASCII base followed by VS16/keycap/skin-tone is coalesced and
  widened like the streaming merge path
- Clusters ending in ZWJ set `ForceMergeNext`; regional indicators use
  `LastWasRI`; VS16/keycap/skin-tone modifiers widen a one-cell base to two
- `GraphemeState` + `SnapshotState`/`NewGraphemeReaderFromState` capture and
  restore the buffer and tokenizer state for pause/handoff

**Line Representation** (line.go):
- `Line` holds `[]Span` for external API access
- `Span` is either text (`Text` + `Width`) or a blank run (empty `Text`,
  `Width` spaces; `Width == 0` is empty)
- `Span.Link` holds the OSC 8 hyperlink URI active when the span was written
  (empty = no link); always check it alongside fg/bg when comparing spans,
  and never set it on blank/erased spans
- `spanLine` is internal representation with width tracking
- Functions: `splitSpan()`, `replaceRange()`, `truncateLine()`, `mergeAdjacent()` handle complex span manipulation
- `replaceRange()` must not merge/coalesce adjacent spans with different `Link` values

### Scrollback (scrollback.go)

- `Terminal.Scrollback()` returns a `*Scrollback` reader; capacity is set with
  `NewWithMode(..., WithScrollback(rows))` (default `DefaultScrollbackRows`).
- Rows that scroll off the top of the main screen are captured by
  `terminal.scrollCapture` (alt-screen output is never captured).
  Only scrolls whose region starts at the top of the screen (`y1 == 0`,
  checked in `spanScreen.scroll`) feed scrollback; mid-screen scroll
  regions (CSI r margins, IL/DL) recycle lines in place and must not capture.
- Storage is a ring buffer of physical rows. Each row keeps a monotonic
  `ScrollID` that stays stable as rows are added and evicted; `Range()` reports
  the valid IDs.
- `LogicalLine(id)` walks backward/forward over `wrapped` rows to rebuild the
  logical line and reflows it at the terminal's current width. Trailing blank
  padding on the final physical row is dropped before reflow, so a 20-column
  line of 50 cells returns rows of width 20/20/10. Each returned row's `ID` is
  the stored raw row containing its first cell: reflown rows that split one
  stored row share its ID, rows straddling a boundary report the row holding
  the first cell, and IDs are non-zero and non-decreasing but may repeat
  (use `Row(id)` to fetch the stored row). The no-reflow fast path reports the
  stored IDs unchanged.
- `Scrollback.Snapshot()`/`Restore()` deep-copy and replace the ring contents
  independently of `Terminal.Snapshot`/`Restore` (which exclude scrollback).
  Restore preserves `Base`/`Next` IDs, keeps only the newest rows when the
  snapshot exceeds capacity, and returns `ErrScrollbackSnapshotInvalid` for
  malformed input. The no-lock `LockedScrollback.Snapshot()` is the
  callback-safe snapshot entry point.
- `MovePos` moves by *stored* physical rows, not reflown rows.
- `Row`/`LogicalLine` return copies of the stored span slices (public API
  hands out copies; internal hot paths never copy).
- `Len()` reports the stored row count; `Clear()` drops all rows while
  keeping `ScrollID`s monotonic (`Range` then reports `base == next ==
  nextScrollID`). `CSI 3 J` clears scrollback only, leaving the visible
  screen and cursor untouched. `CSI 0J/1J/2J` behavior is unchanged.
- `Scrollback` methods lock internally, so never call them from a `Frontend`
  callback (lock already held there — deadlock). Use `v.Scrollback()`, the
  `LockedScrollback` variant, inside callbacks instead.
- `Row.PlainTextString()` is the convenience accessor (thin wrapper over
  `Line.PlainTextString`), keep it in `scrollback.go`, not in a test file.
- Scrollback `Row`/`LogicalLine` copies preserve `Span.Link` (checked by
  `TestOSC8_LinkPreservedInScrollback`).
- Reading requires the terminal lock, so all `*Scrollback` methods take it
  themselves; `LockedScrollback` methods assume it is already held.
- Frontends that do not want to poll `Len()`/`Range()` can implement the
  optional `ScrollbackListener` extension. `scrollCapture` calls
  `ScrollbackRowsAppended(v, first, next)` once per capture batch, with the
  terminal lock held and IDs `[first, next)` matching the rows just appended.
  Read them through `v.Scrollback()`; do not call the locking `*Scrollback`
  methods. Alt-screen output and `Clear()` never fire it.

  ```go
  type myFrontend struct{ *termemu.EmptyFrontend }

  func (f *myFrontend) ScrollbackRowsAppended(v termemu.LockedView, first, next termemu.ScrollID) {
      sb := v.Scrollback() // no-lock view; callback already holds the lock
      for id := first; id < next; id++ {
          row, ok := sb.Row(id)
          if ok {
              _ = row.PlainTextString()
          }
      }
  }
  ```

### Dual Screen Model

Terminal maintains two screens:
- `mainScreen`: Normal screen buffer
- `altScreen`: Alternate screen (used by vim, less, etc.)
- `onAltScreen` flag controls which is active
- Switching screens triggers `CRScreenSwitch` change reason

### Frontend Interface

Frontends receive callbacks:
- `RegionChanged(Region, ChangeReason)`: Area of screen changed (text, clear, scroll, screen switch)
- `CursorMoved(x, y)`: Cursor position changed
- `StyleChanged(Style)`: Current style changed
- `ViewFlagChanged/ViewIntChanged/ViewStringChanged`: Terminal mode changed
  (includes `VFSynchronizedOutput` for `DECSET/DECRST 2026`; frontends may
  batch repaints while set — the core does no batching)
- `ScrollLines(delta)`: signed main-screen scroll delta (negative = content
  moved up / rows left the top, positive = content moved down). Emitted for
  every scroll, including mid-screen regions; alt-screen scrolls do not emit
  it. Scrollback capture is separate.
- Optional `ClipboardSink.Clipboard(selection, data)`: OSC 52 write path;
  only called if the frontend implements it (queries never forwarded)
- Optional `ScrollbackListener.ScrollbackRowsAppended(v, first, next)`: one
  call per scrollback capture batch, with the lock held; IDs are the newly
  appended range. Read rows via `v.Scrollback()`; not fired for alt-screen
  output or `Clear()`
- `Bell()`: Bell character received

`EmptyFrontend` is a no-op implementation for when no UI callbacks are needed.

### Text Rendering Modes

The library supports two text handling modes:
1. **Rune mode**: Each Unicode code point is one cell (simple, fast)
2. **Grapheme mode**: Each grapheme cluster is one cell (proper Unicode, handles combining chars)

Choose via `NewWithMode(frontend, backend, TextReadModeRune/Grapheme)`

### TTY Frontend

`NewTTYFrontend(out)` needs no terminal argument: `NewWithMode` detects the
`SetTerminal` interface and wires the terminal automatically. Callers use
`Attach(Region)` before rendering and `Detach()` when done (see
`examples/tty`).

## Testing Patterns

**Unit Tests:**
- Use `MakeTerminalWithMock()` or `MakeScreenWithMock()` to get terminal/screen with `MockFrontend`
- `MockFrontend` records all callbacks (regions, cursor moves, styles)
- Check expectations against recorded calls

**Integration Tests (integration_test.go, `//go:build unix`):**
- `TestIntegration_RunPrintf` starts a real child under a `PTYBackend`, so a
  broken `Attach`/`SysProcAttr` setup fails the test
- `TestIntegration_ReusePTY` runs two commands through one backend
- `TestIntegration_BuildTerminalBeforeAttach` covers constructing the Terminal
  before the pty exists (the backend opens lazily)
- Output arrives asynchronously: poll with `waitForLine` instead of sleeping
- `TestIntegration_WritesToSlave` covers the read loop without a child process
- Always run these with `-race`

**Table-Driven Tests:**
- See escapes_test.go and screen_test.go for examples
- Test cases specify input and expected screen state

**Generative / property tests:**
- `screen_invariants_test.go` has a deterministic random-input test and
  `FuzzTerminalInput`. Both feed arbitrary text/escape streams and assert
  `checkScreenInvariants` (full-width lines, span widths, cursor and margins in
  bounds), then round-trip Snapshot/Restore. Keep new screen features inside
  those invariants.
- `fuzz_extra_test.go` adds `FuzzTerminalInputChunking` (whole vs 1/3/7-byte
  reads must reach the same semantic screen state), `FuzzReplaceRangeInvariants`
  (span width/UTF-8 invariants under arbitrary text and offsets), and
  `FuzzLineEquivalentPacking` (repacking cells must not change `Line.Equivalent`).
- Corpus seeds under `testdata/fuzz/<Target>/` are regression tests; keep them
  when a fuzz failure is fixed.
- Failing fuzz inputs are kept under `testdata/fuzz/FuzzTerminalInput/` and run
  as seeds by a normal `go test`. Run a longer session with
  `go test -run '^$' -fuzz '^FuzzTerminalInput$' -fuzztime 30s .`.
- `scrollback_property_test.go` checks the ring buffer against a model under
  random append/evict/clear/snapshot operations.
- `keys_coverage_test.go` enforces that every `KeyCode` encodes in legacy and
  Kitty modes, and property-checks the functional-code and keypad tables.

## Common Development Tasks

### Adding a New Escape Sequence

1. Add parsing in `ptyReadLoop()` (escapes.go) switch statement
2. Implement behavior (usually call screen methods)
3. Add test case in escapes_test.go
4. Update integration_failures_test.go if testing specific sequences

### Adding a New Style Mode

1. Add constant to `Mode` type in style.go
2. Add to `modeToSGRCode` map for ANSI output
3. Update `colorModes` array if it's SGR 1-8
4. Add parsing in CSI 'm' handler in escapes.go
5. Add test in style_test.go (if exists) or screen_test.go

### Working with Spans

Spans optimize memory and rendering:
- Use empty `Text` + `Width` for blank runs; use `Text + Width` for actual text content
- `replaceRange()` is the core function for modifying span lines
- Always respect grapheme cluster boundaries (don't split wide chars)

### Screen Grid Operations

Key functions in screen.go:
- `writeString()`: Write text at cursor, advance cursor
- `rawWriteSpan()`: Write span at specific position
- `eraseRegion()`: Clear rectangular area
- `scroll()`: Scroll region vertically
- `deleteChars()`: Delete characters, shift remaining left

## Screen Implementation Notes

`spanScreen` (screen.go) is the only `screen` implementation and `newScreen()`
returns it directly. When changing the `screen` interface you only need to
update `spanScreen`.

`scrollCapture` is how scrollback stays decoupled from the screen: `terminal`
wires it in `newTerminal`, and `spanScreen.scroll()` invokes it before overwriting
lost rows. Keep the capture path allocation-light but correct: rows are copied
because the screen reuses its `spanLine` storage.

`setLink` is the other screen-internal hook: `terminal.setHyperlink` (driven by
OSC 8) pushes the URI to both screens; `writeString`/`rawWriteRune` stamp it
onto content spans while erase/scroll/resize blanks stay link-free. `resetTerminal`
(RIS) ends any active link.

Use benchmarks in `screen_bench_test.go` to catch regressions.

## Dependencies

- `github.com/creack/pty`: PTY creation and control
- `github.com/creack/termios`: raw terminal mode (examples)
- `github.com/rivo/uniseg`: Unicode grapheme cluster segmentation
- `github.com/google/go-cmp`: Test comparisons
