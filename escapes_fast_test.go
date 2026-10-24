package termemu

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

// TestParseFastCSIAcceptsOnlyPlainHotCommands pins the boundary between the
// fast path and the general parser. Anything with a private marker, an
// intermediate, a sub-parameter, a malformed byte, or an unexpected final byte
// must fall back.
func TestParseFastCSIAcceptsOnlyPlainHotCommands(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"[H", true},
		{"[12;40H", true},
		{"[12;40f", true},
		{"[m", true},
		{"[0m", true},
		{"[1;38;2;200;120;40;48;5;17m", true},
		{"[1;2;3;4;5;6;7;8m", true},
		{"[A", true},
		{"[5A", true},
		{"[0B", true},
		{"[3C", true},
		{"[9D", true},
		{"[J", true},
		{"[2J", true},
		{"[K", true},
		{"[1K", true},
		{"[99999999999999999999A", true}, // same int arithmetic as the general parser

		{"", false},
		{"[", false},
		{"[]m", false},
		{"[?25h", false},
		{"[>c", false},
		{"[<u", false},
		{"[=u", false},
		{"[1;2:3m", false},
		{"[1 q", false},
		{"[1$p", false},
		{"[12\x7f", false},
		{"[12", false},
		{"[1;2;3;4;5;6;7;8;9m", true}, // extra parameters are truncated like the general parser
		{"[1;2;3;4;5;6;7;8;9A", true},
		{"[1S", false},
		{"[1;2r", false},
		{"[1;2X", false},
		{"[1;2L", false},
		{"M", false},
		{"]0;title\x07", false},
	}

	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	for _, tc := range cases {
		_, ok := term.tryFastCSI([]byte(tc.cmd))
		if ok != tc.want {
			t.Errorf("tryFastCSI(%q) ok = %v, want %v", tc.cmd, ok, tc.want)
		}
	}
}

// snapshotJSON serializes the full terminal state (both screens, cursor,
// styles, margins, modes) so the two parsers can be compared exactly.
func snapshotJSON(t *testing.T, term *terminal) []byte {
	t.Helper()
	b, err := json.Marshal(term.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return b
}

// newFastTestTerminal builds two identical terminals with some content and a
// non-default style so commands act on interesting state.
func newFastTestTerminals(t *testing.T) (general, fast *terminal) {
	t.Helper()
	newTerm := func() *terminal {
		term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
		if term == nil {
			t.Fatal("newTerminal returned nil")
		}
		term.mainScreen.setSize(20, 6)
		term.altScreen.setSize(20, 6)
		for i, cmd := range []string{
			"[2;3H",
			"[1;38;5;196;48;2;1;2;3m",
		} {
			if !term.handleCommand(bytes.NewReader([]byte(cmd))) {
				t.Fatalf("setup command %d (%q) failed", i, cmd)
			}
		}
		term.mainScreen.writeString("abcdefghij", 10, false, TextReadModeRune)
		for i, cmd := range []string{
			"[3;5r",
			"[?6h",
		} {
			if !term.handleCommand(bytes.NewReader([]byte(cmd))) {
				t.Fatalf("setup command %d (%q) failed", i, cmd)
			}
		}
		return term
	}
	return newTerm(), newTerm()
}

// TestFastCSIMatchesGeneralParser runs every hot command through both parsers
// and requires byte-identical terminal state.
func TestFastCSIMatchesGeneralParser(t *testing.T) {
	commands := []string{
		"[H", "[f",
		"[12;40H", "[12;40f", "[0;0H", "[0;3H", "[3;0H",
		"[1H", "[1;1H", "[6;20H", "[100;100H",
		"[A", "[0A", "[1A", "[5A", "[100A",
		"[B", "[0B", "[1B", "[5B", "[100B",
		"[C", "[0C", "[1C", "[5C", "[100C",
		"[D", "[0D", "[1D", "[5D", "[100D",
		"[m", "[0m", "[1m", "[22m", "[1;2;3;4;5;6;7;8m",
		"[38;5;196m", "[48;5;17m", "[38;2;200;120;40m", "[48;2;1;2;3m",
		"[38;5m", "[38;2;1;2m", "[38;9;1m", "[999m",
		"[39m", "[49m", "[90m", "[97m", "[100m", "[107m",
		"[21m", "[24m", "[51;52;53m", "[54;55m", "[9;29m",
		"[J", "[0J", "[1J", "[2J", "[3J", "[4J",
		"[K", "[0K", "[1K", "[2K", "[3K",
		"[99999999999999999999A", "[;A", "[;m", "[1;m", "[;1m",
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			general, fast := newFastTestTerminals(t)

			fOK, ok := fast.tryFastCSI([]byte(cmd))
			if !ok {
				t.Fatalf("tryFastCSI(%q) rejected a hot command", cmd)
			}
			gOK := general.handleCommand(bytes.NewReader([]byte(cmd)))
			if gOK != fOK {
				t.Fatalf("handled mismatch for %q: general=%v fast=%v", cmd, gOK, fOK)
			}

			gJSON := snapshotJSON(t, general)
			fJSON := snapshotJSON(t, fast)
			if string(gJSON) != string(fJSON) {
				t.Fatalf("state mismatch for %q\n general: %s\n fast:    %s", cmd, gJSON, fJSON)
			}
		})
	}
}

