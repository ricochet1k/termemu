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
- Helper scripts live under `scripts/`; `scripts/bench.sh` runs benchmarks
  (optionally with CPU/heap profiles via `PROFILE=1`). Generated test
  artifacts (`scripts/tmux_integration_test`,
  `scripts/integration_test_results.txt`) are gitignored.
- Test helpers and benchmarks live in `_test.go` files at root.
- `backend.go` holds `Backend`, `PTYBackend`, `NoPTYBackend`, `TeeBackend`.
  `backend_unix.go` / `backend_other.go` hold `setControllingTTY` (the only
  platform-specific child-session setup).
- `terminal.go` (coordinator + scrollback wiring), `handoff.go` (Pause/
  Resume/NewFromHandoff and reader state), `screen.go` (the only screen
  implementation), `scrollback.go` (scrollback ring buffer + reflow),
  `escapes.go` (ANSI parser), `grapheme_reader.go` (grapheme tokenizer),
  `charset.go` (G0-G3 designations, GL selection, DEC Special Graphics),
  `style.go` (colors/modes), `keys.go` (input encoding), `tty_frontend.go`
  (ANSI renderer frontend).
- `serialize_test.go` covers JSON/gob round-trips for the public value types.

## Serialization
All public value types (`Style`, `Span`, `Line`, `Region`, `Pos`, `Row`,
`ScrollbackSnapshot`, `ScreenState`, `Snapshot`, `ReaderState`, `Handoff`)
round-trip through
`encoding/json` and `encoding/gob`. `Style`, `keyboardMode`, and `charsetState`
implement
custom `MarshalJSON`/`UnmarshalJSON` and `GobEncode`/`GobDecode` over exported
shadow structs, so `Snapshot.KeyboardMain/KeyboardAlt` and
`ScreenState.Charsets/SavedCharsets` serialize even though their types are
unexported. `ScreenState.Tabs` is a plain `[]bool` (nil means the default
stops every 8 columns). `Style` encodes its three packed uint32 components
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
- Run all benchmarks: `go test ./... -run '^$' -bench . -benchmem`
- Run a single benchmark: `go test ./path/to/pkg -run '^$' -bench '^BenchmarkName$'`
- Convenience wrapper with optional profiles: `scripts/bench.sh [pattern] [benchtime]`
  and `PROFILE=1 scripts/bench.sh Scrollback` (writes `cpu.pprof`/`mem.pprof`).
- `bench_extra_test.go` covers screen/terminal snapshot+restore, resize, ANSI
  line rendering, escape-command parsing, scrollback, and retained memory
  (`B/screen`, `B/cell`). `screen_bench_test.go` holds the write-throughput
  benchmarks.

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

**Why `ptyReadOne` can't hold the lock across a blocking read:**
It only reads from the backend when the reader's buffer is empty, and it does
that without the lock. Every command and printable run already in the buffer
is then applied by `processBuffered` under a single lock acquisition. A command
split at the buffer boundary is parked in `pendingCmd` (without copying, it
aliases the reusable `cmdBuf`) and resumed with a blocking read outside the
lock. Do not make `processBuffered` fill from the backend or call the locking
public read methods: that would reintroduce the deadlock.

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
- Handles keyboard input via `SendKey()` and mouse reports via
  `SendMouseRaw()`. Mouse flags are masked to the documented bits before
  encoding: X10 writes raw parameter bytes (UTF-8-encoding them would corrupt
  values above 0x7F), 1005 encodes all three parameters as codepoints, and
  1002 drops motion events that carry no button (those belong to 1003).
  `?1016` (SGR-Pixels) is exposed as `VFMousePixel`: the frontend should pass
  pixel coordinates, and they are encoded verbatim. `?1007` (alternate
  scroll) is handled here too: on the alt screen with no mouse mode active, a
  wheel press becomes KeyUp/KeyDown. `?1046` clears `VFAltScreenAllowed`, and
  both alt-screen entry paths ignore switch requests while it is clear.
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
- Tab stops are a per-screen `tabs []bool` (`len == size.X`, default every
  `defaultTabInterval` columns). `setSize` keeps existing stops and defaults
  new columns; RIS and DECSTR reset them. TAB/CHT stop on the last column when
  no stop remains (VTE/kitty behavior), CBT stops on column 0.
