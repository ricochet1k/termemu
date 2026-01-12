package termemu

import (
	"testing"
)

const (
	benchWidth   = 160
	benchHeight  = 50
	benchSegSize = 8
)

func newBenchScreen() *screen {
	s := newScreen(&EmptyFrontend{})
	s.setSize(benchWidth, benchHeight)
	s.autoWrap = false // avoid scroll during steady-state throughput benches
	return s
}

func makeBenchLine(w int) []rune {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	line := make([]rune, w)
	for i := 0; i < w; i++ {
		line[i] = rune(alphabet[i%len(alphabet)])
	}
	return line
}

func BenchmarkScreenWriteRunesPlain(b *testing.B) {
	s := newBenchScreen()
	line := makeBenchLine(benchWidth)

	b.ReportAllocs()
	b.SetBytes(int64(benchWidth * benchHeight))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for y := 0; y < benchHeight; y++ {
			// Direct cursor set keeps the benchmark focused on screen writes.
			s.cursorPos = Pos{X: 0, Y: y}
			s.writeRunes(line)
		}
	}
}

func BenchmarkScreenWriteRunesPlainWrapScroll(b *testing.B) {
	s := newBenchScreen()
	s.autoWrap = true
	line := makeBenchLine(benchWidth)
	lines := benchHeight * 2

	b.ReportAllocs()
	b.SetBytes(int64(benchWidth * lines))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s.cursorPos = Pos{X: 0, Y: 0}
		for y := 0; y < lines; y++ {
			s.writeRunes(line)
		}
	}
}

func BenchmarkScreenWriteRunesStyled(b *testing.B) {
	s := newBenchScreen()
	line := makeBenchLine(benchWidth)
	fg := []Color{ColWhite, ColYellow}
	bg := []Color{ColBlack, ColBlue}

	b.ReportAllocs()
	b.SetBytes(int64(benchWidth * benchHeight))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for y := 0; y < benchHeight; y++ {
			s.cursorPos = Pos{X: 0, Y: y}
			for x := 0; x < benchWidth; x += benchSegSize {
				idx := (x / benchSegSize) & 1
				s.setColors(fg[idx], bg[idx])
				end := x + benchSegSize
				if end > benchWidth {
					end = benchWidth
				}
				s.writeRunes(line[x:end])
			}
		}
	}
}

func BenchmarkScreenWriteRunesStyledWrapScroll(b *testing.B) {
	s := newBenchScreen()
	s.autoWrap = true
	line := makeBenchLine(benchWidth)
	lines := benchHeight * 2
	fg := []Color{ColWhite, ColYellow}
	bg := []Color{ColBlack, ColBlue}

	b.ReportAllocs()
	b.SetBytes(int64(benchWidth * lines))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s.cursorPos = Pos{X: 0, Y: 0}
		for y := 0; y < lines; y++ {
			for x := 0; x < benchWidth; x += benchSegSize {
				idx := (x / benchSegSize) & 1
				s.setColors(fg[idx], bg[idx])
				end := x + benchSegSize
				if end > benchWidth {
					end = benchWidth
				}
				s.writeRunes(line[x:end])
			}
		}
	}
}

func BenchmarkScreenRandomCellUpdates(b *testing.B) {
	s := newBenchScreen()
	const lcgMul = uint32(1664525)
	const lcgAdd = uint32(1013904223)
	seed := uint32(1)

	b.ReportAllocs()
	b.SetBytes(1)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		seed = seed*lcgMul + lcgAdd
		x := int(seed % benchWidth)
		seed = seed*lcgMul + lcgAdd
		y := int(seed % benchHeight)
		s.rawWriteRune(x, y, 'x', 1, CRText)
	}
}
