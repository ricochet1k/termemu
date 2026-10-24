package termemu

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"unicode/utf8"
)

// chunkReader serves data in fixed-size chunks. Fuzzing with different chunk
// sizes exercises grapheme clusters, UTF-8 sequences, and escape commands split
// across read boundaries.
type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.chunk
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// FuzzTerminalInputChunking checks that the final terminal state does not
// depend on where reads end: a pty can split text, escape commands, and
// grapheme clusters at any byte boundary.
func FuzzTerminalInputChunking(f *testing.F) {
	seeds := [][]byte{
		[]byte("hello"),
		[]byte("👨\u200d👩\u200d👧\u200d👦"),
		[]byte("e\u0301x"),
		[]byte("1\ufe0f\u20e3"),
		[]byte("🇺🇸👍🏽"),
		[]byte("🇸0"),
		[]byte("a\x1b[31mred\x1b[0m"),
		[]byte("a\x00b\tc\r\nd"),
		[]byte{'a', 0xff, 'b', 0xc0, 0x80, 'c'},
		[]byte("\xe0\xe0\xe0\x7f00"),
		[]byte("\xf9\u1936"),
		[]byte("\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mode := range []TextReadMode{TextReadModeGrapheme, TextReadModeRune} {
			whole := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
			feedTerminalReader(t, whole, bytes.NewReader(data), mode)
			want := whole.Snapshot()

			for _, chunk := range []int{1, 3, 7} {
				got := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
				feedTerminalReader(t, got, &chunkReader{data: append([]byte(nil), data...), chunk: chunk}, mode)
				gotSnap := got.Snapshot()
				if !snapshotsEquivalent(want, gotSnap) {
					t.Fatalf("mode %d chunk %d: final state differs for input %q\nwhole: %s\nchunked: %s",
						mode, chunk, data, snapshotSummary(want), snapshotSummary(gotSnap))
				}
			}
		}
	})
}

// linesEquivalentUnderChunking is Line.Equivalent except that the OSC 8 link
// on a blank/space cell is ignored. A space written as part of a text run can
// carry the active link, while the same space written alone is canonicalized
// as a blank span without one; that representation difference is not visible.
func linesEquivalentUnderChunking(a, b Line) bool {
	if a.Width != b.Width {
		return false
	}
	left := lineCellWalker{line: &a}
	right := lineCellWalker{line: &b}
	for {
		x, okX := left.next()
		y, okY := right.next()
		if okX != okY {
			return false
		}
		if !okX {
			return true
		}
		if x.pos != y.pos || x.width != y.width || x.text != y.text || x.style != y.style {
			return false
		}
		if x.text != " " && x.link != y.link {
			return false
		}
	}
}

// snapshotsEquivalent compares screen state semantically, ignoring span
// packing: chunk boundaries legitimately change how many spans a text run is
// stored in, but not the cells or their styles.
func snapshotsEquivalent(a, b Snapshot) bool {
	if len(a.Main.Lines) != len(b.Main.Lines) || len(a.Alt.Lines) != len(b.Alt.Lines) {
		return false
	}
	for i := range a.Main.Lines {
		if !linesEquivalentUnderChunking(a.Main.Lines[i], b.Main.Lines[i]) {
			return false
		}
	}
	for i := range a.Alt.Lines {
		if !linesEquivalentUnderChunking(a.Alt.Lines[i], b.Alt.Lines[i]) {
			return false
		}
	}
	a.Main.Lines, b.Main.Lines = nil, nil
	a.Alt.Lines, b.Alt.Lines = nil, nil
	return reflect.DeepEqual(a, b)
}

func snapshotSummary(s Snapshot) string {
	return fmt.Sprintf("alt=%v cursor=%+v style=%#v margins=%d..%d wrap=%v pending=%v main0=%q alt0=%q",
		s.OnAltScreen, s.Main.Cursor, s.Main.Style, s.Main.TopMargin, s.Main.BottomMargin,
		s.Main.AutoWrap, s.Main.PendingWrap, s.Main.Lines[0].PlainTextString(), s.Alt.Lines[0].PlainTextString())
}

// feedTerminalReader feeds r through the real read path until it reaches EOF.
func feedTerminalReader(t *testing.T, term *terminal, r io.Reader, mode TextReadMode) {
	t.Helper()
	state := &readLoopState{gr: NewGraphemeReaderWithMode(r, mode)}
	for {
		err := term.ptyReadOne(state)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		t.Fatalf("feed: %v", err)
	}
}