- Spans optimize storage: `Span{Width: 80}` (empty Text) for 80 spaces vs storing 80 cells
- A parallel `wrapped []bool` marks rows that continue the previous row due to
  auto-wrap; it feeds scrollback logical lines and is cleared by explicit cursor
  positioning / full-width erase
- `setSize` keeps every line exactly the new width (a wide cluster dropped by
  truncation is padded back), clamps the cursor/saved cursor and scroll margins
  into the new bounds. `scroll` clamps `|dy|` to the region height and ignores
  invalid margins (`top > bottom`).
- `mergeIntoPreviousCell` reports the whole merged span in its
  `RegionChanged` (a mark merged into a wide cluster changes two cells), so
  frontends repaint the cluster instead of just the cursor cell.
- DECSC/DECRC save/restore cursor, pending wrap, the complete `Style`
  (`ScreenState.SavedStyle`), the character set state
  (`ScreenState.SavedCharsets`), and origin mode (`ScreenState.SavedOriginMode`). `CSI r` (DECSTBM) homes the cursor; `CSI 2 J`
  erases without moving it. IL/DL use the cursor-to-bottom-margin region when
  the cursor is inside the margins and cursor-to-screen-bottom when it is above
  or below them (xterm/tmux behavior). Vertical margin scrolling only applies
  to actual vertical cursor movement, so CR cannot scroll a region.
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
  wrap, saved cursor, style, character sets, insert mode, origin mode (plus
  its DECSC slot), the REP character, tab stops, margins, and auto-wrap.
  `restoreState` emits no callbacks; the terminal owns the repaint
  notifications.
- `setScrollCapture` installs a callback invoked (with the terminal lock held)
  before rows scroll off the top, passing the lost rows and their wrapped flags

**Escape Sequence Parsing** (escapes.go):
- `ptyReadLoop(ReaderState)` reads from backend via `GraphemeReader`; it starts
  from captured state and first continues `pendingCmd` when present
- `readEscapeCommand` scans CSI/OSC-style commands a chunk at a time through
  the optional `bulkEscapeReader` (`GraphemeReader.peekBuffered`/
  `discardBuffered`) instead of calling `ReadByte` per byte; other readers and
  replayed partial bytes use the byte-at-a-time fallback. Keep the two paths'
  terminator/malformed-byte semantics identical
- `ptyReadOne(readLoopState)` blocks for backend bytes only when the reader
  buffer is empty, outside the lock. `processBuffered` then applies every
  complete escape command and printable run already buffered under one lock
  acquisition (the lock-traffic win), using `readPrintableRunBuffered` and a
  `bufferedEscapeReader` that return `io.EOF` at the buffer end instead of
  reading the backend. A command split at that boundary is stored in
  `pendingCmd` (aliasing `cmdBuf`, no copy) and resumed by
  `continueEscapeCommand` with a normal blocking read before the lock is taken,
  so a child that stops mid-sequence still cannot block `Snapshot`/`Restore`.
  Keep the pause/handoff invariant: `ReaderState` still captures the buffered
  bytes plus the partially consumed command.
- `dispatchEscapeCommand` first tries `tryFastCSI`, which parses the completed
  command bytes in one pass into a fixed parameter store and dispatches the hot
  plain forms (`H`/`f`, `m`, `A`-`D`, `J`, `K`). `J`/`K`/SGR/CUP share their
  application helpers with the general parser. Anything else (private markers,
  intermediates, sub-parameters, malformed bytes, other finals) falls back to
  `handleCommand` unchanged, so the partial-command replay path is unaffected.
