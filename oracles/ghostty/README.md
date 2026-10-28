# ghostty oracle

A standalone tool that replays [asciicast](https://github.com/asciinema/asciinema/blob/develop/doc/asciicast-v2.md) recordings into `termemu` and, when built with the `libghostty` tag, into `libghostty-vt`, then reports any differences in the resulting screen state.

This directory is an **independent Go module** (`github.com/ricochet1k/termemu/oracles/ghostty`). It lives outside the root `termemu` module so that the library and root-module CI remain free of cgo and external dependencies.

## What it does

- `replay --cast FILE` replays an asciicast v2 or v3 file into a `termemu` terminal and prints the final screen dump.
- `diff --cast FILE` replays the same bytes into `termemu` and `libghostty-vt` and prints the first batch of divergences (size mismatch, per-cell text/style differences, cursor position, cursor visibility, or alt-screen flag).

Output events (`"o"`) are concatenated; input (`"i"`), marker (`"m"`), and resize (`"r"`) events are ignored.

## Building without libghostty

The default build compiles with no cgo and no external libraries:

```sh
cd oracles/ghostty
go build ./...
go test ./...
```

In this mode `diff` exits with a message explaining that the ghostty comparison requires `-tags libghostty`.

## Building with libghostty

`go.mitchellh.com/libghostty` is already a dependency of this module, so no
`go get` step is needed. It declares `go 1.26.0`, so this module's `go.mod`
requires Go 1.26 (the go command selects that toolchain automatically). That
requirement is contained here — the root `termemu` module keeps its own, lower
Go version.

1. Build and install `libghostty-vt` from the Ghostty source tree with Zig
   (requires zig >= 0.17.0, matching Ghostty's `minimum_zig_version`):

   ```sh
   zig build -Demit-lib-vt -Demit-xcframework=false --prefix /tmp/ghostty-vt
   ```

   `-Demit-xcframework=false` matters on macOS: with the xcframework slice
   enabled, the iOS simulator targets can fail to compile (the vendored
   Highway C++ library) even though the host library installs fine.

2. Point `pkg-config` at it and build with the `libghostty` tag:

   ```sh
   cd oracles/ghostty
   export PKG_CONFIG_PATH=/tmp/ghostty-vt/share/pkgconfig
   pkg-config --modversion libghostty-vt   # sanity check
   go build -tags libghostty ./...
   ```

3. Run `diff` against a cast:

   ```sh
   ./ghostty diff --cast recording.cast
   ```

This path is verified: built against `libghostty-vt 0.1.0-dev` (ghostty `main`)
on darwin/arm64 with zig 0.17.0, and `diff` reports `no divergences` on a clean
cast. Without `PKG_CONFIG_PATH` the tagged build fails at the cgo step with a
missing `libghostty-vt` pkg-config entry.

### Cross-compilation

Because `libghostty-vt` depends only on libc, you can cross-compile with Zig as the C compiler:

```sh
CGO_ENABLED=1 \
GOOS=linux GOARCH=amd64 \
CC="zig cc -target x86_64-linux-gnu" \
CXX="zig c++ -target x86_64-linux-gnu" \
CGO_CFLAGS="-I/tmp/ghostty-vt/include -DGHOSTTY_STATIC" \
CGO_LDFLAGS="-L/tmp/ghostty-vt/lib -lghostty-vt" \
go build -tags libghostty ./...
```

## Why a separate module?

The root `termemu` module must stay pure Go and portable. Keeping this oracle as its own module means:

- Root `go build ./...`, `go test ./...`, `go vet ./...`, and `GOOS=windows go build ./...` never see cgo or `libghostty-vt`.
- `oracles/ghostty` can depend on `termemu` through a `replace` directive pointing at the repository root, just like the separate GUI example modules under `examples/`.
- No `go.work` file is required.
