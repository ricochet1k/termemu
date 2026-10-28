//go:build !libghostty

package main

import "errors"

// newGhosttyOracle is unavailable unless the binary is built with the
// libghostty build tag. The stub returns a clear error so that `go build`
// without the tag succeeds and `diff` can report why ghostty comparison is
// disabled.
func newGhosttyOracle(width, height int) (Oracle, error) {
	return nil, errors.New("ghostty oracle unavailable: build with -tags libghostty and install libghostty-vt (see README.md)")
}