// spanLineFromText builds a line whose spans follow grapheme/rune cluster
// boundaries, using the same width rules as the reader.
func spanLineFromText(text string, mode TextReadMode, style Style) spanLine {
	text = sanitizeUTF8(text)
	var line spanLine
	state := -1
	b := []byte(text)
	for len(b) > 0 {
		cluster, consumed, width, newState, ok := stepTextCluster(b, state, mode)
		if !ok || consumed <= 0 {
			break
		}
		if width < 0 {
			width = 0
		}
		line.spans = append(line.spans, Span{Style: style, Text: string(cluster), Width: width})
		line.width += width
		b = b[consumed:]
		state = newState
	}
	return line
}

func checkSpanLineStructure(t *testing.T, line *spanLine, context string) {
	t.Helper()
	sum := 0
	for i, sp := range line.spans {
		if sp.Width < 0 {
			t.Fatalf("%s: span %d has negative width %#v", context, i, sp)
		}
		if !utf8.ValidString(sp.Text) {
			t.Fatalf("%s: span %d has invalid UTF-8 text %q", context, i, sp.Text)
		}
		sum += sp.Width
	}
	if line.width != sum {
		t.Fatalf("%s: line.width = %d, sum(span.Width) = %d (spans %#v)", context, line.width, sum, line.spans)
	}
}

// FuzzReplaceRangeInvariants hammers span splitting/replacement with arbitrary
// text, including wide characters, combining marks, and invalid UTF-8.
func FuzzReplaceRangeInvariants(f *testing.F) {
	f.Add([]byte("hello"), []byte("x"), uint16(1), uint8(2), false)
	f.Add([]byte("世界abc"), []byte("🐹"), uint16(2), uint8(1), true)
	f.Add([]byte("e\u0301x"), []byte("\u0301"), uint16(1), uint8(0), true)
	f.Add([]byte("1\ufe0f\u20e3"), []byte("2\ufe0f\u20e3"), uint16(0), uint8(2), true)
	f.Add([]byte("a\xffb"), []byte("\xff"), uint16(2), uint8(1), false)

	f.Fuzz(func(t *testing.T, initial, insert []byte, x uint16, n uint8, grapheme bool) {
		mode := TextReadModeRune
		if grapheme {
			mode = TextReadModeGrapheme
		}
		style := NewStyle()
		line := spanLineFromText(string(initial), mode, style)
		checkSpanLineStructure(t, &line, "initial")

		pos := 0
		if line.width > 0 {
			pos = int(x) % (line.width + 1)
		}
		del := int(n) % (line.width + 1)

		var ins Span
		if len(insert) > 0 {
			built := spanLineFromText(string(insert), mode, style)
			ins = Span{Style: style, Text: Line{Spans: built.spans}.PlainTextString(), Width: built.width}
		}

		context := "replaceRange"
		replaceRange(&line, pos, del, ins, mode)
		checkSpanLineStructure(t, &line, context)
	})
}

// FuzzLineEquivalentPacking checks the semantic line comparison is independent
// of how the same cells are packed into spans.
func FuzzLineEquivalentPacking(f *testing.F) {
	f.Add([]byte("abc"), uint8(1))
	f.Add([]byte("世界"), uint8(1))
	f.Add([]byte("e\u0301x"), uint8(2))

	f.Fuzz(func(t *testing.T, text []byte, splitSeed uint8) {
		text = []byte(sanitizeUTF8(string(text)))
		mode := TextReadModeGrapheme
		style := NewStyle()

		whole := spanLineFromText(string(text), mode, style)
		// Repack by splitting at a deterministic offset; the cell content is
		// unchanged, so Equivalent must still hold.
		var repacked spanLine
		for _, sp := range whole.spans {
			if sp.Width > 0 {
				offset := int(splitSeed)%sp.Width + 1
				if offset >= sp.Width {
					repacked.spans = append(repacked.spans, sp)
				} else {
					left, right, wide := splitSpan(sp, offset, mode)
					if wide.Width > 0 {
						// Splitting inside a wide cluster is not a valid
						// repacking; keep the cluster intact.
						repacked.spans = append(repacked.spans, sp)
					} else {
						if left.Width > 0 {
							repacked.spans = append(repacked.spans, left)
						}
						if right.Width > 0 {
							repacked.spans = append(repacked.spans, right)
						}
					}
				}
				repacked.width += sp.Width
				if offset%2 == 0 {
					splitSeed++
				}
				continue
			}
			repacked.spans = append(repacked.spans, sp)
		}

		a := Line{Spans: whole.spans, Width: whole.width}
		b := Line{Spans: repacked.spans, Width: repacked.width}
		if !a.Equivalent(b) {
			t.Fatalf("repacked line is not Equivalent: whole=%#v repacked=%#v", a, b)
		}
	})
}
