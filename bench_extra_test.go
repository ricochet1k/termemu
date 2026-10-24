package termemu

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"
)

// This file adds benchmarks for the paths that screen_bench_test.go does not
// cover: scrollback storage/reflow, snapshot/restore, resize, the ANSI read
// path, escape parsing, and retained memory. Run everything with:
//
//	go test -run '^$' -bench . -benchmem .
//
// or use scripts/bench.sh to also collect CPU/memory profiles.

func newBenchTermOnly(b *testing.B) *terminal {
	b.Helper()
	t := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	if t == nil {
		b.Fatal("newTerminal returned nil")
	}
	return t
}

var benchAlphabet = []byte("abcdefghijklmnopqrstuvwxyz")

func benchLine(w int, offset int) string {
	buf := make([]byte, w)
	for i := range buf {
		buf[i] = benchAlphabet[(i+offset)%len(benchAlphabet)]
	}
	return string(buf)
}

// fillBenchScreen writes a full screen of text, alternating plain and styled
// segments so span packing and style handling are both exercised.
func fillBenchScreen(s screen, w, h int, styled bool) {
	s.setSize(w, h)
	styleA := NewStyle()
	styleB := NewStyle()
	_ = styleA.SetColor(ComponentFG, RGB(200, 120, 40))
	_ = styleB.SetColor(ComponentBG, RGB(10, 20, 30))
	styleB.SetMode(ModeBold)
	for y := 0; y < h; y++ {
		s.setCursorPos(0, y)
		line := benchLine(w, y)
		if !styled {
			s.writeString(line, w, false, TextReadModeRune)
			continue
		}
		for x := 0; x < w; x += benchSegSize {
			end := x + benchSegSize
			if end > w {
				end = w
			}
			if (x/benchSegSize)&1 == 0 {
				s.setStyle(styleA)
			} else {
				s.setStyle(styleB)
			}
			s.writeString(line[x:end], end-x, false, TextReadModeRune)
		}
	}
	s.setCursorPos(0, 0)
}

func BenchmarkScreenSnapshot(b *testing.B) {
	for _, styled := range []bool{false, true} {
		name := "plain"
		if styled {
			name = "styled"
		}
		b.Run(name, func(b *testing.B) {
			s := newSpanScreen(&EmptyFrontend{})
			fillBenchScreen(s, benchWidth, benchHeight, styled)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = s.snapshotState()
			}
		})
	}
}

func BenchmarkScreenRestore(b *testing.B) {
	for _, styled := range []bool{false, true} {
		name := "plain"
		if styled {
			name = "styled"
		}
		b.Run(name, func(b *testing.B) {
			s := newSpanScreen(&EmptyFrontend{})
			fillBenchScreen(s, benchWidth, benchHeight, styled)
			snap := s.snapshotState()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.restoreState(snap)
			}
		})
	}
}

func BenchmarkTerminalSnapshot(b *testing.B) {
	t := newBenchTermOnly(b)
	if err := t.Resize(benchWidth, benchHeight); err != nil {
		b.Fatal(err)
	}
	fillBenchScreen(t.mainScreen, benchWidth, benchHeight, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = t.Snapshot()
	}
}

func BenchmarkTerminalRestore(b *testing.B) {
	t := newBenchTermOnly(b)
	if err := t.Resize(benchWidth, benchHeight); err != nil {
		b.Fatal(err)
	}
	fillBenchScreen(t.mainScreen, benchWidth, benchHeight, true)
	snap := t.Snapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = t.Restore(snap)
	}
}

func BenchmarkTerminalResizeFullScreen(b *testing.B) {
	t := newBenchTermOnly(b)
	if err := t.Resize(benchWidth, benchHeight); err != nil {
		b.Fatal(err)
	}
	fillBenchScreen(t.mainScreen, benchWidth, benchHeight, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Alternate between two sizes so every iteration actually reflows.
		if i&1 == 0 {
			_ = t.Resize(benchWidth/2, benchHeight/2)
		} else {
			_ = t.Resize(benchWidth, benchHeight)
		}
	}
}

func BenchmarkANSILine(b *testing.B) {
	for _, styled := range []bool{false, true} {
		name := "plain"
		if styled {
			name = "styled"
		}
		b.Run(name, func(b *testing.B) {
			s := newSpanScreen(&EmptyFrontend{})
			fillBenchScreen(s, benchWidth, benchHeight, styled)
			b.SetBytes(int64(benchWidth))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = s.renderLineANSI(i % benchHeight)
			}
		})
	}
}

