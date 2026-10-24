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

// benchIterMultiplier fixes the amount of work one benchmark iteration does:
// payloadBuilder(benchIterMultiplier) bytes of terminal output. It is
// deliberately independent of b.N, so ns/op, MB/s and allocs/op are all per
// fixed-size payload rather than amortized over a payload sized from a stale
// b.N.
const benchIterMultiplier = 10

// benchReader streams the same fixed payload once per benchmark iteration,
// repeats times in total. Setup (terminal construction, resize, backend) is
// paid once and is therefore negligible per iteration.
type benchReader struct {
	payload []byte
	repeats int
	offset  int
	start   <-chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (r *benchReader) Read(p []byte) (int, error) {
	<-r.start
	n := 0
	for n < len(p) && r.repeats > 0 {
		if r.offset == len(r.payload) {
			r.offset = 0
			r.repeats--
			if r.repeats == 0 {
				break
			}
		}
		copied := copy(p[n:], r.payload[r.offset:])
		n += copied
		r.offset += copied
	}
	if n > 0 {
		return n, nil
	}
	r.once.Do(func() { close(r.done) })
	return 0, io.EOF
}

func runTerminalBench(b *testing.B, payloadBuilder func(multiplier int) []byte, autoWrap bool, newFn func(Frontend) screen) {
	runTerminalBenchMode(b, payloadBuilder, autoWrap, TextReadModeRune, newFn)
}

func runTerminalBenchMode(b *testing.B, payloadBuilder func(multiplier int) []byte, autoWrap bool, mode TextReadMode, newFn func(Frontend) screen) {
	b.ReportAllocs()

	// One iteration is exactly one copy of this fixed-size payload. Building
	// it before the timer and streaming it b.N times keeps the per-iteration
	// work (and therefore the reported numbers) independent of b.N.
	payload := payloadBuilder(benchIterMultiplier)
	b.SetBytes(int64(len(payload)))

	b.StopTimer()
	start := make(chan struct{})
	done := make(chan struct{})
	reader := &benchReader{payload: payload, repeats: b.N, start: start, done: done}
	backend := NewNoPTYBackend(reader, io.Discard)
	tln := newBenchTerminal(&EmptyFrontend{}, backend, mode, newFn, autoWrap)
	if tln == nil {
		b.Fatal("newBenchTerminal returned nil")
	}
	if err := tln.Resize(benchWidth, benchHeight); err != nil {
		b.Fatalf("Resize failed: %v", err)
	}
	b.StartTimer()
	close(start)
	<-done
	<-tln.readLoopDone
}

func newBenchTerminal(frontend Frontend, backend Backend, mode TextReadMode, newFn func(Frontend) screen, autoWrap bool) *terminal {
	if frontend == nil {
		frontend = &EmptyFrontend{}
	}
	if backend == nil {
		return nil
	}
	term := newTerminal(frontend, backend, mode)
	term.mainScreen = newFn(frontend)
	term.altScreen = newFn(frontend)
	term.mainScreen.setScrollCapture(term.scrollCapture)
	term.altScreen.setScrollCapture(term.scrollCapture)
	term.mainScreen.setScrollDelta(term.scrollLinesDelta)
	// Configure the active screen before the read loop starts so the loop's
	// first AutoWrap read is not racing an unsynchronized write. Resize runs
	// under the terminal lock.
	term.mainScreen.SetAutoWrap(autoWrap)
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

// benchmarkRepaintRow measures rewriting a full row with content the screen
// already holds: the idempotent repaint that progress bars, watch, and status
// lines perform.
func benchmarkRepaintRow(b *testing.B, styled bool) {
	for _, factory := range screenFactories() {
		factory := factory
		b.Run(factory.name, func(b *testing.B) {
			s := factory.new(&EmptyFrontend{})
			s.setSize(benchWidth, benchHeight)
			line := benchLine(benchWidth, 0)
			styleA := NewStyle()
			styleB := NewStyle()
			_ = styleA.SetColor(ComponentFG, RGB(200, 120, 40))
			_ = styleB.SetColor(ComponentBG, RGB(10, 20, 30))

			write := func() {
				s.setCursorPos(0, 0)
				if !styled {
					s.writeString(line, benchWidth, false, TextReadModeRune)
					return
				}
				for x := 0; x < benchWidth; x += benchSegSize {
					end := x + benchSegSize
					if end > benchWidth {
						end = benchWidth
					}
					if (x/benchSegSize)&1 == 0 {
						s.setStyle(styleA)
					} else {
						s.setStyle(styleB)
					}
					s.writeString(line[x:end], end-x, false, TextReadModeRune)
				}
			}

			write()
			b.SetBytes(int64(benchWidth))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				write()
			}
		})
	}
}

func BenchmarkRepaintPlain(b *testing.B) {
	benchmarkRepaintRow(b, false)
}

func BenchmarkRepaintStyled(b *testing.B) {
	benchmarkRepaintRow(b, true)
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
