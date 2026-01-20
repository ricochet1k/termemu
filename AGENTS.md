# AGENTS

This file provides build/test commands and code style guidance for agents working in this repo.
Follow these instructions unless the user gives higher-priority directions.
Keep this file up-to-date. If you have to search for something, add it to the Repository layout. If the layout is out of date, fix it. If I have to issue corrections, record them here in AGENTS.md.

## Quick facts
- Module: `github.com/ricochet1k/termemu`
- Go version: 1.24+ (see `go.mod`)
- Primary language: Go

## Repository layout
- Core library code lives at repo root (package `termemu`).
- Examples live under `examples/`.
- Test helpers and benchmarks live in `_test.go` files at root.

## Build, lint, and test commands
Use these exact commands unless the user asks otherwise.

### Build
- Build all packages: `go build ./...`
- Build a specific package: `go build ./path/to/pkg`
- Build examples:
  - `go build ./examples/simple`
  - `go build ./examples/tty`
  - `go build ./examples/unicode_test`

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
No dedicated linter is configured in the repo.
When requested, use the Go toolchain defaults:
- `go vet ./...`
- `gofmt -w <files>` (formatting only; see style section)

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
- Use `WithLock` helpers where available instead of manual lock plumbing.
- Avoid launching goroutines that outlive their owners unless required.

#### Data race prevention (critical!)
The terminal has a background read loop goroutine (`ptyReadLoop`) that continuously writes to screen state.
Calling read methods from test/main goroutines without locks causes data races.

**Why `ptyReadOne` can't hold the lock for its entire duration:**
The function does blocking I/O reads (`ReadPrintableBytes`, `ReadByte`, `handleCommand`).
Holding the lock during blocking reads would deadlock if anyone tries to access the terminal.
Instead, it acquires the lock around each state mutation, then releases before the next read.

**Frontend callback contract:**
All `Frontend` interface methods are called with the terminal lock already held.
Frontend implementations can safely call terminal read methods (`Line`, `ANSILine`, etc.)
without acquiring locks. See godoc on `Frontend` interface in `frontend.go`.

**Methods requiring the lock** (check docstrings for "caller must lock"):
- `ANSILine()`, `Line()`, `StyledLine()`, `StyledLines()`, `PrintTerminal()`

**How to fix in tests:**
```go
// WRONG - races with ptyReadLoop:
line := term.Line(0)

// CORRECT - use WithLock:
var line string
term.WithLock(func() {
    line = term.Line(0)
})
```

**When adding new terminal read methods:**
1. Either document that caller must hold lock, OR
2. Create a `*Safe()` variant that acquires the lock internally

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
- Span-based rendering (spanScreen) optimizes memory and reduces allocations for large screens
- Grid-based rendering (gridScreen) may have simpler logic but higher memory overhead
- Wide characters (CJK, emoji) need special handling (splitSpan)
- Text merging (combining characters) requires careful cursor position handling
- Frontend callbacks happen with lock held, keep them fast
- PTY operations are platform-specific (uses creack/pty for portability)
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
- `PTYBackend`: Real PTY using github.com/creack/pty
- `NoPTYBackend`: Pipes for non-PTY environments
- `TeeBackend`: Wraps a backend to duplicate reads

**Terminal** (terminal.go): Main coordinator
- Owns main and alternate screens
- Runs `ptyReadLoop()` goroutine to read from backend
- Manages view flags/ints/strings (cursor visibility, mouse mode, window title, etc.)
- Provides locking (`Lock()`, `Unlock()`, `WithLock()`) for thread-safe access
- Handles keyboard input via `SendKey()`

**Screen** (screen.go, screen_grid.go): Character grid management
- **TWO IMPLEMENTATIONS CURRENTLY EXIST** - comparing performance before deleting the slower one:
  - `gridScreen`: Traditional cell-based grid (arrays of runes, styles, widths per cell)
  - `spanScreen`: Optimized span-based representation (runs of styled text)
