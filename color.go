package termemu

import (
	"fmt"
	"strconv"
)

type Color uint32

type ColorMode uint32
type ColorType uint32

// BIT LAYOUT for Color (32-bit value):
//
// Bits 0-7:   Color value (for 256-color mode) or R channel (for RGB)
// Bits 8-23:  G and B channels (for RGB mode)
// Bits 24-30: Text modes (Bold, Dim, Italic, Underline, Blink, Reverse, Invisible = 7 bits)
// Bit 31:     Color type (0=256-color/default, 1=RGB)
//
// IMPROVEMENT with Style type:
// - We now use Color only for FG/BG colors (bits 0-23 + type flag on bit 31)
// - Bits 24-30 in FG store: Bold, Dim, Italic, Underline, Blink, Reverse, Invisible (7 bits)
// - Bits 24-30 in BG store: Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink (6 bits)
// - Bits 24-28 in UnderlineColor store: UnderlineStyle bits (0-2), plus 2 reserved bits
// - Bits 29-31 in UnderlineColor: reserved for future use
//
// SUPPORTED MODES (no memory overhead):
// - Bold, Dim, Italic, Underline, Blink, Reverse, Invisible (old)
// - Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink (new)
// - Underline color (separate from foreground)
// - Underline style (3 bits: solid, double, curly, dotted, etc.)

const (
	mask256color Color = 0xff
	maskRGBcolor Color = 0xffffff // 24-bit

	ColDefault Color = mask256color + 1 // outside the range
	ColBlack   Color = 0
	ColRed     Color = 1
	ColGreen   Color = 2
	ColYellow  Color = 3
	ColBlue    Color = 4
	ColMagenta Color = 5
	ColCyan    Color = 6
	ColWhite   Color = 7

	// Text modes in FG (bits 24-30)
	ModeBold      ColorMode = 1 << (0 + 24)
	ModeDim       ColorMode = 1 << (1 + 24)
	ModeItalic    ColorMode = 1 << (2 + 24)
	ModeUnderline ColorMode = 1 << (3 + 24)
	ModeBlink     ColorMode = 1 << (4 + 24)
	ModeReverse   ColorMode = 1 << (5 + 24)
	ModeInvisible ColorMode = 1 << (6 + 24)

	// Text modes in BG (bits 24-30, but we use only 24-29 to leave room)
	ModeStrike          ColorMode = 1 << (0 + 24)
	ModeOverline        ColorMode = 1 << (1 + 24)
	ModeDoubleUnderline ColorMode = 1 << (2 + 24)
	ModeFramed          ColorMode = 1 << (3 + 24)
	ModeEncircled       ColorMode = 1 << (4 + 24)
	ModeRapidBlink      ColorMode = 1 << (5 + 24)

	colorTypeShift           = 7 + 24
	colorTypeMask            = 1 << colorTypeShift
	ColorType256   ColorType = 0
	ColorTypeRGB   ColorType = 1

	// Underline style bits (in UnderlineColor, bits 24-26)
	UnderlineStyleShift = 24
	UnderlineStyleMask  = 0x7 << UnderlineStyleShift
)

// Style represents complete text styling: foreground color, background color, and all text modes
// This type encapsulates the new layout where modes are spread across FG, BG, and UnderlineColor
type Style struct {
	FG             Color // Foreground color with FG modes in bits 24-30
	BG             Color // Background color with BG modes in bits 24-30
	UnderlineColor Color // Separate underline color with style bits in bits 24-26
}

var Colors8 = []Color{
	ColBlack,
	ColRed,
	ColGreen,
	ColYellow,
	ColBlue,
	ColMagenta,
	ColCyan,
	ColWhite,
}

var ColorModes = []ColorMode{
	ModeBold,
	ModeDim,
	ModeItalic,
	ModeUnderline,
	ModeBlink,
	ModeReverse,
	ModeInvisible,
}

var BGModes = []ColorMode{
	ModeStrike,
	ModeOverline,
	ModeDoubleUnderline,
	ModeFramed,
	ModeEncircled,
	ModeRapidBlink,
}

var colorMasks = []Color{
	mask256color,
	maskRGBcolor,
}

func (c Color) SetColor(color Color) Color {
	c &= ^mask256color
	color &= mask256color
	return (c | color) & ^Color(colorTypeMask)
}

func (c Color) SetColorRGB(r int, g int, b int) Color {
	r &= 0xff
	g &= 0xff
	b &= 0xff
	// clear any previous RGB/256 color bits
	c &= ^maskRGBcolor
	c &= ^mask256color
	// set RGB color bits and mark the color type as RGB
	c = (c | Color(r<<16|g<<8|b)) | Color(colorTypeMask)
	return c
}

func (c Color) Color() int {
	t := c.ColorType()
	mask := colorMasks[int(t)]
	return int(c & mask)
}

func (c Color) ColorRGB() (int, int, int) {
	i := c.Color()
	return i >> 16, (i >> 8) & 0xff, i & 0xff
}

func (c Color) ColorType() ColorType {
	return ColorType(c&colorTypeMask) >> colorTypeShift
}

func (c Color) SetMode(mode ColorMode) Color {
	return c | Color(mode)
}

func (c Color) ResetMode(mode ColorMode) Color {
	return c & ^Color(mode)
}

