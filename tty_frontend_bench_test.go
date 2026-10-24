package termemu

import (
	"bytes"
	"io"
	"strconv"
	"testing"
)

// This file benchmarks the TTYFrontend rendering path with workloads that
// resemble interactive programs: escape-dense styled runs, an unchanged
// full-screen repaint, and a scrolled full screen.

func newTTYBenchmarkTerm(b *testing.B, w, h int) (*terminal, *TTYFrontend) {
	b.Helper()
	f := NewTTYFrontend(io.Discard)
	term := newTerminal(f, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	f.SetTerminal(term)
	if err := term.Resize(w, h); err != nil {
		b.Fatal(err)
	}
	f.Attach(Region{X: 0, Y: 0, X2: w, Y2: h})
	return term, f
}

// buildStyledRunsPayload paints one row as many small, differently styled
// runs (like syntax highlighting) and leaves the cursor at the start.
func buildStyledRunsPayload(w int) []byte {
	var buf bytes.Buffer
	buf.WriteByte('\r')
	for x := 0; x < w; x += 4 {
		buf.WriteString("\x1b[")
		buf.WriteString(strconv.Itoa(30 + (x/4)%8))
		buf.WriteByte(';')
		buf.WriteString(strconv.Itoa(40 + (x/4)%8))
		buf.WriteString("mabc")
		if x+3 >= w {
			buf.WriteString("\x1b[0m")
			break
		}
		buf.WriteByte('d')
	}
	return buf.Bytes()
}

// buildFullRepaintPayload paints the whole screen with the same styled-run
// pattern every row, homing the cursor first.
func buildFullRepaintPayload(w, h int) []byte {
	row := buildStyledRunsPayload(w)
	var buf bytes.Buffer
	for y := 0; y < h; y++ {
		buf.WriteString("\x1b[")
		buf.WriteString(strconv.Itoa(y + 1))
		buf.WriteString(";1H")
		buf.Write(row[1:]) // drop the leading carriage return
	}
	buf.WriteString("\x1b[H")
	return buf.Bytes()
}

func BenchmarkTTYRenderSmallRuns(b *testing.B) {
	payload := buildStyledRunsPayload(benchWidth)
	term, _ := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(payload, TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := term.testFeedTerminalInputFromBackend(payload, TextReadModeGrapheme); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTTYRenderRepaintUnchanged(b *testing.B) {
	payload := buildFullRepaintPayload(benchWidth, benchHeight)
	term, _ := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(payload, TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := term.testFeedTerminalInputFromBackend(payload, TextReadModeGrapheme); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTTYRenderScrolled(b *testing.B) {
	payload := buildFullRepaintPayload(benchWidth, benchHeight)
	term, _ := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(payload, TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	// Model a scrolling program: one line leaves the top and a freshly
	// styled line is painted at the bottom.
	row := buildStyledRunsPayload(benchWidth)[1:]
	var scroll []byte
	scroll = append(scroll, "\x1b[1S"...)
	scroll = append(scroll, "\x1b["+strconv.Itoa(benchHeight)+";1H"...)
	scroll = append(scroll, row...)
	b.SetBytes(int64(len(scroll)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := term.testFeedTerminalInputFromBackend(scroll, TextReadModeGrapheme); err != nil {
			b.Fatal(err)
		}
	}
}

// benchBatchFrontend is the optional batching contract, declared locally so
// these benchmarks compile against both per-callback and batching frontends.
type benchBatchFrontend interface {
	PrepareBatch(LockedView)
	WriteBatch()
}

// benchFlushFrame ends a simulated read-loop batch. Before batching existed
// the callbacks had already rendered, so the flush is a no-op.
func benchFlushFrame(term *terminal, f *TTYFrontend) {
	b, ok := interface{}(f).(benchBatchFrontend)
	if !ok {
		return
	}
	term.View(func(v LockedView) { b.PrepareBatch(v) })
	b.WriteBatch()
}

func BenchmarkTTYRenderFrameSmallRuns(b *testing.B) {
	term, f := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(buildFullRepaintPayload(benchWidth, benchHeight), TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	regions := make([]Region, 0, benchWidth/4)
	for x := 0; x < benchWidth; x += 4 {
		regions = append(regions, Region{X: x, Y: 0, X2: min(x+4, benchWidth), Y2: 1})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.View(func(v LockedView) {
			for _, r := range regions {
				f.RegionChanged(v, r, CRText)
			}
		})
		benchFlushFrame(term, f)
	}
}

func BenchmarkTTYRenderFrameRepaint(b *testing.B) {
	term, f := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(buildFullRepaintPayload(benchWidth, benchHeight), TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	region := Region{X: 0, Y: 0, X2: benchWidth, Y2: benchHeight}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.View(func(v LockedView) { f.RegionChanged(v, region, CRText) })
		benchFlushFrame(term, f)
	}
}

func BenchmarkTTYRenderFrameScrolled(b *testing.B) {
	term, f := newTTYBenchmarkTerm(b, benchWidth, benchHeight)
	if err := term.testFeedTerminalInputFromBackend(buildFullRepaintPayload(benchWidth, benchHeight), TextReadModeGrapheme); err != nil {
		b.Fatal(err)
	}
	region := Region{X: 0, Y: 0, X2: benchWidth, Y2: benchHeight}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.View(func(v LockedView) { f.RegionChanged(v, region, CRScroll) })
		benchFlushFrame(term, f)
	}
}
