package termemu

import (
	"fmt"
	"strconv"
)

// Mode represents a text styling mode (bold, italic, underline, etc.)
type Mode uint16

const (
	ModeBold Mode = 1 << iota
	ModeDim
	ModeItalic
	ModeUnderline
	ModeBlink
	ModeReverse
	ModeInvisible
	ModeStrike
	ModeOverline
	ModeDoubleUnderline
	ModeFramed
	ModeEncircled
	ModeRapidBlink
)

// ColorComponent specifies which color component of a Style to modify
type ColorComponent uint8

const (
	ComponentFG ColorComponent = iota
	ComponentBG
	ComponentUnderline
)

// Public color constants for 256-color mode
const (
	ColBlack   = 0
	ColRed     = 1
	ColGreen   = 2
	ColYellow  = 3
	ColBlue    = 4
	ColMagenta = 5
	ColCyan    = 6
	ColWhite   = 7
)

// Internal constants and masks
const (
	colDefault     uint32 = 0x100
	colBright      uint32 = 0x200
	mask256color   uint32 = 0xff
	maskRGBcolor   uint32 = 0xffffff
	maskBrightIdx  uint32 = 0x7
	colorTypeShift        = 31
	colorTypeMask  uint32 = 1 << colorTypeShift
	modeBitsShift         = 24
	modeBitsMask   uint32 = 0x7F << modeBitsShift
)

// Color constants for the Color type
const (
	ColorType256 = 0
	ColorTypeRGB = 1
)

// Mode to SGR code mappings for output generation
var modeToSGRCode = map[Mode]int{
	ModeBold:            1,
	ModeDim:             2,
	ModeItalic:          3,
	ModeUnderline:       4,
	ModeBlink:           5,
	ModeRapidBlink:      6,
	ModeReverse:         7,
	ModeInvisible:       8,
	ModeStrike:          9,
	ModeDoubleUnderline: 21,
	ModeFramed:          51,
	ModeEncircled:       52,
	ModeOverline:        53,
}

// Color represents a terminal color value. Use the constructor functions
// to create values: DefaultColor, Color256, BrightColor, RGB.
type Color uint32

const (
	defaultColor Color = Color(colDefault)
)

// DefaultColor returns the terminal's default color.
func DefaultColor() Color { return defaultColor }

// Color256 returns a color from the 256-color palette (0–255).
func Color256(idx int) Color {
	if idx < 0 {
		idx = 0
	} else if idx > 255 {
		idx = 255
	}
	return Color(uint32(idx) & mask256color)
}

// BrightColor returns a bright variant (0–7) of the standard ANSI color.
func BrightColor(idx int) Color {
	if idx < 0 {
		idx = 0
	} else if idx > 7 {
		idx = 7
	}
	return Color(colBright | (uint32(idx) & maskBrightIdx))
}

// RGB creates a color from red, green, and blue components (0–255 each).
func RGB(r, g, b int) Color {
	r &= 0xff
	g &= 0xff
	b &= 0xff
	return Color((uint32(r)<<16 | uint32(g)<<8 | uint32(b)) | colorTypeMask)
}

// raw returns the internal uint32 representation.
func (c Color) raw() uint32 { return uint32(c) }

// IsDefault reports whether this is the terminal's default color.
func (c Color) IsDefault() bool { return c.raw() == colDefault }

// IsRGB reports whether this is an RGB color.
func (c Color) IsRGB() bool { return c.raw()&colorTypeMask != 0 }

// IsBright reports whether this is a bright ANSI color (0–7).
func (c Color) IsBright() bool {
	r := c.raw()
	return !c.IsDefault() && !c.IsRGB() && (r&colBright) != 0
}

// Index returns the 256-color or bright-color index.
// For RGB colors the lower 24 bits are returned.
func (c Color) Index() int { return int(c.raw() & maskRGBcolor) }

// RGB returns the red, green, blue components for an RGB color.
func (c Color) RGB() (r, g, b int) {
	v := c.raw() & maskRGBcolor
	return int((v >> 16) & 0xff), int((v >> 8) & 0xff), int(v & 0xff)
}