func BenchmarkStyledLinesRegion(b *testing.B) {
	s := newSpanScreen(&EmptyFrontend{})
	fillBenchScreen(s, benchWidth, benchHeight, true)
	r := Region{X: 0, Y: 0, X2: benchWidth, Y2: benchHeight}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.StyledLines(r)
	}
}

func BenchmarkTTYRenderRegion(b *testing.B) {
	t := newBenchTermOnly(b)
	if err := t.Resize(benchWidth, benchHeight); err != nil {
		b.Fatal(err)
	}
	fillBenchScreen(t.mainScreen, benchWidth, benchHeight, true)
	f := NewTTYFrontend(io.Discard)
	f.SetTerminal(t)
	region := Region{X: 0, Y: 0, X2: benchWidth, Y2: benchHeight}
	f.Attach(region)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t.View(func(v LockedView) {
			f.RegionChanged(v, region, CRText)
		})
	}
}

func BenchmarkHandleCommand(b *testing.B) {
	cases := []struct {
		name string
		cmd  []byte
	}{
		{"SGR", []byte("\x1b[1;38;2;200;120;40;48;5;17m")},
		{"Cursor", []byte("\x1b[12;40H")},
		{"Erase", []byte("\x1b[2J")},
		{"OSC8", []byte("\x1b]8;;https://example.com/x\x1b\\")},
		{"OSC0", []byte("\x1b]0;benchmark title\x07")},
	}
	t := newBenchTermOnly(b)
	fillBenchScreen(t.mainScreen, benchWidth, benchHeight, true)
	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.cmd)))
			b.ReportAllocs()
			b.ResetTimer()
			var r bytes.Reader
			for i := 0; i < b.N; i++ {
				r.Reset(c.cmd)
				_ = t.handleCommand(&r)
			}
		})
	}
}

// fillBenchScrollback appends rows of scrollback directly, marking a fraction
// as wrapped so LogicalLine has to reflow.
func fillBenchScrollback(t *terminal, rows, width, wrapEvery int) {
	lines := make([]spanLine, 0, rows)
	wrapped := make([]bool, 0, rows)
	for i := 0; i < rows; i++ {
		var spans []Span
		spans = append(spans, Span{Text: benchLine(width, i), Width: width})
		lines = append(lines, spanLine{spans: spans, width: width})
		wrapped = append(wrapped, wrapEvery > 0 && i%wrapEvery == 0)
	}
	t.Lock()
	t.captureScrollRows(lines, wrapped)
	t.Unlock()
}

func BenchmarkScrollbackCapture(b *testing.B) {
	t := newBenchTermOnly(b)
	fillBenchScrollback(t, DefaultScrollbackRows, benchWidth, 2)
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(benchWidth, benchHeight)
	line := benchLine(benchWidth, 0)
	lines := []spanLine{{spans: []Span{{Text: line, Width: benchWidth}}, width: benchWidth}}
	wrapped := []bool{false}
	b.SetBytes(int64(benchWidth))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t.Lock()
		t.captureScrollRows(lines, wrapped)
		t.Unlock()
	}
}

func BenchmarkScrollbackRow(b *testing.B) {
	t := newBenchTermOnly(b)
	fillBenchScrollback(t, DefaultScrollbackRows, benchWidth, 2)
	sb := t.Scrollback()
	base, next := sb.Range()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := base + ScrollID(i%int(next-base))
		_, _ = sb.Row(id)
	}
}

func BenchmarkScrollbackLogicalLine(b *testing.B) {
	t := newBenchTermOnly(b)
	fillBenchScrollback(t, DefaultScrollbackRows, benchWidth, 2)
	sb := t.Scrollback()
	base, next := sb.Range()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := base + ScrollID(i%int(next-base))
		_ = sb.LogicalLine(id)
	}
}

func BenchmarkScrollbackSnapshot(b *testing.B) {
	t := newBenchTermOnly(b)
	fillBenchScrollback(t, DefaultScrollbackRows, benchWidth, 2)
	sb := t.Scrollback()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sb.Snapshot()
	}
}

// TestBenchScrollbackSink keeps benchmark allocations from being optimized
// away where the compiler can see the result is unused.
var (
	benchScreenSink   screen
	benchSnapshotSink Snapshot
)

