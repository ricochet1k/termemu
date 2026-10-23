package termemu

import (
	"io"
	"strings"
	"testing"
)

func TestTextWidthSequences(t *testing.T) {
	tests := []struct {
		name          string
		seq           string
		graphemeWidth int
		runeWidth     int
	}{
		{name: "thumbs up", seq: "👍", graphemeWidth: 2, runeWidth: 2},
		{name: "thumbs up skin tone", seq: "👍🏽", graphemeWidth: 2, runeWidth: 2},
		{name: "flag", seq: "🇺🇸", graphemeWidth: 2, runeWidth: 2},
		{name: "heart VS16", seq: "❤️", graphemeWidth: 2, runeWidth: 2},
		{name: "keycap one", seq: "1\uFE0F\u20E3", graphemeWidth: 2, runeWidth: 2},
		{name: "woman technologist ZWJ", seq: "👩\u200d💻", graphemeWidth: 2, runeWidth: 2},
		{name: "rainbow flag ZWJ", seq: "🏳\uFE0F\u200d🌈", graphemeWidth: 2, runeWidth: 2},
		{name: "combining acute", seq: "e\u0301", graphemeWidth: 1, runeWidth: 1},
		{name: "combining acute in text", seq: "a\u0301b", graphemeWidth: 2, runeWidth: 2},
		{name: "family ZWJ", seq: "👨\u200d👩\u200d👧\u200d👦", graphemeWidth: 2, runeWidth: 2},
	}

	modes := []struct {
		name string
		mode TextReadMode
	}{
		{name: "grapheme", mode: TextReadModeGrapheme},
		{name: "rune", mode: TextReadModeRune},
	}

	for _, mode := range modes {
		for _, tt := range tests {
			t.Run(mode.name+"/"+tt.name, func(t *testing.T) {
				want := tt.graphemeWidth
				if mode.mode == TextReadModeRune {
					want = tt.runeWidth
				}

				_, term, _ := MakeTerminalWithMock(mode.mode)
				if err := term.testFeedTerminalInputFromBackend([]byte(tt.seq), mode.mode); err != nil {
					t.Fatalf("feed %q: %v", tt.seq, err)
				}

				if pos := term.CursorPos(); pos.X != want || pos.Y != 0 {
					t.Fatalf("cursor = (%d,%d), want (%d,0) for %q", pos.X, pos.Y, want, tt.seq)
				}

				screen := term.screen().(*spanScreen)
				if len(screen.lines[0].spans) == 0 {
					t.Fatalf("no spans stored for %q", tt.seq)
				}
				if got := screen.lines[0].spans[0].Width; got != want {
					t.Fatalf("stored span width = %d, want %d for %q", got, want, tt.seq)
				}

				line := term.StyledLine(0, want, 0)
				if got := strings.TrimRight(line.PlainTextString(), " "); got != tt.seq {
					t.Fatalf("rendered text = %q, want %q", got, tt.seq)
				}
				if line.Width != want {
					t.Fatalf("line width = %d, want %d", line.Width, want)
				}
			})
		}
	}
}

func TestNewDefaultsToGraphemeMode(t *testing.T) {
	term := New(&EmptyFrontend{}, NewNoPTYBackend(strings.NewReader(""), io.Discard))
	if term == nil {
		t.Fatal("New returned nil")
	}
	defer term.Close()

	tm := term.(*terminal)
	tm.Lock()
	mode := tm.textReadMode
	tm.Unlock()
	if mode != TextReadModeGrapheme {
		t.Fatalf("New default mode = %v, want TextReadModeGrapheme", mode)
	}
}