// Style represents complete text styling including colors and text modes.
// Modes are distributed across the high bytes (bits 24-30) of fg, bg, and underlineColor.
// Color data uses bits 0-23, and bit 31 is the color type flag (256-color vs RGB).
type Style struct {
	fg             uint32 // Foreground color + mode bits
	bg             uint32 // Background color + mode bits
	underlineColor uint32 // Underline color + mode bits
}

const ESC byte = 27

// NewStyle creates a default style with no colors and no modes set
func NewStyle() Style {
	return Style{
		fg:             colDefault,
		bg:             colDefault,
		underlineColor: colDefault,
	}
}

// modeBits extracts and combines mode bits from all three color values
// Returns a combined Mode value with all set modes
func (s *Style) modeBits() Mode {
	// Extract bits 24-30 from each color value
	fgModes := (s.fg >> modeBitsShift) & 0x7F
	bgModes := (s.bg >> modeBitsShift) & 0x7F

	// Combine into a single Mode value
	// FG stores modes 0-6, BG stores modes 7-12 (but shifted down)
	var combined Mode

	// Map FG modes (bits 0-6)
	for i := uint(0); i < 7; i++ {
		if (fgModes & (1 << i)) != 0 {
			combined |= Mode(1) << i
		}
	}

	// Map BG modes (bits 7-12 in Mode enum, but stored in bits 0-5 of bg's mode bits)
	for i := uint(0); i < 6; i++ {
		if (bgModes & (1 << i)) != 0 {
			combined |= Mode(1) << (i + 7)
		}
	}

	return combined
}

// SetColor sets a color component to the given Color value.
func (s *Style) SetColor(component ColorComponent, color Color) error {
	c := uint32(color) & ^modeBitsMask
	switch component {
	case ComponentFG:
		s.fg = (s.fg & modeBitsMask) | c
	case ComponentBG:
		s.bg = (s.bg & modeBitsMask) | c
	case ComponentUnderline:
		s.underlineColor = (s.underlineColor & modeBitsMask) | c
	default:
		return fmt.Errorf("invalid color component: %d", component)
	}
	return nil
}

// GetColor returns the Color for a component.
func (s *Style) GetColor(component ColorComponent) (Color, error) {
	var c uint32
	switch component {
	case ComponentFG:
		c = s.fg
	case ComponentBG:
		c = s.bg
	case ComponentUnderline:
		c = s.underlineColor
	default:
		return 0, fmt.Errorf("invalid color component: %d", component)
	}
	return Color(c & ^modeBitsMask), nil
}

// SetMode sets one or more text modes
func (s *Style) SetMode(modes ...Mode) {
	for _, m := range modes {
		// Determine which color value should store this mode
		if m < (1 << 7) {
			// Modes 0-6 go in FG (bits 24-30)
			s.fg |= uint32(m) << modeBitsShift
		} else if m < (1 << 13) {
			// Modes 7-12 go in BG (bits 24-29)
			// Shift down by 7 to fit in the BG mode space
			bgMode := m >> 7
			s.bg |= uint32(bgMode) << modeBitsShift
		}
	}
}

// Reset clears specific text modes, or resets everything when no modes are given.
//
//	style.Reset()           // reset all colors and modes
//	style.Reset(ModeBold)   // clear only bold
func (s *Style) Reset(modes ...Mode) {
	if len(modes) == 0 {
		// Reset everything
		s.fg = colDefault
		s.bg = colDefault
		s.underlineColor = colDefault
		return
	}

	for _, m := range modes {
		if m < (1 << 7) {
			// Modes 0-6 in FG
			s.fg &= ^(uint32(m) << modeBitsShift)
		} else if m < (1 << 13) {
			// Modes 7-12 in BG
			bgMode := m >> 7
			s.bg &= ^(uint32(bgMode) << modeBitsShift)
		}
	}
}

// TestMode checks if a mode is set
func (s *Style) TestMode(mode Mode) bool {
	return s.modeBits()&mode != 0
}

