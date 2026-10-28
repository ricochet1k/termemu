//go:build !libghostty

package main

import (
	"strings"
	"testing"
)

func TestNewGhosttyOracleUntagged(t *testing.T) {
	oracle, err := newGhosttyOracle(80, 24)
	if err == nil {
		if oracle != nil {
			_ = oracle.Close()
		}
		t.Fatal("expected an error without the libghostty build tag")
	}
	if oracle != nil {
		t.Fatalf("expected nil oracle, got %T", oracle)
	}
	if !strings.Contains(err.Error(), "-tags libghostty") {
		t.Fatalf("error %q should mention -tags libghostty", err)
	}
}
