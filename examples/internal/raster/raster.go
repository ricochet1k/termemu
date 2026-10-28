// Package raster renders termemu screen state into an RGBA image on the CPU.
//
// It exists so the graphical frontend examples (headless, gioui, ebiten) can
// share one renderer instead of each growing its own glyph atlas. It is not
// part of the termemu library API.
//
// The renderer is deliberately simple: it paints the whole screen every time,
// uses a single monospace face (so no shaping or glyph atlas is needed), and
// maps one grapheme cluster to one cell. Ligatures, font fallback, and
// per-glyph kerning are out of scope; a real frontend would use a text
// library that provides them.
package raster

import (
	"errors"
	"image"
	"image/color"
	"image/draw"

	"github.com/ricochet1k/termemu"
	"github.com/rivo/uniseg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Renderer draws terminal state with a fixed cell grid.
type Renderer struct {
	face   font.Face
	cellW  int
	cellH  int
	ascent int
}

// New builds a renderer using the bundled Go Mono font at sizePx pixels.
func New(sizePx float64) (*Renderer, error) {
	f, err := sfnt.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	adv, ok := face.GlyphAdvance('M')
	if !ok {
		return nil, errors.New("raster: font has no advance for 'M'")
	}
	m := face.Metrics()
	return &Renderer{
		face:   face,
		cellW:  adv.Round(),
		cellH:  (m.Ascent + m.Descent).Ceil(),
		ascent: m.Ascent.Ceil(),
	}, nil
}

// Close releases the font face.
func (r *Renderer) Close() error { return r.face.Close() }

// CellSize returns the pixel size of one character cell.
func (r *Renderer) CellSize() (w, h int) { return r.cellW, r.cellH }

// Size returns the pixel size needed to display a cols×rows terminal.
func (r *Renderer) Size(cols, rows int) (w, h int) {
	return cols * r.cellW, rows * r.cellH
}

// Draw paints the visible screen of v into dst. dst must be at least
// Size(cols, rows); the caller is responsible for clearing extra space.
func (r *Renderer) Draw(dst *image.RGBA, v termemu.LockedView) {
	cols, rows := v.Size()
	draw.Draw(dst, dst.Bounds(), image.NewUniform(toRGBA(v.DefaultBackground())), image.Point{}, draw.Src)

	for y := 0; y < rows; y++ {
		line := v.StyledLine(0, cols, y)
		x := 0
		for _, span := range line.Spans {
			if span.Width <= 0 {
				continue
			}
			if x >= cols {
				break
			}
			fg, bg := r.colors(v, span.Style)
			r.paintSpan(dst, x, y, cols, span, fg, bg)
			x += span.Width
		}
	}
	r.drawCursor(dst, v)
}

// paintSpan draws one span: the cell backgrounds, the glyphs, and the
// underline/strike decorations that apply to the whole span.
func (r *Renderer) paintSpan(dst *image.RGBA, x, y, cols int, span termemu.Span, fg, bg color.RGBA) {
	width := span.Width
	if x+width > cols {
		width = cols - x
	}
	r.fillCells(dst, x, y, width, bg)
	if span.Text != "" {
		cx := x
		g := uniseg.NewGraphemes(span.Text)
		for g.Next() {
			if cx >= cols {
				break
			}
			cw := g.Width()
			if cw <= 0 {
				// Zero-width clusters are merged into the previous cell
				// by the screen, so there is no separate cell to draw.
				continue
			}
			if cx+cw > cols {
				break
			}
			r.fillCells(dst, cx, y, cw, bg)
			r.drawCluster(dst, cx, y, g.Str(), fg, span.Style.TestMode(termemu.ModeBold))
			cx += cw
		}
	}
	r.decorate(dst, x, y, width, span.Style, fg)
}

// drawCluster paints one grapheme cluster at cell (x, y).
func (r *Renderer) drawCluster(dst *image.RGBA, x, y int, cluster string, fg color.RGBA, bold bool) {
	d := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(fg),
		Face: r.face,
		Dot:  fixed.P(x*r.cellW, y*r.cellH+r.ascent),
	}
	d.DrawString(cluster)
	if bold {
		// No bold face is loaded, so fake it with a one-pixel smear.
		d.Dot.X += fixed.I(1)
		d.DrawString(cluster)
	}
}

// decorate draws underline, double underline, strike, and overline.
func (r *Renderer) decorate(dst *image.RGBA, x, y, width int, style termemu.Style, fg color.RGBA) {
	if width <= 0 {
		return
	}
	top := y * r.cellH
	bottom := top + r.cellH - 1
	switch {
	case style.TestMode(termemu.ModeDoubleUnderline):
		r.fillRect(dst, x*r.cellW, bottom-2, (x+width)*r.cellW, bottom-1, fg)
		r.fillRect(dst, x*r.cellW, bottom, (x+width)*r.cellW, bottom+1, fg)
	case style.TestMode(termemu.ModeUnderline):
		r.fillRect(dst, x*r.cellW, bottom, (x+width)*r.cellW, bottom+1, fg)
	}
	if style.TestMode(termemu.ModeStrike) {
		mid := top + r.ascent/2
		r.fillRect(dst, x*r.cellW, mid, (x+width)*r.cellW, mid+1, fg)
	}
	if style.TestMode(termemu.ModeOverline) {
		r.fillRect(dst, x*r.cellW, top, (x+width)*r.cellW, top+1, fg)
	}
}

