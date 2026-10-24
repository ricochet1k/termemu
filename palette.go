package termemu

import (
	"fmt"
	"strconv"
	"strings"
)

// paletteSize is the number of entries in the color palette (OSC 4 indexes).
const paletteSize = 256

// buildDefaultPalette returns xterm's default 256-color palette: the 16 ANSI
// colors, a 6x6x6 RGB cube, and a 24-step grayscale ramp.
func buildDefaultPalette() [paletteSize]Color {
	var p [paletteSize]Color
	ansi := [16]Color{
		RGB(0x00, 0x00, 0x00), RGB(0xcd, 0x00, 0x00), RGB(0x00, 0xcd, 0x00), RGB(0xcd, 0xcd, 0x00),
		RGB(0x00, 0x00, 0xee), RGB(0xcd, 0x00, 0xcd), RGB(0x00, 0xcd, 0xcd), RGB(0xe5, 0xe5, 0xe5),
		RGB(0x7f, 0x7f, 0x7f), RGB(0xff, 0x00, 0x00), RGB(0x00, 0xff, 0x00), RGB(0xff, 0xff, 0x00),
		RGB(0x5c, 0x5c, 0xff), RGB(0xff, 0x00, 0xff), RGB(0x00, 0xff, 0xff), RGB(0xff, 0xff, 0xff),
	}
	copy(p[:16], ansi[:])
	levels := [6]int{0, 95, 135, 175, 215, 255}
	idx := 16
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				p[idx] = RGB(levels[r], levels[g], levels[b])
				idx++
			}
		}
	}
	for i := 0; i < 24; i++ {
		v := 8 + i*10
		p[232+i] = RGB(v, v, v)
	}
	return p
}

// defaultPalette is the palette a terminal starts with and OSC 104 restores.
var defaultPalette = buildDefaultPalette()

// defaultDynamicColors is the initial state for OSC 10/11/12.
var defaultDynamicColors = dynamicColors{
	fg:     RGB(0xff, 0xff, 0xff),
	bg:     RGB(0x00, 0x00, 0x00),
	cursor: RGB(0xff, 0xff, 0xff),
}

// dynamicColors holds the terminal's default foreground, background, and
// cursor colors as set by OSC 10/11/12.
type dynamicColors struct {
	fg, bg, cursor Color
}

// paletteColor returns the RGB value for a palette index. Out-of-range indexes
// return the default color.
func (t *terminal) paletteColor(idx int) Color {
	if idx < 0 || idx >= paletteSize {
		return DefaultColor()
	}
	return t.palette[idx]
}

// PaletteColor returns the RGB color currently mapped to palette index idx.
// It locks internally; use LockedView.PaletteColor inside callbacks.
func (t *terminal) PaletteColor(idx int) Color {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.PaletteColor(idx)
}

// DefaultForeground returns the terminal's default foreground color (OSC 10).
// It locks internally.
func (t *terminal) DefaultForeground() Color {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.DefaultForeground()
}

// DefaultBackground returns the terminal's default background color (OSC 11).
// It locks internally.
func (t *terminal) DefaultBackground() Color {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.DefaultBackground()
}

// CursorColor returns the terminal's cursor color (OSC 12). It locks
// internally.
func (t *terminal) CursorColor() Color {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.CursorColor()
}

// PaletteColor returns the RGB color mapped to palette index idx. Indexes
// outside 0-255 return the default color.
func (v LockedView) PaletteColor(idx int) Color {
	if v.t == nil {
		if idx < 0 || idx >= paletteSize {
			return DefaultColor()
		}
		return defaultPalette[idx]
	}
	return v.t.paletteColor(idx)
}

// DefaultForeground returns the default foreground color (OSC 10).
func (v LockedView) DefaultForeground() Color {
	if v.t == nil {
		return defaultDynamicColors.fg
	}
	return v.t.defaults.fg
}

// DefaultBackground returns the default background color (OSC 11).
func (v LockedView) DefaultBackground() Color {
	if v.t == nil {
		return defaultDynamicColors.bg
	}
	return v.t.defaults.bg
}

// CursorColor returns the cursor color (OSC 12).
func (v LockedView) CursorColor() Color {
	if v.t == nil {
		return defaultDynamicColors.cursor
	}
	return v.t.defaults.cursor
}

// setPaletteEntry sets one palette entry, returning whether it changed.
func (t *terminal) setPaletteEntry(idx int, c Color) bool {
	if idx < 0 || idx >= paletteSize {
		return false
	}
	if t.palette[idx] == c {
		return false
	}
	t.palette[idx] = c
	return true
}

// resetPalette restores the default palette entries, either all of them or the
// listed indexes.
func (t *terminal) resetPalette(indexes []int) bool {
	changed := false
	if len(indexes) == 0 {
		for i := range t.palette {
			if t.palette[i] != defaultPalette[i] {
				t.palette[i] = defaultPalette[i]
				changed = true
			}
		}
		return changed
	}
	for _, idx := range indexes {
		if idx < 0 || idx >= paletteSize {
			continue
		}
		if t.palette[idx] != defaultPalette[idx] {
			t.palette[idx] = defaultPalette[idx]
			changed = true
		}
	}
	return changed
}

