//go:build libghostty

package main

import (
	"fmt"
	"sort"
	"strings"

	"go.mitchellh.com/libghostty"
)

// ghosttyOracle implements Oracle using libghostty-vt via go-libghostty.
type ghosttyOracle struct {
	term   *libghostty.Terminal
	width  int
	height int
}

// newGhosttyOracle creates an Oracle backed by a libghostty terminal of the
// given size.
func newGhosttyOracle(width, height int) (Oracle, error) {
	term, err := libghostty.NewTerminal(libghostty.WithSize(uint16(width), uint16(height)))
	if err != nil {
		return nil, fmt.Errorf("libghostty NewTerminal: %w", err)
	}
	return &ghosttyOracle{term: term, width: width, height: height}, nil
}

// Feed writes VT output bytes into the libghostty terminal.
func (o *ghosttyOracle) Feed(p []byte) error {
	o.term.VTWrite(p)
	return nil
}

// Dump returns a Dump built from the current libghostty terminal state.
func (o *ghosttyOracle) Dump() (Dump, error) {
	cols, err := o.term.Cols()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty Cols: %w", err)
	}
	rows, err := o.term.Rows()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty Rows: %w", err)
	}
	cx, err := o.term.CursorX()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty CursorX: %w", err)
	}
	cy, err := o.term.CursorY()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty CursorY: %w", err)
	}
	visible, err := o.term.CursorVisible()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty CursorVisible: %w", err)
	}
	screen, err := o.term.ActiveScreen()
	if err != nil {
		return Dump{}, fmt.Errorf("libghostty ActiveScreen: %w", err)
	}

	d := Dump{
		Width:         int(cols),
		Height:        int(rows),
		Rows:          make([][]Cell, rows),
		CursorVisible: visible,
		AltScreen:     screen == libghostty.ScreenAlternate,
	}
	d.Cursor.X = int(cx)
	d.Cursor.Y = int(cy)

	for y := 0; y < int(rows); y++ {
		d.Rows[y] = make([]Cell, cols)
		for x := 0; x < int(cols); x++ {
			cell, style, err := o.cellAt(x, y)
			if err != nil {
				return Dump{}, err
			}
			d.Rows[y][x] = Cell{Text: cell, Style: style}
		}
	}
	return d, nil
}

// Close releases the libghostty terminal.
func (o *ghosttyOracle) Close() error {
	o.term.Close()
	return nil
}

// cellAt returns the text and style for one cell.
func (o *ghosttyOracle) cellAt(x, y int) (string, StyleSummary, error) {
	ref, err := o.term.GridRef(libghostty.Point{
		Tag: libghostty.PointTagActive,
		X:   uint16(x),
		Y:   uint32(y),
	})
	if err != nil {
		return "", StyleSummary{}, fmt.Errorf("libghostty GridRef(%d,%d): %w", x, y, err)
	}

	cell, err := ref.Cell()
	if err != nil {
		return "", StyleSummary{}, fmt.Errorf("libghostty Cell(%d,%d): %w", x, y, err)
	}

	hasText, err := cell.HasText()
	if err != nil {
		return "", StyleSummary{}, fmt.Errorf("libghostty HasText(%d,%d): %w", x, y, err)
	}

	var text string
	if hasText {
		wide, err := cell.Wide()
		if err != nil {
			return "", StyleSummary{}, fmt.Errorf("libghostty Wide(%d,%d): %w", x, y, err)
		}
		// Spacer cells are continuations of a wide character; represent them
		// as blank so they match the termemu dump convention.
		if wide != libghostty.CellWideSpacerTail && wide != libghostty.CellWideSpacerHead {
			tag, err := cell.ContentTag()
			if err != nil {
				return "", StyleSummary{}, fmt.Errorf("libghostty ContentTag(%d,%d): %w", x, y, err)
			}
			if tag == libghostty.CellContentCodepoint {
				cp, err := cell.Codepoint()
				if err != nil {
					return "", StyleSummary{}, fmt.Errorf("libghostty Codepoint(%d,%d): %w", x, y, err)
				}
				text = string(rune(cp))
			} else {
				graphemes, err := ref.Graphemes()
				if err != nil {
					return "", StyleSummary{}, fmt.Errorf("libghostty Graphemes(%d,%d): %w", x, y, err)
				}
				text = codepointsToString(graphemes)
			}
		}
	}

	style, err := ref.Style()
	if err != nil {
		return "", StyleSummary{}, fmt.Errorf("libghostty Style(%d,%d): %w", x, y, err)
	}
	return text, ghosttyStyleSummary(style), nil
}

// codepointsToString converts a slice of Unicode codepoints to a Go string.
func codepointsToString(cps []uint32) string {
	runes := make([]rune, len(cps))
	for i, cp := range cps {
		runes[i] = rune(cp)
	}
	return string(runes)
}

// ghosttyStyleSummary converts a libghostty Style to a portable StyleSummary.
func ghosttyStyleSummary(s *libghostty.Style) StyleSummary {
	return StyleSummary{
		Fg:        ghosttyColorString(s.FgColor()),
		Bg:        ghosttyColorString(s.BgColor()),
		Underline: ghosttyColorString(s.UnderlineColor()),
		Modes:     ghosttyModesString(s),
	}
}

// ghosttyColorString returns a canonical string for a libghostty style color.
func ghosttyColorString(c libghostty.StyleColor) string {
	switch c.Tag {
	case libghostty.StyleColorNone:
		return "default"
	case libghostty.StyleColorPalette:
		return fmt.Sprintf("palette:%d", c.Palette)
	case libghostty.StyleColorRGB:
		return fmt.Sprintf("rgb:%02x%02x%02x", c.RGB.R, c.RGB.G, c.RGB.B)
	default:
		return fmt.Sprintf("unknown:%d", c.Tag)
	}
}

// ghosttyModesString returns a sorted, pipe-separated list of active mode names.
func ghosttyModesString(s *libghostty.Style) string {
	var modes []string
	if s.Bold() {
		modes = append(modes, "bold")
	}
	if s.Faint() {
		modes = append(modes, "dim")
	}
	if s.Italic() {
		modes = append(modes, "italic")
	}
	switch s.Underline() {
	case libghostty.UnderlineSingle:
		modes = append(modes, "underline")
	case libghostty.UnderlineDouble:
		modes = append(modes, "double-underline")
	case libghostty.UnderlineCurly:
		modes = append(modes, "curly-underline")
	case libghostty.UnderlineDotted:
		modes = append(modes, "dotted-underline")
	case libghostty.UnderlineDashed:
		modes = append(modes, "dashed-underline")
	}
	if s.Blink() {
		modes = append(modes, "blink")
	}
	if s.Inverse() {
		modes = append(modes, "reverse")
	}
	if s.Invisible() {
		modes = append(modes, "invisible")
	}
	if s.Strikethrough() {
		modes = append(modes, "strike")
	}
	if s.Overline() {
		modes = append(modes, "overline")
	}
	sort.Strings(modes)
	return strings.Join(modes, "|")
}