// BenchmarkScreenMemoryRetained reports heap bytes retained by one fully
// populated 160x50 screen. It is deliberately not scaled by b.N: the measured
// quantity is per-screen retention, not throughput.
func BenchmarkScreenMemoryRetained(b *testing.B) {
	for _, styled := range []bool{false, true} {
		name := "plain"
		if styled {
			name = "styled"
		}
		b.Run(name, func(b *testing.B) {
			measureScreenRetained(b, styled)
		})
	}
}

func measureScreenRetained(b *testing.B, styled bool) {
	const numScreens = 200
	slots := make([]screen, numScreens)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < numScreens; i++ {
		s := newSpanScreen(&EmptyFrontend{})
		fillBenchScreen(s, benchWidth, benchHeight, styled)
		slots[i] = s
	}
	benchScreenSink = slots[0]
	runtime.GC()
	runtime.ReadMemStats(&after)
	// Keep the slots slice live across the GC above; otherwise liveness
	// analysis can declare it dead after the loop and the measurement sees
	// nothing retained.
	runtime.KeepAlive(slots)
	perScreen := float64(after.HeapAlloc-before.HeapAlloc) / float64(numScreens)
	benchScreenSink = nil
	slots = nil

	b.ReportAllocs()
	b.ResetTimer()
	// ReportMetric must come after ResetTimer: ResetTimer clears custom metrics.
	b.ReportMetric(perScreen, "B/screen")
	b.ReportMetric(perScreen/float64(benchWidth*benchHeight), "B/cell")
	for i := 0; i < b.N; i++ {
		s := newSpanScreen(&EmptyFrontend{})
		fillBenchScreen(s, benchWidth, benchHeight, styled)
		benchScreenSink = s
	}
	benchScreenSink = nil
}

// BenchmarkTerminalWriteModes compares rune and grapheme read modes on the
// same input. Grapheme mode is the default and does the full clustering work;
// rune mode is cheaper but only correct for text without combining sequences.
func BenchmarkTerminalWriteModes(b *testing.B) {
	modes := []struct {
		name string
		mode TextReadMode
	}{
		{"rune", TextReadModeRune},
		{"grapheme", TextReadModeGrapheme},
	}
	payloads := []struct {
		name  string
		build func(multiplier int) []byte
		wrap  bool
	}{
		{"plain", func(multiplier int) []byte { return buildPlainPayload(benchHeight * multiplier) }, false},
		{"styled", func(multiplier int) []byte { return buildStyledPayload(benchHeight*multiplier, true) }, false},
		{"plain_wrap", func(multiplier int) []byte { return buildWrapPayload(benchHeight * 2 * multiplier) }, true},
		{"emoji_wrap", func(multiplier int) []byte { return buildEmojiPayload(benchHeight * multiplier) }, true},
	}
	for _, p := range payloads {
		for _, m := range modes {
			p, m := p, m
			b.Run(p.name+"/"+m.name, func(b *testing.B) {
				for _, factory := range screenFactories() {
					factory := factory
					b.Run(factory.name, func(b *testing.B) {
						runTerminalBenchMode(b, p.build, p.wrap, m.mode, factory.new)
					})
				}
			})
		}
	}
}

// buildEmojiPayload builds wrapped output dominated by graphemes that need
// clustering: combining marks, keycaps, ZWJ sequences, flags, and skin tones.
func buildEmojiPayload(lines int) []byte {
	unit := "e\u0301 1\ufe0f\u20e3 👨\u200d👩\u200d👧 🇺🇸👍🏽 "
	line := strings.Repeat(unit, benchWidth/8+1)
	buf := make([]byte, 0, len(line)*lines+16)
	buf = appendCursor(buf, 1, 1)
	for i := 0; i < lines; i++ {
		buf = append(buf, line...)
	}
	return buf
}

// BenchmarkGraphemeReaderASCIIModes compares the tokenizer's ASCII throughput
// in both read modes.
func BenchmarkGraphemeReaderASCIIModes(b *testing.B) {
	data := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog 0123456789\n", 400))
	for _, m := range []struct {
		name string
		mode TextReadMode
	}{{"rune", TextReadModeRune}, {"grapheme", TextReadModeGrapheme}} {
		m := m
		b.Run(m.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				gr := NewGraphemeReaderWithMode(bytes.NewReader(data), m.mode)
				for {
					text, _, _, err := gr.ReadPrintableBytes(0)
					if err == io.EOF {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
					if text == "" {
						if _, err := gr.ReadByte(); err == io.EOF {
							break
						} else if err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}