// paletteChanged tells the frontend that palette resolution changed, so every
// cell rendered with a palette color may look different.
func (t *terminal) paletteChanged() {
	size := t.screen().Size()
	t.frontend.RegionChanged(LockedView{t: t}, Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRPalette)
}

// formatColorSpec formats c the way xterm replies to OSC color queries:
// rgb:rrrr/gggg/bbbb with 16-bit components.
func formatColorSpec(c Color) string {
	if c.IsDefault() {
		c = RGB(0, 0, 0)
	}
	r, g, b := c.RGB()
	return fmt.Sprintf("rgb:%04x/%04x/%04x", r*257, g*257, b*257)
}

// parseColorSpec parses an X11-style color spec: rgb:R/G/B with 1-4 hex digits
// per component, or #RGB, #RRGGBB, #RRRGGGBBB, or #RRRRGGGGBBBB.
func parseColorSpec(s string) (Color, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "rgb:") {
		rest := s[len("rgb:"):]
		parts := strings.Split(rest, "/")
		if len(parts) != 3 {
			return 0, false
		}
		var vals [3]int
		for i, p := range parts {
			v, ok := parseColorComponent(p)
			if !ok {
				return 0, false
			}
			vals[i] = v
		}
		return RGB(vals[0], vals[1], vals[2]), true
	}
	if strings.HasPrefix(s, "#") {
		hexDigits := s[1:]
		if len(hexDigits) == 0 || len(hexDigits)%3 != 0 {
			return 0, false
		}
		width := len(hexDigits) / 3
		var vals [3]int
		for i := 0; i < 3; i++ {
			v, ok := parseColorComponent(hexDigits[i*width : (i+1)*width])
			if !ok {
				return 0, false
			}
			vals[i] = v
		}
		return RGB(vals[0], vals[1], vals[2]), true
	}
	return 0, false
}

// parseColorComponent parses 1-4 hex digits, scaling to the 0-255 range.
func parseColorComponent(s string) (int, bool) {
	if len(s) < 1 || len(s) > 4 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	max := uint64(1)<<(4*uint(len(s))) - 1
	return int((v*255 + max/2) / max), true
}

// handleOSC4 implements OSC 4: set or query palette entries. The payload is a
// sequence of "index;color" pairs, where color is a color spec or "?" to
// query.
func (t *terminal) handleOSC4(payload []byte) {
	parts := strings.Split(string(payload), ";")
	changed := false
	for i := 0; i+1 < len(parts); i += 2 {
		idx, err := strconv.Atoi(parts[i])
		if err != nil || idx < 0 || idx >= paletteSize {
			continue
		}
		value := parts[i+1]
		if value == "?" {
			_ = t.writeReply([]byte("\x1b]4;" + strconv.Itoa(idx) + ";" +
				formatColorSpec(t.paletteColor(idx)) + "\x1b\\"))
			continue
		}
		c, ok := parseColorSpec(value)
		if !ok {
			continue
		}
		if t.setPaletteEntry(idx, c) {
			changed = true
		}
	}
	if changed {
		t.paletteChanged()
	}
}

// handleOSC104 implements OSC 104: reset the palette, either entirely (no
// parameters) or only the listed indexes.
func (t *terminal) handleOSC104(payload []byte) {
	s := strings.TrimSpace(string(payload))
	var indexes []int
	if s != "" {
		for _, p := range strings.Split(s, ";") {
			idx, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				continue
			}
			indexes = append(indexes, idx)
		}
	}
	if t.resetPalette(indexes) {
		t.paletteChanged()
	}
}

// handleOSCDynamicColor implements OSC 10/11/12 (set or query the default
// foreground, background, and cursor colors) and 110/111/112 (reset them).
func (t *terminal) handleOSCDynamicColor(kind int, payload []byte) {
	set := func(c Color) bool {
		switch kind {
		case 10, 110:
			if t.defaults.fg == c {
				return false
			}
			t.defaults.fg = c
		case 11, 111:
			if t.defaults.bg == c {
				return false
			}
			t.defaults.bg = c
		case 12, 112:
			if t.defaults.cursor == c {
				return false
			}
			t.defaults.cursor = c
		}
		return true
	}
	get := func() Color {
		switch kind {
		case 10, 110:
			return t.defaults.fg
		case 11, 111:
			return t.defaults.bg
		default:
			return t.defaults.cursor
		}
	}

	if kind >= 110 {
		// Reset forms ignore their payload.
		var d Color
		switch kind {
		case 110:
			d = defaultDynamicColors.fg
		case 111:
			d = defaultDynamicColors.bg
		default:
			d = defaultDynamicColors.cursor
		}
		if set(d) {
			t.paletteChanged()
		}
		return
	}

	value := strings.TrimSpace(string(payload))
	if value == "?" {
		replyKind := kind
		_ = t.writeReply([]byte("\x1b]" + strconv.Itoa(replyKind) + ";" +
			formatColorSpec(get()) + "\x1b\\"))
		return
	}
	c, ok := parseColorSpec(value)
	if !ok {
		return
	}
	if set(c) {
		t.paletteChanged()
	}
}
