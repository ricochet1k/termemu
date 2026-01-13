package termemu

import (
	"io"
	"strconv"
	"sync"
	"testing"
)

const (
	benchWidth   = 160
	benchHeight  = 50
	benchSegSize = 8
)

type benchReader struct {
	data  []byte
	start <-chan struct{}
	done  chan struct{}
	once  sync.Once
}

func (r *benchReader) Read(p []byte) (int, error) {
	<-r.start
	if len(r.data) == 0 {
		r.once.Do(func() { close(r.done) })
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		r.once.Do(func() { close(r.done) })
		return n, io.EOF
	}
	return n, nil
}

func runTerminalBench(b *testing.B, payloadBuilder func(multiplier int) []byte, autoWrap bool, newFn func(Frontend) screen) {
	b.ReportAllocs()

	multiplier := 10 * b.N
	payload := payloadBuilder(multiplier)
	b.SetBytes(int64(len(payload)) / int64(b.N))

	b.StopTimer()
	start := make(chan struct{})
	done := make(chan struct{})
	reader := &benchReader{data: payload, start: start, done: done}
	backend := NewNoPTYBackend(reader, io.Discard)
	tln := newBenchTerminal(&EmptyFrontend{}, backend, TextReadModeRune, newFn)
	if tln == nil {
		b.Fatal("newBenchTerminal returned nil")
	}
	if err := tln.Resize(benchWidth, benchHeight); err != nil {
		b.Fatalf("Resize failed: %v", err)
	}
	tln.screen().SetAutoWrap(autoWrap)
	b.StartTimer()
	close(start)
	<-done
	<-tln.readLoopDone
}

func newBenchTerminal(frontend Frontend, backend Backend, mode TextReadMode, newFn func(Frontend) screen) *terminal {
	if frontend == nil {
		frontend = &EmptyFrontend{}
	}
	if backend == nil {
		return nil
	}
	term := &terminal{
		frontend:     frontend,
		mainScreen:   newFn(frontend),
		altScreen:    newFn(frontend),
		backend:      backend,
		viewFlags:    make([]bool, viewFlagCount),
		viewInts:     make([]int, viewIntCount),
		viewStrings:  make([]string, viewStringCount),
		textReadMode: mode,
	}
	term.startReadLoop()
	return term
}

func makeBenchLineBytes(w int) []byte {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	line := make([]byte, w)
	for i := 0; i < w; i++ {
		line[i] = alphabet[i%len(alphabet)]
	}
	return line
}

func appendCursor(dst []byte, x, y int) []byte {
	dst = append(dst, '\x1b', '[')
	dst = strconv.AppendInt(dst, int64(y), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(x), 10)
	dst = append(dst, 'H')
	return dst
}

func appendColor(dst []byte, fg, bg int) []byte {
	dst = append(dst, '\x1b', '[')
	dst = strconv.AppendInt(dst, int64(fg), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(bg), 10)
	dst = append(dst, 'm')
	return dst
}

func buildPlainPayload(lines int) []byte {
	line := makeBenchLineBytes(benchWidth)
	buf := make([]byte, 0, (benchWidth+16)*lines)
	for y := 0; y < lines; y++ {
		buf = appendCursor(buf, 1, y+1)
		buf = append(buf, line...)
	}
	return buf
}

func buildWrapPayload(lines int) []byte {
	line := makeBenchLineBytes(benchWidth)
	buf := make([]byte, 0, benchWidth*lines+16)
	buf = appendCursor(buf, 1, 1)
	for i := 0; i < lines; i++ {
		buf = append(buf, line...)
	}
	return buf
}

func buildStyledPayload(lines int, cursorPerLine bool) []byte {
	line := makeBenchLineBytes(benchWidth)
	buf := make([]byte, 0, (benchWidth+32)*lines)
	fg := []int{37, 33}
	bg := []int{40, 44}
	if !cursorPerLine {
		buf = appendCursor(buf, 1, 1)
	}
	for y := 0; y < lines; y++ {
		if cursorPerLine {
			buf = appendCursor(buf, 1, y+1)
		}
		for x := 0; x < benchWidth; x += benchSegSize {
			idx := (x / benchSegSize) & 1
			buf = appendColor(buf, fg[idx], bg[idx])
			end := x + benchSegSize
			if end > benchWidth {
				end = benchWidth
			}
			buf = append(buf, line[x:end]...)
		}
	}
	return buf
}

func buildRandomUpdatesPayload(updates int) []byte {
	const lcgMul = uint32(1664525)
	const lcgAdd = uint32(1013904223)
	buf := make([]byte, 0, updates*16)
	seed := uint32(1)
	for i := 0; i < updates; i++ {
		seed = seed*lcgMul + lcgAdd
		x := int(seed % benchWidth)
		seed = seed*lcgMul + lcgAdd
		y := int(seed % benchHeight)
		buf = appendCursor(buf, x+1, y+1)
		buf = append(buf, 'x')
	}
	return buf
}

func BenchmarkTerminalWritePlain(b *testing.B) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			runTerminalBench(b, func(multiplier int) []byte {
				return buildPlainPayload(benchHeight * multiplier)
			}, false, factory.new)
		})
	}
}

func BenchmarkTerminalWritePlainWrapScroll(b *testing.B) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			runTerminalBench(b, func(multiplier int) []byte {
				return buildWrapPayload(benchHeight * 2 * multiplier)
			}, true, factory.new)
		})
	}
}

func BenchmarkTerminalWriteStyled(b *testing.B) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			runTerminalBench(b, func(multiplier int) []byte {
				return buildStyledPayload(benchHeight*multiplier, true)
			}, false, factory.new)
		})
	}
}

func BenchmarkTerminalWriteStyledWrapScroll(b *testing.B) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			runTerminalBench(b, func(multiplier int) []byte {
				return buildStyledPayload(benchHeight*2*multiplier, false)
			}, true, factory.new)
		})
	}
}

func BenchmarkTerminalRandomCellUpdates(b *testing.B) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			runTerminalBench(b, func(multiplier int) []byte {
				return buildRandomUpdatesPayload(benchWidth * benchHeight * multiplier)
			}, false, factory.new)
		})
	}
}
