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
	colDefault     uint32 = 0x100 // outside the 256-color range
	mask256color   uint32 = 0xff
	maskRGBcolor   uint32 = 0xffffff
	colorTypeShift        = 31
	colorTypeMask  uint32 = 1 << colorTypeShift
	modeBitsShift         = 24
	modeBitsMask   uint32 = 0x7F << modeBitsShift // 7 bits for modes
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

// --- Temporary backward compatibility exports ---
// These will be removed after migrating all code to Style

// Color is a backward compatibility type wrapping uint32
type Color uint32

// ColDefault is the default color value
const ColDefault Color = Color(colDefault)

// Colors8 provides the 8 basic colors
var Colors8 = [8]Color{
	ColBlack,
	ColRed,
	ColGreen,
	ColYellow,
	ColBlue,
	ColMagenta,
	ColCyan,
	ColWhite,
}

// ColorModes maps SGR codes 1-7 to their Mode values
var ColorModes = [7]Mode{
	ModeBold,      // SGR 1
	ModeDim,       // SGR 2
	ModeItalic,    // SGR 3
	ModeUnderline, // SGR 4
	ModeBlink,     // SGR 5
	ModeReverse,   // SGR 7 (skipping 6 which is RapidBlink in BG)
	ModeInvisible, // SGR 8
}

// ANSIEscape generates ANSI escape for two Color values (backward compatibility)
func ANSIEscape(fg, bg Color) []byte {
	// Convert Color values to Style
	s := Style{fg: uint32(fg), bg: uint32(bg), underlineColor: colDefault}
	return s.ANSIEscape()
}

// Helper methods on Color for backward compatibility
// These store mode bits directly in bits 24-30 without caring about FG vs BG distribution
func (c Color) SetMode(mode Mode) Color {
	// Mode already has the bit position (0-12), store it directly in bits 24-30
	// For modes 0-6, store as-is; for modes 7-12, shift down by 7
	modeBits := uint32(mode)
	if mode >= (1 << 7) {
		// BG mode (7-12), shift down to fit in 7-bit space
		modeBits = modeBits >> 7
	}
	return Color(uint32(c) | (modeBits << modeBitsShift))
}

func (c Color) ResetMode(mode Mode) Color {
	modeBits := uint32(mode)
	if mode >= (1 << 7) {
		modeBits = modeBits >> 7
	}
	return Color(uint32(c) &^ (modeBits << modeBitsShift))
}

func (c Color) TestMode(mode Mode) bool {
	modeBits := uint32(mode)
	if mode >= (1 << 7) {
		modeBits = modeBits >> 7
	}
	return (uint32(c) & (modeBits << modeBitsShift)) != 0
}

func (c Color) SetColor(col Color) Color {
	// Preserve mode bits, replace color
	return Color((uint32(c) & modeBitsMask) | (uint32(col) & ^modeBitsMask))
}

func (c Color) SetColorRGB(r, g, b int) Color {
	r &= 0xff
	g &= 0xff
	b &= 0xff
	colorVal := (uint32(r)<<16 | uint32(g)<<8 | uint32(b)) | colorTypeMask
	return Color((uint32(c) & modeBitsMask) | colorVal)
}

func (c Color) Color() int {
	c = Color(uint32(c) &^ modeBitsMask)
	isRGB := (uint32(c) & colorTypeMask) == colorTypeMask
	if isRGB {
		return int(uint32(c) & maskRGBcolor)
	}
	return int(uint32(c) & mask256color)
}

func (c Color) ColorRGB() (int, int, int) {
	val := c.Color()
	return val >> 16, (val >> 8) & 0xff, val & 0xff
}

func (c Color) ColorType() int {
	c = Color(uint32(c) &^ modeBitsMask)
	if (uint32(c) & colorTypeMask) == colorTypeMask {
		return 1 // RGB
	}
	return 0 // 256
}

func (c Color) Modes() []Mode {
	var result []Mode
	modes := (uint32(c) >> modeBitsShift) & 0x7F
	for i := uint(0); i < 7; i++ {
		if (modes & (1 << i)) != 0 {
			result = append(result, Mode(1)<<i)
		}
	}
	return result
}

// ColorType constants
const (
	ColorType256 = 0
	ColorTypeRGB = 1
)

// SetColorDefault resets a color component to its default value
func (s *Style) SetColorDefault(component ColorComponent) error {
	switch component {
	case ComponentFG:
		s.fg = (s.fg & modeBitsMask) | colDefault
	case ComponentBG:
		s.bg = (s.bg & modeBitsMask) | colDefault
	case ComponentUnderline:
		s.underlineColor = (s.underlineColor & modeBitsMask) | colDefault
	default:
		return fmt.Errorf("invalid color component: %d", component)
	}
	return nil
}