- The printable-run `maxWidth` is computed by `processBuffered` from live
  screen state under the same lock that applies each buffered run. If a run is
  read through the public `ReadPrintableBytes` and the terminal is resized in
  the meantime, `writeString` re-checks the current screen width and
  `writeLongString` splits an oversized stale run at grapheme boundaries, so
  the output is still laid out at the new width.
- Distinguishes printable text runs from control codes
- CSI parameters are separated by `;` and sub-parameters by `:`. Handlers use
  the primary (first) value of each parameter; SGR also reads the
  colon-separated groups, so `38:2::r:g:b`, `38:2:r:g:b`, `38:5:n`, the
  semicolon forms, and `58`/`59` underline colors all parse. Unknown or short
  colon forms are ignored rather than leaking their bytes as text
- Parses CSI sequences (cursor movement, SGR colors, screen manipulation).
  Relative cursor moves include CNL/CPL (`E`/`F`), HPR/VPR (`a`/`e`), and
  CHT/CBT (`I`/`Z`, which use the screen's tab stop map). Explicit cursor
  moves clamp to the screen and never scroll; only LF/VT/FF/IND and RI scroll
  at margins. LF, VT and FF move down without changing the column, as on a
  VT100 (the tty driver normally delivers CRLF; raw-mode programs rely on the
  column being kept). NEL (`ESC E`) is the line feed plus carriage return.
  ICH (`CSI @`) inserts blanks and shifts the row right; IRM
  (`CSI 4 h/l`) makes printable writes insert instead of overwrite, and LNM
  (`CSI 20 h/l`) makes LF perform a carriage return too. HTS
  (`ESC H`) and TBC (`CSI g`) set/clear tab stops. DECOM (`CSI ? 6 h/l`)
  makes CUP/HVP/VPA rows relative to the top margin, confines explicit cursor
  movement to the scroll region, and moves home to the top margin. REP
  (`CSI b`) re-emits the last graphic code point; the state is cleared by every
  control character and escape sequence (after dispatch, so REP itself still
  sees it), matching xterm and tmux.
- Parses OSC sequences: 0/2 window title, 6/7 working directory
  (`VSCurrentDirectory`; OSC 7 no longer sets `VSCurrentFile`), 8 hyperlinks
  (tracked URI pushed to both screens via internal `setLink`), 52 clipboard
  writes (base64-decoded, forwarded to `ClipboardSink`; queries dropped),
  9/777 notifications (forwarded to the optional `NotificationSink`), and 133
  shell-integration marks (`A`/`B`/`C`/`D` decoded into a `PromptMark` and
  forwarded to the optional `ShellIntegration`; other 133 forms are ignored).
  Both optional interfaces get the terminal lock and a `LockedView`.
- `DECSET/DECRST 2026` sets/clears `VFSynchronizedOutput`
- `DECRQM` (`CSI [?]Ps $ p`) reports both DEC private modes
  (`privateModeState`) and ANSI modes (`ansiModeState`: IRM, LNM).
  `DECREQTPARM` (`CSI Ps x`) answers with xterm's VT100 parameter reply.
- `DECSCUSR` (`CSI Ps SP q`) stores the cursor shape in
  `VICursorShape` (the raw `Ps`, `CursorShape*` constants); unknown shapes are
  ignored. `TTYFrontend` forwards it to the outer terminal on change and on
  `Attach`, and restores the default on `Detach`.
- Parses keyboard protocol mode switches
- IRM and DECOM behave as terminal-wide modes (xterm keeps both in
  `xw->flags`): `copyTransientState` carries them across a main/alt switch
  along with the cursor, clamping a carried cursor into the destination
  screen's margins. `restoreCursorPos` also clamps into the *current* margins,
  because DECRC may follow a DECSTBM that changed them.
- Character sets (`charset.go`): ESC `(`/`)`/`*`/`+` designate G0-G3, `SO`/`SI`
  (0x0E/0x0F) invoke G1/G0 as GL, and ESC `n`/`o` invoke G2/G3 (LS2/LS3).
  Only ASCII and DEC Special Graphics (ESC `( 0`) are mapped; the national
  replacement sets and unknown designations fall back to ASCII. The mapping
  happens in `spanScreen.writeString` (via `mapDECGraphics`) before the run is
  measured, and only when the invoked GL set is DEC graphics, so ordinary
  ASCII output pays a single comparison. Mapped runes are one cell wide and
  UTF-8 continuation bytes cannot fall in 0x5F-0x7E, so the run width stays
  valid and multi-byte text passes through. DECSC/DECRC save the character
  set state with the cursor; RIS and DECSTR reset it. The state is shared
  across the main/alt switch (`copyTransientState`) like the other transient
  fields, and tmux is not a usable reference for it (tmux keeps the
  designation/shift bytes in its grid and leaves the mapping to the outer
  terminal, so `capture-pane` shows the raw ASCII).

**Palette and default colors** (palette.go): the terminal owns a 256-entry RGB
palette (`OSC 4`, reset by `OSC 104`) and default foreground/background/cursor
colors (`OSC 10`/`11`/`12`, reset by `110`/`111`/`112`). Cells keep palette
indexes; frontends resolve them with `LockedView.PaletteColor` /
`DefaultForeground` / `DefaultBackground` / `CursorColor` (there are locking
`Terminal` equivalents). Queries (`OSC 4;n;?`, `OSC 1x;?`) are answered with
`rgb:rrrr/gggg/bbbb`. Any change emits one full-screen `RegionChanged` with
`CRPalette` so frontends repaint; re-setting an identical color does not.
Palette and default colors are part of `Snapshot` (and therefore handoff) and
are reset by RIS. `ANSILine` still emits palette indexes, so a frontend that
renders to another terminal should resolve changed entries itself.

**Modes** (terminal.go): `LNM` (`CSI 20 h/l`) is terminal-wide (xterm keeps it
in `xw->flags`), so it lives on `terminal.newlineMode` and round-trips through
`Snapshot.NewlineMode`. RIS clears it; DECSTR does not (xterm's soft reset only
clears INSERT and ORIGIN among the mode flags).

**Style System** (style.go):
- `Style` struct uses bit-packing: 3 uint32 fields (fg, bg, underlineColor)
- Each uint32: bit 31 = color type (256-color vs RGB), bits 24-30 = mode bits, bits 0-23 = color data
- Modes: Bold, Dim, Italic, Underline, Blink, Reverse, Invisible, Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink
- `ANSIEscape()` generates SGR sequences for rendering
- `ANSIEscapeFrom(prev)` emits minimal diffs. Colors use `SGR 39`/`49` to
  return to default. Mode changes prefer individual SGR set/reset codes
  (`1`/`22`, `3`/`23`, `4`/`24`, `24`/`21`, `25`/`5`+`6`, `54`/`51`+`52`,
  `55`/`53`, ...) so unchanged colors are not re-emitted. Going to a plain
  default style emits one `SGR 0`; dropping all modes while switching to a new
  non-default color also uses a reset followed by the colors
- `modeSGRGroups`/`modeSGRCodes` are indexed by mode bit position; keep them in
  sync when adding a mode (`TestModeSGRCodesCoverAllModes`)
- Both delegate to `appendANSIEscapeFrom`/`appendANSIEscapeColor`, which
  append to a caller-provided buffer (no per-call slice allocation). The
  underline color has no 30-37/90-97 shorthand, so
  `appendANSIEscapeUnderlineColor` always emits `SGR 58;5;n` or
  `SGR 58;2;r;g;b` (a bright color therefore round-trips as palette 8+n)
- Line renderers (`spanScreen.renderLineANSI`, `ttyFrontend`'s
  `renderStyledLineANSI`) emit one leading `SGR 0` and then deltas between
  adjacent spans; do not revert to a full escape per span

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
- `ReadPrintableBytes` additionally consumes interior printable-ASCII bytes
  in both modes without calling the tokenizer when the next byte is also
  printable ASCII (no selector can follow). The last byte of an ASCII run
  still goes through the tokenizer; keep it that way so VS16/keycap widening
  and read-boundary behavior stay identical (`TestRuneModeASCIIFastPathChunking`).
  Grapheme mode still requires `!lastWasRI && !forceMergeNext`
- `readPrintableRunBuffered` is `readPrintableRun` with filling disabled: it
  consumes only buffered bytes and reports an incomplete trailing cluster as
  empty, so the read loop can apply a whole chunk under one lock and then
  block for the remainder outside it
- `splitSpan` and `byteIndexForCell` map cell offsets to byte offsets directly
  when a span's text is all ASCII (`Width == len(Text)`), in both modes
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
- Row span storage is a lazily allocated arena (`terminal.scrollArena`), not a
  per-row `make`. It is split into `scrollArenaBlock` chunks of
  `scrollArenaBlockRows` ring slots; each slot has a fixed `stride` span
  capacity, so `addScrollRow` is a copy into its slot (no allocation) and
  eviction just overwrites the slot on reuse. Blocks are appended as the ring
  fills, so a fresh terminal reserves nothing. A block grows its stride when a
  wider row arrives and re-copies the rows currently in that block; growth
  rounds up only above 8 spans. The unused tail of a slot is cleared on every
  store (and `Clear` clears live rows) so evicted text is not retained. Restore
  rebuilds the ring and arena from the snapshot, so snapshots from either
  layout work. Public row copies are unchanged: `Row`/`LogicalLine` still hand
  out independent span slices.
- `LogicalLine(id)` walks backward/forward over `wrapped` rows to rebuild the
  logical line and reflows it at the terminal's current width. The walk stops
  at the oldest stored row (it must not wrap around to the newest row) and
  never visits the ring twice. Reflow never splits a wide cluster across rows:
  a row may be shorter than the terminal width when the next cluster does not
  fit. Trailing blank padding on the final physical row is dropped before
  reflow, so a 20-column line of 50 cells returns rows of width 20/20/10. Each
  returned row's `ID` is
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
- Switching screens carries the current cursor, pending wrap, and style over to
  the other screen (xterm: the switch itself moves nothing). Each screen keeps
  its own saved cursor for DECSC/DECRC.
- `?47` preserves alt contents; `?1047` clears the alt screen on the way out;
  `?1048` is DECSC/DECRC; `?1049` saves the main cursor, clears alt on entry,
  and restores the main cursor on exit.
- Switching screens triggers `CRScreenSwitch` change reason

### Frontend Interface

Frontends receive callbacks:
- `RegionChanged(Region, ChangeReason)`: Area of screen changed (text, clear, scroll, screen switch)
- `CursorMoved(x, y)`: Cursor position changed
- `StyleChanged(Style)`: Current style changed
- `ViewFlagChanged/ViewIntChanged/ViewStringChanged`: Terminal mode changed
  (includes `VFSynchronizedOutput` for `DECSET/DECRST 2026`; frontends may
  defer repaints while set)
- Optional `BatchFrontend.PrepareBatch(v)` / `WriteBatch()`: damage-batched
  rendering. Callbacks still fire per change; `PrepareBatch` renders the
  accumulated damage under the terminal lock and `WriteBatch` writes it after
  the lock is released. The read loop calls the pair when it is about to
  block for more input (or stops/parks), and `Resize`/`Restore` call it after
  their out-of-band updates. Non-batching frontends keep the per-change
  contract.
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
`examples/tty`). `TTYFrontend` implements `BatchFrontend`: `RegionChanged`
unions damage, `PrepareBatch` renders one frame under the terminal lock and
skips rows whose emitted bytes match a per-row shadow, and `WriteBatch` does
the actual `Write` without the terminal lock. Scroll damage (`CRScroll`)
invalidates the affected rows' shadows instead of comparing them, since a
scrolled row almost always differs. Keep `t.mu` (frontend state) and `writeMu`
(output writes) out of any path that takes the terminal lock: `writeMu` is a
leaf lock, and callbacks take `t.mu` while the terminal lock is already held.

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
- `fuzz_more_test.go` adds `FuzzTerminalResizeRestore` (arbitrary content
  through resize + different-size restore), `FuzzScrollbackLogicalLine`
  (logical-line walk/reflow with random rows, wrap flags, and widths), and
  `FuzzHandoffTargetSize` (Handoff restored at a different target size).
