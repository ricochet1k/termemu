# Examples

Two kinds of examples live here.

## Root-module examples

These import only `termemu` and its existing dependencies, so they build with
the rest of the repo (`go build ./...`). Run them from the repository root:

| Example | What it shows |
| --- | --- |
| `simple` | Minimal `Frontend` usage: run a command, poll the screen. |
| `tty` | `TTYFrontend` in raw mode: nested terminal, forwards input and resize. |
| `unicode_test` | Grapheme/wide/combining character handling. |
| `esctest` | Interactive escape-sequence debugger. |
| `tmux` | tmux comparison harness (see its own README). |

```
go run ./examples/tty -- vim
```

## GUI frontend examples (separate modules)

Graphical frontends need toolkit dependencies, so each one is its own module
and is invisible to the root module's build, tests, vet, and lint. They share
`examples/internal/raster`, which renders terminal state into an `image.RGBA`
on the CPU with the bundled Go Mono font — no glyph atlas, no text shaping, no
font fallback. A production frontend would use a text library that provides
those; these examples are here to show the `Frontend`/`BatchFrontend` wiring,
not to be a renderer you should copy.

| Example | What it shows |
| --- | --- |
| `headless` | Runs a command and writes its screen to a PNG. No window, no GUI toolkit, so it is the easiest one to run and debug. |
| `gioui` | Gio window: text input via the platform IME, `key.EditEvent` for text, `key.Event` for functional keys, image blit for the grid. |
| `ebiten` | Ebiten game loop: `Update`/`Draw`, texture upload of the latest frame, `Termination` when the child exits. |

Run them from their own directory:

```
cd examples/headless && go run . -out screen.png -- ls --color=always /tmp
cd examples/gioui    && go run . -- sh
cd examples/ebiten   && go run . -- sh
```

Each module has `replace` directives pointing at the repository root and at
`examples/internal/raster`, so no `GOFLAGS` or workspace file is needed. After
changing imports, run `go mod tidy` inside that example directory; root
`go mod tidy` will not see them.

Notes and known gaps, shared by all three:

- Colors come from `termemu.Style` (`GetColor`) resolved through
  `LockedView.PaletteColor`/`DefaultForeground`/`DefaultBackground`, so `OSC 4`
  palette changes and `OSC 10/11/12` defaults work.
- Rendered modes: bold (synthetic smear), dim, reverse, invisible, underline,
  double underline, strike, overline. Italic, blink, and framed/encircled are
  parsed but not drawn.
- Mouse reporting (`SendMouseRaw`) and blink animation are not wired up.
- Go Mono has no CJK or emoji glyphs: wide clusters consume the right number
  of cells but draw as blank.