// SetColor256 sets a color component to a 256-color mode value
func (s *Style) SetColor256(component ColorComponent, idx int) error {
	if idx < 0 || idx > 255 {
		return fmt.Errorf("color index out of range: %d", idx)
	}
	colorVal := uint32(idx&0xff) & ^colorTypeMask
	switch component {
	case ComponentFG:
		s.fg = (s.fg & modeBitsMask) | colorVal
	case ComponentBG:
		s.bg = (s.bg & modeBitsMask) | colorVal
	case ComponentUnderline:
		s.underlineColor = (s.underlineColor & modeBitsMask) | colorVal
	default:
		return fmt.Errorf("invalid color component: %d", component)
	}
	return nil
}

// SetColorRGB sets a color component to an RGB value
func (s *Style) SetColorRGB(component ColorComponent, r, g, b int) error {
	r &= 0xff
	g &= 0xff
	b &= 0xff
	colorVal := (uint32(r)<<16 | uint32(g)<<8 | uint32(b)) | colorTypeMask
	switch component {
	case ComponentFG:
		s.fg = (s.fg & modeBitsMask) | colorVal
	case ComponentBG:
		s.bg = (s.bg & modeBitsMask) | colorVal
	case ComponentUnderline:
		s.underlineColor = (s.underlineColor & modeBitsMask) | colorVal
	default:
		return fmt.Errorf("invalid color component: %d", component)
	}
	return nil
}

// GetColor returns the color value for a component and its type
// Returns (value, isRGB, error)
func (s *Style) GetColor(component ColorComponent) (int, bool, error) {
	var c uint32
	switch component {
	case ComponentFG:
		c = s.fg
	case ComponentBG:
		c = s.bg
	case ComponentUnderline:
		c = s.underlineColor
	default:
		return 0, false, fmt.Errorf("invalid color component: %d", component)
	}

	c &= ^modeBitsMask // Clear mode bits
	isRGB := (c & colorTypeMask) == colorTypeMask
	var value int
	if isRGB {
		value = int(c & maskRGBcolor)
	} else {
		value = int(c & mask256color)
	}
	return value, isRGB, nil
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

// ResetMode clears one or more text modes
func (s *Style) ResetMode(modes ...Mode) {
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

// ResetModes clears all text modes
func (s *Style) ResetModes() {
	s.fg &= ^modeBitsMask
	s.bg &= ^modeBitsMask
	s.underlineColor &= ^modeBitsMask
}

// ResetColor resets a color component to default
func (s *Style) ResetColor(component ColorComponent) error {
	return s.SetColorDefault(component)
}

// ResetAll resets all colors and modes to defaults
func (s *Style) ResetAll() {
	s.fg = colDefault
	s.bg = colDefault
	s.underlineColor = colDefault
}

// ansiEscapeColor generates ANSI escape sequences for a single color
func ansiEscapeColor(c uint32, param byte) []byte {
	var seq []byte

	// Clear mode bits to get just the color
	c &= ^modeBitsMask

	isRGB := (c & colorTypeMask) == colorTypeMask

	if isRGB {
		rgb := c & maskRGBcolor
		r := (rgb >> 16) & 0xff
		g := (rgb >> 8) & 0xff
		b := rgb & 0xff
		seq = append(seq, ESC, '[', param, '8', ';', '2', ';')
		seq = append(seq, []byte(fmt.Sprintf("%d;%d;%dm", r, g, b))...)
	} else {
		colorVal := int(c & mask256color)
		if c == colDefault {
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
func (s *Style) ANSIEscape() []byte {
	var seq []byte

	// Reset first
	seq = append(seq, ESC, '[', '0', 'm')

	// Add modes
	for _, mode := range s.Modes() {
		if code, ok := modeToSGRCode[mode]; ok {
			seq = append(seq, ESC, '[')
			seq = append(seq, []byte(strconv.Itoa(code))...)
			seq = append(seq, 'm')
		}
	}

	// Add colors
	seq = append(seq, ansiEscapeColor(s.fg, '3')...)
	seq = append(seq, ansiEscapeColor(s.bg, '4')...)

	return seq
}

// ANSIEscapeFrom generates a minimal ANSI escape sequence that changes from prev to this style
func (s *Style) ANSIEscapeFrom(prev *Style) []byte {
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

	// Only emit color changes if they changed
	if fgChanged {
		seq = append(seq, ansiEscapeColor(s.fg, '3')...)
	}
	if bgChanged {
		seq = append(seq, ansiEscapeColor(s.bg, '4')...)
	}

	return seq
}