- `spanScreen` represents the screen as a list of `spanLine`s, each containing `[]Span`
- Spans optimize storage: `Span{Rune: ' ', Width: 80}` for 80 spaces vs storing 80 cells
- `gridScreen` stores per-cell data: `chars[][]rune`, `cellStyles[][]Style`, `cellWidth[][]uint8`, `cellCont[][]bool`
- Both implement the `screen` interface with identical public API
- Handles complex operations: scrolling, wide character overlaps, grapheme cluster splitting
- Screen size, cursor position, scroll margins, auto-wrap behavior

**Escape Sequence Parsing** (escapes.go):
- `ptyReadLoop()` reads from backend via `GraphemeReader`
- Distinguishes printable text runs from control codes
- Parses CSI sequences (cursor movement, SGR colors, screen manipulation)
- Parses OSC sequences (window title, working directory)
- Parses keyboard protocol mode switches

**Style System** (style.go):
- `Style` struct uses bit-packing: 3 uint32 fields (fg, bg, underlineColor)
- Each uint32: bit 31 = color type (256-color vs RGB), bits 24-30 = mode bits, bits 0-23 = color data
- Modes: Bold, Dim, Italic, Underline, Blink, Reverse, Invisible, Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink
- `ANSIEscape()` generates SGR sequences for rendering

**Grapheme Handling** (grapheme_reader.go):
- `GraphemeReader` wraps an io.Reader and tokenizes text
- Two modes: `TextReadModeRune` (1 rune = 1 token) or `TextReadModeGrapheme` (1 grapheme cluster = 1 token)
- Handles combining characters, zero-width joiners, emoji sequences
- Returns merge tokens for combining chars that should merge into previous cell

**Line Representation** (line.go, screen_grid.go):
- `Line` holds `[]Span` for external API access
- `Span` can be text (`Text` field) or repeated rune (`Rune` + `Width`)
- `spanLine` is internal representation with width tracking
- Functions: `splitSpan()`, `replaceRange()`, `truncateLine()`, `mergeAdjacent()` handle complex span manipulation

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
- `ScrollLines(y)`: Lines scrolling off top (for scrollback buffer)
- `Bell()`: Bell character received

`EmptyFrontend` is a no-op implementation for when no UI callbacks are needed.

### Text Rendering Modes

The library supports two text handling modes:
1. **Rune mode**: Each Unicode code point is one cell (simple, fast)
2. **Grapheme mode**: Each grapheme cluster is one cell (proper Unicode, handles combining chars)

Choose via `NewWithMode(frontend, backend, TextReadModeRune/Grapheme)`

## Testing Patterns

**Unit Tests:**
- Use `MakeTerminalWithMock()` or `MakeScreenWithMock()` to get terminal/screen with `MockFrontend`
- `MockFrontend` records all callbacks (regions, cursor moves, styles)
- Check expectations against recorded calls

**Integration Tests:**
- Use real `PTYBackend` with slave writes to simulate command output
- Give `time.Sleep()` for ptyReadLoop to process
- Check MockFrontend received expected callbacks

**Table-Driven Tests:**
- See escapes_test.go and screen_test.go for examples
- Test cases specify input and expected screen state

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
- Use `Rune + Width` for repeated characters (spaces, dashes)
- Use `Text + Width` for actual text content
- `replaceRange()` is the core function for modifying span lines
- Always respect grapheme cluster boundaries (don't split wide chars)

### Screen Grid Operations

Key functions in screen.go:
- `writeString()`: Write text at cursor, advance cursor
- `rawWriteSpan()`: Write span at specific position
- `eraseRegion()`: Clear rectangular area
- `scroll()`: Scroll region vertically
- `deleteChars()`: Delete characters, shift remaining left

## Screen Implementation Status

**IMPORTANT:** Two screen implementations currently coexist for performance comparison:
- `gridScreen` in screen_grid.go (traditional cell-based)
- `spanScreen` in screen.go (span-based optimization)

Both implement the `screen` interface. The slower implementation will be deleted once optimization analysis is complete. When making changes:
- Test both implementations if modifying screen interface
- Performance-critical changes may need implementation in both
- Use benchmarks in screen_bench_test.go to compare
- The `newScreen()` factory function chooses which implementation to use

## Dependencies

- `github.com/creack/pty`: PTY creation and control
- `github.com/rivo/uniseg`: Unicode grapheme cluster segmentation
- `github.com/google/go-cmp`: Test comparisons
