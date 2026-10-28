package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ricochet1k/termemu"
	"github.com/rivo/uniseg"
)

// termemuOracle implements Oracle using the termemu library.
type termemuOracle struct {
	width  int
	height int
	feed   []byte
}

// newTermemuOracle creates an Oracle backed by a fresh termemu terminal of
// the given size. Bytes passed to Feed are accumulated and replayed when Dump
// is called.
func newTermemuOracle(width, height int) Oracle {
	return &termemuOracle{width: width, height: height}
}

// Feed accumulates VT output bytes.
func (o *termemuOracle) Feed(p []byte) error {
	o.feed = append(o.feed, p...)
	return nil
}

// Dump creates a termemu terminal, replays the accumulated feed, waits for the
// read loop to finish, and returns the resulting screen snapshot.
func (o *termemuOracle) Dump() (Dump, error) {
	pr, pw := io.Pipe()
	backend := termemu.NewNoPTYBackend(pr, io.Discard)
	term := termemu.NewWithMode(&termemu.EmptyFrontend{}, backend, termemu.TextReadModeGrapheme)
	if err := term.Resize(o.width, o.height); err != nil {
		return Dump{}, fmt.Errorf("resize: %w", err)
	}

	go func() {
		_, _ = pw.Write(o.feed)
		_ = pw.Close()
	}()

	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		return Dump{}, errors.New("timeout waiting for terminal read loop")
	}

	var d Dump
	term.View(func(v termemu.LockedView) {
		d = dumpFromView(v)
	})
	return d, nil
}

// Close is a no-op for the termemu oracle; resources are released in Dump.
func (o *termemuOracle) Close() error {
	return nil
}

// dumpFromView builds a Dump from a termemu LockedView. It is safe to call
// only inside Terminal.View or a Frontend callback.
func dumpFromView(v termemu.LockedView) Dump {
	w, h := v.Size()
	d := Dump{
		Width:  w,
		Height: h,
		Rows:   make([][]Cell, h),
	}
	cursor := v.CursorPos()
	d.Cursor.X = cursor.X
	d.Cursor.Y = cursor.Y
	d.CursorVisible = v.GetViewFlag(termemu.VFShowCursor)
	d.AltScreen = v.Snapshot().OnAltScreen

	if h == 0 || w == 0 {
		return d
	}

	lines := v.StyledLines(termemu.Region{X: 0, Y: 0, X2: w, Y2: h})
	for y, line := range lines {
		if y >= h {
			break
		}
		d.Rows[y] = cellsFromLine(line, w)
	}
	return d
}

// cellsFromLine expands a termemu Line into one Cell per column.
func cellsFromLine(line termemu.Line, width int) []Cell {
	cells := make([]Cell, width)
	col := 0
	for _, sp := range line.Spans {
		if col >= width {
			break
		}
		if sp.Width <= 0 {
			continue
		}
		if sp.Text == "" {
			// Blank span: empty text, default style.
			end := col + sp.Width
			if end > width {
				end = width
			}
			style := styleSummary(sp.Style)
			for ; col < end; col++ {
				cells[col].Style = style
			}
			continue
		}

		// Text span: walk grapheme clusters so emoji/ZWJ sequences stay in one
		// cell. Wide clusters leave an empty continuation cell.
		style := styleSummary(sp.Style)
		g := uniseg.NewGraphemes(sp.Text)
		for g.Next() {
			if col >= width {
				break
			}
			cluster := g.Str()
			cw := uniseg.StringWidth(cluster)
			if cw <= 0 {
				// Combining mark or other zero-width content attaches to the
				// previous cell.
				if col > 0 {
					cells[col-1].Text += cluster
				}
				continue
			}
			cells[col].Text = cluster
			cells[col].Style = style
			col++
			if cw > 1 && col < width {
				// Wide character continuation cell.
				cells[col].Style = style
				col++
			}
		}
	}
	return cells
}

// styleSummary converts a termemu Style to a portable StyleSummary.
func styleSummary(s termemu.Style) StyleSummary {
	return StyleSummary{
		Fg:        colorString(s, termemu.ComponentFG),
		Bg:        colorString(s, termemu.ComponentBG),
		Underline: colorString(s, termemu.ComponentUnderline),
		Modes:     modesString(s),
	}
}

// colorString returns a canonical string for the requested color component.
func colorString(s termemu.Style, comp termemu.ColorComponent) string {
	c, err := s.GetColor(comp)
	if err != nil {
		return "invalid"
	}
	switch {
	case c.IsDefault():
		return "default"
	case c.IsRGB():
		r, g, b := c.RGB()
		return fmt.Sprintf("rgb:%02x%02x%02x", r, g, b)
	case c.IsBright():
		return fmt.Sprintf("bright:%d", c.Index())
	default:
		return fmt.Sprintf("palette:%d", c.Index())
	}
}

// modesString returns a sorted, pipe-separated list of active mode names.
func modesString(s termemu.Style) string {
	modes := s.Modes()
	if len(modes) == 0 {
		return ""
	}
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = modeName(m)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

// modeName maps a termemu Mode to its portable name.
func modeName(m termemu.Mode) string {
	switch m {
	case termemu.ModeBold:
		return "bold"
	case termemu.ModeDim:
		return "dim"
	case termemu.ModeItalic:
		return "italic"
	case termemu.ModeUnderline:
		return "underline"
	case termemu.ModeBlink:
		return "blink"
	case termemu.ModeReverse:
		return "reverse"
	case termemu.ModeInvisible:
		return "invisible"
	case termemu.ModeStrike:
		return "strike"
	case termemu.ModeOverline:
		return "overline"
	case termemu.ModeDoubleUnderline:
		return "double-underline"
	case termemu.ModeFramed:
		return "framed"
	case termemu.ModeEncircled:
		return "encircled"
	case termemu.ModeRapidBlink:
		return "rapid-blink"
	default:
		return fmt.Sprintf("mode-%d", m)
	}
}