func (c Color) ResetModes() {
	for _, m := range ColorModes {
		c.ResetMode(m)
	}
}

func (c Color) TestMode(mode ColorMode) bool {
	return c&Color(mode) != 0
}

func (c Color) Modes() []ColorMode {
	count := 0
	for _, m := range ColorModes {
		if c.TestMode(m) {
			count += 1
		}
	}

	modes := make([]ColorMode, count)
	i := 0
	for _, m := range ColorModes {
		if c.TestMode(m) {
			modes[i] = m
			i++
		}
	}
	return modes
}

const ESC byte = 27

// only set color, no escape, no modes
func ansiEscapeColor(c Color, ctype byte) []byte {
	var seq []byte

	color := c.Color()
	colorStr := strconv.Itoa(color)
	t := c.ColorType()
	// fmt.Printf("%v: %x %d %#v\n", t, c, color, colorStr)
	switch t {
	case ColorType256:
		if c == ColDefault {
			// ANSIEscape already reset colors, so leave defaults untouched
		} else if color < 8 {
			seq = append(seq, ESC, '[', ctype, byte(colorStr[0]), 'm')
		} else {
			seq = append(seq, ESC, '[', ctype, '8', ';', '5', ';')
			seq = append(seq, []byte(colorStr+"m")...)
		}
	case ColorTypeRGB:
		seq = append(seq, ESC, '[', ctype, '8', ';', '2', ';')
		r, g, b := c.ColorRGB()
		seq = append(seq, []byte(fmt.Sprintf("%d;%d;%dm", r, g, b))...)
	}
	return seq
}

// ANSI Escape sequence to set this color
func ANSIEscape(fg Color, bg Color) []byte {
	seq := []byte{ESC, '[', '0', 'm'}
	for i, m := range ColorModes {
		if fg.TestMode(m) {
			seq = append(seq, ESC, '[')
			seq = append(seq, []byte(strconv.Itoa(1+i))...)
			seq = append(seq, 'm')
		}
	}

	seq = append(seq, ansiEscapeColor(fg, '3')...)
	seq = append(seq, ansiEscapeColor(bg, '4')...)

	return seq
}

// Style Methods

// NewStyle creates a default style
func NewStyle() Style {
	return Style{
		FG:             ColDefault,
		BG:             ColDefault,
		UnderlineColor: ColDefault,
	}
}

// ANSIEscape generates ANSI escape sequence for this style
func (s *Style) ANSIEscape() []byte {
	seq := []byte{ESC, '[', '0', 'm'}

	// FG modes
	for i, m := range ColorModes {
		if s.FG.TestMode(m) {
			seq = append(seq, ESC, '[')
			seq = append(seq, []byte(strconv.Itoa(1+i))...)
			seq = append(seq, 'm')
		}
	}

	// BG modes (mapped to their SGR codes)
	bgModeCodes := []int{9, 53, 21, 51, 52, 6} // Strike, Overline, DoubleUnderline, Framed, Encircled, RapidBlink
	for i, m := range BGModes {
		if s.BG.TestMode(m) {
			seq = append(seq, ESC, '[')
			seq = append(seq, []byte(strconv.Itoa(bgModeCodes[i]))...)
			seq = append(seq, 'm')
		}
	}

	// FG and BG colors
	seq = append(seq, ansiEscapeColor(s.FG, '3')...)
	seq = append(seq, ansiEscapeColor(s.BG, '4')...)

	// Underline color if set and different from foreground
	if s.UnderlineColor != ColDefault {
		seq = append(seq, ansiEscapeColor(s.UnderlineColor, '5')[2:]...) // SGR 58 for underline color
		// Prepend SGR 58
		seq = append([]byte{ESC, '[', '5', '8', ';'}, seq...)
	}

	return seq
}

// SetFGMode sets a foreground text mode
func (s *Style) SetFGMode(mode ColorMode) {
	s.FG = s.FG.SetMode(mode)
}

// ResetFGMode clears a foreground text mode
func (s *Style) ResetFGMode(mode ColorMode) {
	s.FG = s.FG.ResetMode(mode)
}

// TestFGMode checks if a foreground text mode is set
func (s *Style) TestFGMode(mode ColorMode) bool {
	return s.FG.TestMode(mode)
}

// SetBGMode sets a background text mode (stored in BG color's mode bits)
func (s *Style) SetBGMode(mode ColorMode) {
	s.BG = s.BG.SetMode(mode)
}

// ResetBGMode clears a background text mode (stored in BG color's mode bits)
func (s *Style) ResetBGMode(mode ColorMode) {
	s.BG = s.BG.ResetMode(mode)
}

// TestBGMode checks if a background text mode is set (stored in BG color's mode bits)
func (s *Style) TestBGMode(mode ColorMode) bool {
	return s.BG.TestMode(mode)
}

// SetUnderlineStyle sets the underline style (bits 24-26)
func (s *Style) SetUnderlineStyle(style uint8) {
	s.UnderlineColor &= ^Color(UnderlineStyleMask)
	s.UnderlineColor |= Color((style & 0x7) << UnderlineStyleShift)
}

// UnderlineStyle returns the underline style (bits 24-26)
func (s *Style) UnderlineStyle() uint8 {
	return uint8((s.UnderlineColor & Color(UnderlineStyleMask)) >> UnderlineStyleShift)
}