// Modes returns a slice of all currently set modes
func (s *Style) Modes() []Mode {
	combined := s.modeBits()
	var result []Mode
	for i := Mode(0); i < 16; i++ {
		m := Mode(1) << i
		if combined&m != 0 {
			result = append(result, m)
		}
	}
	return result
}

// ansiEscapeColor generates ANSI escape sequences for a single color
func ansiEscapeColor(c Color, param byte) []byte {
	var seq []byte

	cval := c.raw()
	isRGB := cval&colorTypeMask == colorTypeMask
	isBright := !c.IsDefault() && !isRGB && (cval&colBright) == colBright

	if isRGB {
		r, g, b := c.RGB()
		seq = append(seq, ESC, '[', param, '8', ';', '2', ';')
		seq = fmt.Appendf(seq, "%d;%d;%dm", r, g, b)
	} else if isBright {
		// Bright colors: SGR 90-97 for FG (param='3'), 100-107 for BG (param='4')
		brightIdx := int(cval & maskBrightIdx)
		baseCode := 90
		if param == '4' {
			baseCode = 100
		}
		seq = append(seq, ESC, '[')
		seq = append(seq, []byte(strconv.Itoa(baseCode+brightIdx))...)
		seq = append(seq, 'm')
	} else {
		colorVal := int(cval & mask256color)
		if cval == colDefault {
			// Don't emit anything for default color
		} else if colorVal < 8 {
			seq = append(seq, ESC, '[', param, byte('0'+rune(colorVal)), 'm')
		} else {
			seq = append(seq, ESC, '[', param, '8', ';', '5', ';')
			seq = append(seq, []byte(strconv.Itoa(colorVal)+"m")...)
		}
	}

	return seq
}

// ANSIEscape generates a complete ANSI escape sequence for this style
func (s Style) ANSIEscape() []byte {
	var seq []byte

	// Reset first
	seq = append(seq, ESC, '[', '0', 'm')

	def := NewStyle()
	seq = append(seq, s.ANSIEscapeFrom(def)...)

	return seq
}

// ANSIEscapeFrom generates a minimal ANSI escape sequence that changes from prev to this style
func (s Style) ANSIEscapeFrom(prev Style) []byte {
	var seq []byte

	// Check what changed
	modesChanged := s.modeBits() != prev.modeBits()
	fgChanged := (s.fg &^ modeBitsMask) != (prev.fg &^ modeBitsMask)
	bgChanged := (s.bg &^ modeBitsMask) != (prev.bg &^ modeBitsMask)

	if !modesChanged && !fgChanged && !bgChanged {
		return nil
	}

	// If modes changed, we need to reset and reapply all modes
	if modesChanged {
		seq = append(seq, ESC, '[', '0', 'm')
		// Re-apply all modes in this style
		for _, mode := range s.Modes() {
			if code, ok := modeToSGRCode[mode]; ok {
				seq = append(seq, ESC, '[')
				seq = append(seq, []byte(strconv.Itoa(code))...)
				seq = append(seq, 'm')
			}
		}
		// If modes changed, we also need to re-emit colors since we reset
		fgChanged = true
		bgChanged = true
	}

	// Only emit color changes if they changed.
	// A component returning to default needs an explicit reset (SGR 39/49)
	// unless a full reset was already emitted for a mode change above.
	if fgChanged {
		if Color(s.fg&^modeBitsMask).IsDefault() && !modesChanged {
			seq = append(seq, ESC, '[', '3', '9', 'm')
		} else {
			seq = append(seq, ansiEscapeColor(Color(s.fg&^modeBitsMask), '3')...)
		}
	}
	if bgChanged {
		if Color(s.bg&^modeBitsMask).IsDefault() && !modesChanged {
			seq = append(seq, ESC, '[', '4', '9', 'm')
		} else {
			seq = append(seq, ansiEscapeColor(Color(s.bg&^modeBitsMask), '4')...)
		}
	}

	return seq
}