- Corpus seeds under `testdata/fuzz/<Target>/` are regression tests; keep them
  when a fuzz failure is fixed.
- `tmux_diff_test.go` is an opt-in differential test (`TERMEMU_TMUX_DIFF=1`,
  or `scripts/run_tmux_diff.sh`). Its normal corpus feeds termemu the same
  bytes tmux sees (the pane tty's ONLCR turns LF into CRLF); a raw corpus runs
  the pane with `stty -opost` to compare bare LF/VT/FF semantics, and
  `TestDifferentialTmuxScrollback` compares history rows (captured with
  `-S -N`) against the scrollback ring plus the visible screen, with tmux's
  `scroll-on-clear` option disabled. It starts a private tmux server, compares
  the captured screen and cursor against termemu, and normalizes termemu's
  output through the same tmux. `TestDifferentialTmux` checks curated
  sequences including styles; `TestDifferentialTmuxRandom` runs seeded random
  sequences (50 seeds) and compares visible text, cursor, cursor visibility,
  and alt-screen state (styles on blank cells are intentionally not compared
  there). Add validated sequences to `tmuxDiffSequences`; avoid
  version-sensitive features (OSC 8 capture, images) unless the harness is
  extended. tmux lacks CHT (`CSI I`) and ignores HPR/VPR (`CSI a`/`e`), so
  those are covered by unit tests rather than the differential corpus.
- Failing fuzz inputs are kept under `testdata/fuzz/FuzzTerminalInput/` and run
  as seeds by a normal `go test`. Run a longer session with
  `go test -run '^$' -fuzz '^FuzzTerminalInput$' -fuzztime 30s .`.
- `scrollback_property_test.go` checks the ring buffer against a model under
  random append/evict/clear/snapshot operations.
- `keys_coverage_test.go` enforces that every `KeyCode` encodes in legacy and
  Kitty modes, and property-checks the functional-code and keypad tables.
- `fuzz_input_modes_test.go` adds `FuzzKeyEncoding` (arbitrary KeyEvent × Kitty
  flags), `FuzzMouseEncoding` (every encoding and mouse mode, decoding the
  report back), `FuzzEscapeCommandResume` (byte-at-a-time partial-command
  replay must match a whole-buffer parse), and `FuzzTTYFrontendRoundTrip`
  (render through `TTYFrontend`, re-parse, compare cells; OSC 8 links are
  ignored because the renderer does not re-emit them).`TestKeyEncodingProperties`
  and `TestMouseEncodingProperties` keep the same checks in the normal suite.

## Common Development Tasks

### Adding a New Escape Sequence

1. Add parsing in `ptyReadLoop()` (escapes.go) switch statement
2. Implement behavior (usually call screen methods)
3. Add test case in escapes_test.go
4. Update integration_failures_test.go if testing specific sequences

### Adding a New Style Mode

1. Add constant to `Mode` type in style.go
2. Add its SGR code to the `modeSGRCodes` array (indexed by mode bit position)
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

`scroll` saves the rows leaving the region in `scrollScratch` and recycles their
span backing arrays into the vacated rows via `resetSpanLine`; `scrollCapture`
receives that scratch and must not retain it. Do not recycle rows in place when
the shifted copies still share the same backing array (for example
`takeTopRows`), or the live rows get clobbered.

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