// fillCells fills the given number of cells in a row.
func (r *Renderer) fillCells(dst *image.RGBA, x, y, width int, c color.RGBA) {
	if width <= 0 {
		return
	}
	r.fillRect(dst, x*r.cellW, y*r.cellH, (x+width)*r.cellW, (y+1)*r.cellH, c)
}

// fillRect fills a pixel rectangle, clipped to the destination.
func (r *Renderer) fillRect(dst *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	rect := image.Rect(x0, y0, x1, y1).Intersect(dst.Bounds())
	if rect.Empty() {
		return
	}
	draw.Draw(dst, rect, image.NewUniform(c), image.Point{}, draw.Src)
}

// drawCursor paints the cursor over the finished frame. A block cursor
// inverts the cell it covers; bar and underline shapes are painted in the
// cursor color without hiding the glyph.
func (r *Renderer) drawCursor(dst *image.RGBA, v termemu.LockedView) {
	if !v.GetViewFlag(termemu.VFShowCursor) {
		return
	}
	cols, rows := v.Size()
	p := v.CursorPos()
	if p.X < 0 || p.Y < 0 || p.X >= cols || p.Y >= rows {
		return
	}
	cursor := toRGBA(v.CursorColor())
	top := p.Y * r.cellH
	bottom := top + r.cellH
	left := p.X * r.cellW
	right := left + r.cellW

	switch v.GetViewInt(termemu.VICursorShape) {
	case termemu.CursorShapeBlinkBar, termemu.CursorShapeSteadyBar:
		r.fillRect(dst, left, top, left+2, bottom, cursor)
	case termemu.CursorShapeBlinkUnderline, termemu.CursorShapeSteadyUnderline:
		r.fillRect(dst, left, bottom-2, right, bottom, cursor)
	default:
		r.fillRect(dst, left, top, right, bottom, cursor)
		r.drawInvertedCell(dst, v, p)
	}
}

// drawInvertedCell redraws the cell under a block cursor in the cell's own
// background color, which is what a block cursor does to the foreground.
func (r *Renderer) drawInvertedCell(dst *image.RGBA, v termemu.LockedView, p termemu.Pos) {
	line := v.StyledLine(p.X, 1, p.Y)
	if len(line.Spans) == 0 {
		return
	}
	span := line.Spans[0]
	if span.Text == "" {
		return
	}
	g := uniseg.NewGraphemes(span.Text)
	if !g.Next() || g.Width() <= 0 {
		return
	}
	_, bg := r.colors(v, span.Style)
	r.drawCluster(dst, p.X, p.Y, g.Str(), bg, span.Style.TestMode(termemu.ModeBold))
}

// colors resolves a style's foreground and background to RGB, applying the
// modes that change how colors are painted.
func (r *Renderer) colors(v termemu.LockedView, style termemu.Style) (fg, bg color.RGBA) {
	fgColor, _ := style.GetColor(termemu.ComponentFG)
	bgColor, _ := style.GetColor(termemu.ComponentBG)
	fg = r.resolve(v, fgColor, v.DefaultForeground())
	bg = r.resolve(v, bgColor, v.DefaultBackground())
	if style.TestMode(termemu.ModeReverse) {
		fg, bg = bg, fg
	}
	if style.TestMode(termemu.ModeDim) {
		fg = blend(fg, bg, 0.5)
	}
	if style.TestMode(termemu.ModeInvisible) {
		fg = bg
	}
	return fg, bg
}

// resolve maps a terminal color to RGB, falling back to def for the default
// color and to the terminal's palette for indexed colors.
func (r *Renderer) resolve(v termemu.LockedView, c termemu.Color, def termemu.Color) color.RGBA {
	switch {
	case c.IsDefault():
		return toRGBA(def)
	case c.IsRGB():
		return toRGBA(c)
	case c.IsBright():
		return toRGBA(v.PaletteColor(8 + c.Index()&7))
	default:
		return toRGBA(v.PaletteColor(c.Index()))
	}
}

func toRGBA(c termemu.Color) color.RGBA {
	if c.IsDefault() {
		return color.RGBA{A: 0xff}
	}
	red, green, blue := c.RGB()
	return color.RGBA{R: uint8(red), G: uint8(green), B: uint8(blue), A: 0xff}
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-t) + float64(y)*t)
	}
	return color.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 0xff}
}