// TestFastCSIFallbackMatchesGeneralParser checks that sequences the fast path
// rejects still reach the general parser with the same result, including
// private markers and intermediates.
func TestFastCSIFallbackMatchesGeneralParser(t *testing.T) {
	commands := []string{
		"[?25h", "[?25l", "[?1049h", "[?1049l", "[?6h", "[?6l",
		"[>c", "[>q", "[>4;1m",
		"[<u", "[<1u", "[=1;2u",
		"[1 q", "[2 q", "[0 q",
		"[1$p", "[?25$p", "[!p",
		"[1;2:3m", "[12\x7f", "[12", "[1S", "[1;2r", "[1;2X",
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			general, fallback := newFastTestTerminals(t)

			if _, ok := fallback.tryFastCSI([]byte(cmd)); ok {
				t.Fatalf("tryFastCSI(%q) unexpectedly accepted", cmd)
			}
			if general.handleCommand(bytes.NewReader([]byte(cmd))) != fallback.handleCommand(bytes.NewReader([]byte(cmd))) {
				t.Fatalf("handled mismatch for %q", cmd)
			}

			if string(snapshotJSON(t, general)) != string(snapshotJSON(t, fallback)) {
				t.Fatalf("fallback state mismatch for %q", cmd)
			}
		})
	}
}

// TestFastCSIPartialReplay covers the pause/handoff path: a hot command split
// across reads still reaches the fast path and produces the same state as the
// same command delivered whole.
func TestFastCSIPartialReplay(t *testing.T) {
	const whole = "\x1b[12;7H\x1b[1;38;5;33mX"

	run := func(t *testing.T, chunks [][]byte) *terminal {
		term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
		term.mainScreen.setSize(20, 6)
		term.altScreen.setSize(20, 6)
		// gateAfter -1 serves every chunk as its own Read without blocking.
		st := newReadLoopState(newGatedChunkReader(chunks, -1), ReaderState{})
		for {
			if err := term.ptyReadOne(st); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("feed: %v", err)
			}
		}
		if len(st.pendingCmd) != 0 {
			t.Fatalf("command left pending: %q", st.pendingCmd)
		}
		return term
	}

	want := run(t, [][]byte{[]byte(whole)})
	got := run(t, [][]byte{[]byte("\x1b["), []byte("12;"), []byte("7H\x1b[1;38;"), []byte("5;33mX")})
	if string(snapshotJSON(t, want)) != string(snapshotJSON(t, got)) {
		t.Fatal("split command state differs from whole-command state")
	}
}
