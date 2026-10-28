package raster

import (
	"image"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/ricochet1k/termemu"
)

// render feeds input to a terminal, waits for the read loop to finish, and
// returns the rendered image plus the renderer.
func render(t *testing.T, input string, cols, rows int) (*image.RGBA, *Renderer) {
	t.Helper()
	r, err := New(16)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	backend := termemu.NewNoPTYBackend(strings.NewReader(input), io.Discard)
	term := termemu.NewWithMode(&termemu.EmptyFrontend{}, backend, termemu.TextReadModeGrapheme)
	<-term.Done()

	cellW, cellH := r.CellSize()
	img := image.NewRGBA(image.Rect(0, 0, cols*cellW, rows*cellH))
	term.View(func(v termemu.LockedView) { r.Draw(img, v) })
	return img, r
}

// cellCorner returns the top-left pixel of a cell, which is background.
func cellCorner(img *image.RGBA, r *Renderer, x, y int) color.RGBA {
	cw, ch := r.CellSize()
	return img.RGBAAt(x*cw, y*ch)
}

func assertColor(t *testing.T, label string, got, want color.RGBA) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func TestDrawBackgroundAndDefault(t *testing.T) {
	const cols, rows = 20, 3
	img, r := render(t, "\x1b[?25l\x1b[48;5;226mX\x1b[0m", cols, rows)

	assertColor(t, "palette background", cellCorner(img, r, 0, 0), color.RGBA{R: 0xff, G: 0xff, B: 0x00, A: 0xff})
	assertColor(t, "default background", cellCorner(img, r, 1, 0), color.RGBA{R: 0, G: 0, B: 0, A: 0xff})
	assertColor(t, "empty row", cellCorner(img, r, 0, 2), color.RGBA{R: 0, G: 0, B: 0, A: 0xff})

	if wantW, wantH := r.Size(cols, rows); img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
		t.Errorf("image is %v, want %dx%d", img.Bounds(), wantW, wantH)
	}
}

func TestDrawReverseSwapsColors(t *testing.T) {
	img, r := render(t, "\x1b[?25l\x1b[7mX", 4, 1)
	assertColor(t, "reversed cell background", cellCorner(img, r, 0, 0), color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
}

func TestDrawOSC4PaletteAndRGB(t *testing.T) {
	input := "\x1b[?25l" + // hide the cursor so it does not cover a cell
		"\x1b]4;1;rgb:00/80/ff\x1b\\" + // redefine palette entry 1
		"\x1b[41mX" + // SGR 41 = palette index 1
		"\x1b[48;2;10;20;30mY" // truecolor background
	img, r := render(t, input, 4, 1)

	assertColor(t, "redefined palette", cellCorner(img, r, 0, 0), color.RGBA{R: 0x00, G: 0x80, B: 0xff, A: 0xff})
	assertColor(t, "truecolor background", cellCorner(img, r, 1, 0), color.RGBA{R: 10, G: 20, B: 30, A: 0xff})
}

func TestDrawCursorBlockInvertsCell(t *testing.T) {
	// The block cursor paints the cell in the cursor color and redraws the
	// glyph in the cell background color.
	img, r := render(t, "\x1b[32mX", 4, 1)
	// Writing "X" leaves the cursor on the cell after it.
	assertColor(t, "text cell", cellCorner(img, r, 0, 0), color.RGBA{R: 0, G: 0, B: 0, A: 0xff})
	assertColor(t, "cursor block", cellCorner(img, r, 1, 0), color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
}

func TestDrawCursorHidden(t *testing.T) {
	img, r := render(t, "\x1b[32mX\x1b[?25l", 4, 1)
	assertColor(t, "cell where the cursor would be", cellCorner(img, r, 1, 0), color.RGBA{R: 0, G: 0, B: 0, A: 0xff})
}

func TestDrawUnderlineUsesForeground(t *testing.T) {
	img, r := render(t, "\x1b[4mX", 4, 1)
	cw, ch := r.CellSize()
	found := false
	for y := ch - 2; y < ch; y++ {
		for x := 0; x < cw; x++ {
			if img.RGBAAt(x, y) == (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
				found = true
			}
		}
	}
	if !found {
		t.Error("no underline pixels found in the bottom rows of the cell")
	}
}

func TestDrawIgnoresEmptySpans(t *testing.T) {
	// A zero-width span must not paint anything or shift later cells.
	img, r := render(t, "\x1b[?25l\x1b[48;5;196m  \x1b[0m", 4, 1)
	// Only the two blanks carry the red background.
	assertColor(t, "blank 0", cellCorner(img, r, 0, 0), color.RGBA{R: 0xff, G: 0, B: 0, A: 0xff})
	assertColor(t, "blank 1", cellCorner(img, r, 1, 0), color.RGBA{R: 0xff, G: 0, B: 0, A: 0xff})
	assertColor(t, "cell after blanks", cellCorner(img, r, 2, 0), color.RGBA{R: 0, G: 0, B: 0, A: 0xff})
}

// ink returns the number of non-background pixels in one cell.
func ink(img *image.RGBA, r *Renderer, x, y int) int {
	cw, ch := r.CellSize()
	ax, ay := x*cw, y*ch
	count := 0
	for py := ay; py < ay+ch && py < img.Bounds().Dy(); py++ {
		for px := ax; px < ax+cw && px < img.Bounds().Dx(); px++ {
			if img.RGBAAt(px, py) != (color.RGBA{A: 0xff}) {
				count++
			}
		}
	}
	return count
}

func TestDrawTextProducesInk(t *testing.T) {
	img, r := render(t, "\x1b[?25lA", 4, 1)
	if n := ink(img, r, 0, 0); n < 10 {
		t.Errorf("cell 0 has %d ink pixels, want the glyph to be drawn", n)
	}
	if n := ink(img, r, 1, 0); n != 0 {
		t.Errorf("cell 1 has %d ink pixels, want none", n)
	}
}

func TestDrawWideClusterUsesTwoCells(t *testing.T) {
	// Go Mono has no CJK glyphs, so the wide cluster itself paints nothing;
	// what this checks is that it consumes two cells, leaving "A" in cell 2.
	img, r := render(t, "\x1b[?25l日A", 4, 1)
	if n := ink(img, r, 2, 0); n == 0 {
		t.Error("cell 2 is empty, want the glyph after the wide cluster")
	}
	if n := ink(img, r, 3, 0); n != 0 {
		t.Errorf("cell 3 has %d ink pixels, want none", n)
	}
}
