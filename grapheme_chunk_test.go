package termemu

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

// byteAtATimeReader returns one byte per Read so grapheme clusters are split
// across buffer fills exactly where a pause or a slow pty would split them.
type byteAtATimeReader struct {
	data []byte
	off  int
}

func (r *byteAtATimeReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.off]
	r.off++
	return 1, nil
}

func TestGraphemeClustersSplitAcrossReads(t *testing.T) {
	cases := []struct {
		text  string
		width int
	}{
		{"👨\u200d👩", 2},
		{"👨\u200d👩\u200d👧\u200d👦", 2},
		{"👩\u200d💻", 2},
		{"🏳️\u200d🌈", 2},
		{"🇺🇸", 2},
		{"👍🏽", 2},
		{"1\ufe0f\u20e3", 2},
		{"e\u0301", 1},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			backend := NewNoPTYBackend(&byteAtATimeReader{data: []byte(tc.text)}, io.Discard)
			term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
			_ = term.Resize(40, 3)
			defer func() { _ = term.Close() }()

			<-term.Done()

			if got := strings.TrimRight(term.Line(0), " "); got != tc.text {
				t.Fatalf("line = %q, want %q", got, tc.text)
			}
			if width := term.CursorPos().X; width != tc.width {
				t.Fatalf("%q advanced %d cells, want %d (spans: %+v)", tc.text, width, tc.width, term.StyledLine(0, 8, 0).Spans)
			}
		})
	}
}

func TestRuneModeZWJMergesAcrossReads(t *testing.T) {
	text := "👨\u200d👩\u200d👧\u200d👦"
	backend := NewNoPTYBackend(&byteAtATimeReader{data: []byte(text)}, io.Discard)
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeRune)
	_ = term.Resize(40, 3)
	defer func() { _ = term.Close() }()
	<-term.Done()
	if got := strings.TrimRight(term.Line(0), " "); got != text {
		t.Fatalf("line = %q, want %q", got, text)
	}
	if got := term.CursorPos().X; got != 2 {
		t.Fatalf("%q advanced %d cells in rune mode, want 2 (spans: %+v)", text, got, term.StyledLine(0, 8, 0).Spans)
	}
}

func TestInvalidUTF8IsSanitized(t *testing.T) {
	backend := NewNoPTYBackend(&byteAtATimeReader{data: []byte{'a', 0xff, 'b', 0xc0, 0x80, 0xe2, 0x82, 'c'}}, io.Discard)
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
	_ = term.Resize(40, 3)
	defer func() { _ = term.Close() }()
	<-term.Done()
	line := term.Line(0)
	if !utf8.ValidString(line) {
		t.Fatalf("line contains invalid UTF-8: %q", line)
	}
	if !strings.ContainsRune(line, utf8.RuneError) {
		t.Fatalf("expected replacement characters, got %q", line)
	}
}
