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
		{"000\u077b\u1936", 5},
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

func TestInvalidUTF8FollowedBySpacingMarkChunkInvariant(t *testing.T) {
	data := []byte("\xf9\u1936")
	backend := NewNoPTYBackend(&byteAtATimeReader{data: data}, io.Discard)
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
	_ = term.Resize(40, 3)
	defer func() { _ = term.Close() }()
	<-term.Done()

	if got := strings.TrimRight(term.Line(0), " "); got != "\ufffd\u1936" {
		t.Fatalf("line = %q, want replacement + U+1936", got)
	}
	if width := term.CursorPos().X; width != 2 {
		t.Fatalf("width = %d, want 2", width)
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

// TestRuneModeASCIIFastPathChunking guards the printable-ASCII shortcut in
// ReadPrintableBytes: the shortcut skips the per-rune tokenizer when the next
// byte is also printable ASCII, but must keep matching the tokenizer path when
// a selector/emoji follows the ASCII run or reads split at any byte.
func TestRuneModeASCIIFastPathChunking(t *testing.T) {
	run := func(data []byte, chunk int) (string, int) {
		t.Helper()
		var r io.Reader
		if chunk == 0 {
			r = strings.NewReader(string(data))
		} else {
			r = &chunkReader{data: append([]byte(nil), data...), chunk: chunk}
		}
		term := NewWithMode(NewMockFrontend(), NewNoPTYBackend(r, io.Discard), TextReadModeRune)
		_ = term.Resize(40, 3)
		defer func() { _ = term.Close() }()
		<-term.Done()
		return term.Line(0), term.CursorPos().X
	}

	cases := []string{
		"hello",
		"x y z",
		"1\ufe0f\u20e3",
		"a1\ufe0f",
		"abc👨\u200d👩",
		"12👍🏽",
		"!\ufe0f",
		"~1\ufe0f",
	}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			wantLine, wantX := run([]byte(text), 0)
			for _, chunk := range []int{1, 3, 7} {
				gotLine, gotX := run([]byte(text), chunk)
				if gotLine != wantLine || gotX != wantX {
					t.Fatalf("chunk %d: line/cursor (%q,%d), want (%q,%d)", chunk, gotLine, gotX, wantLine, wantX)
				}
			}
		})
	}
}
