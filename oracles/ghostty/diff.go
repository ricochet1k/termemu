package main

import (
	"fmt"
	"strings"
)

// Divergence records one difference between two Dump values.
type Divergence struct {
	Row      int    // -1 for size/row-level mismatches
	Col      int    // -1 for size/row-level mismatches
	Expected string // human-readable expected value
	Got      string // human-readable actual value
	Context  []string
}

// String returns a short, stable representation of the divergence.
func (d Divergence) String() string {
	pos := "size"
	if d.Row >= 0 && d.Col >= 0 {
		pos = fmt.Sprintf("row %d col %d", d.Row, d.Col)
	} else if d.Row >= 0 {
		pos = fmt.Sprintf("row %d", d.Row)
	}
	return fmt.Sprintf("%s: expected %q got %q", pos, d.Expected, d.Got)
}

// Diff compares two dumps and returns a list of divergences. Width/height
// mismatch is reported as a single divergence; per-cell differences are
// reported individually with a short surrounding context window.
func Diff(expected, got Dump) []Divergence {
	const contextWindow = 3
	var divs []Divergence

	if expected.Width != got.Width || expected.Height != got.Height {
		divs = append(divs, Divergence{
			Row:      -1,
			Col:      -1,
			Expected: fmt.Sprintf("%dx%d", expected.Width, expected.Height),
			Got:      fmt.Sprintf("%dx%d", got.Width, got.Height),
			Context:  contextRows(expected, got, 0, contextWindow),
		})
		return divs
	}

	w, h := expected.Width, expected.Height
	for y := 0; y < h; y++ {
		expRow := rowOrEmpty(expected.Rows, y, w)
		gotRow := rowOrEmpty(got.Rows, y, w)
		for x := 0; x < w; x++ {
			if expRow[x] != gotRow[x] {
				divs = append(divs, Divergence{
					Row:      y,
					Col:      x,
					Expected: cellString(expRow[x]),
					Got:      cellString(gotRow[x]),
					Context:  contextRows(expected, got, y, contextWindow),
				})
			}
		}
	}

	if expected.Cursor != got.Cursor {
		divs = append(divs, Divergence{
			Row:      -1,
			Col:      -1,
			Expected: fmt.Sprintf("cursor (%d,%d)", expected.Cursor.X, expected.Cursor.Y),
			Got:      fmt.Sprintf("cursor (%d,%d)", got.Cursor.X, got.Cursor.Y),
			Context:  contextRows(expected, got, 0, contextWindow),
		})
	}
	if expected.CursorVisible != got.CursorVisible {
		divs = append(divs, Divergence{
			Row:      -1,
			Col:      -1,
			Expected: fmt.Sprintf("cursorVisible=%v", expected.CursorVisible),
			Got:      fmt.Sprintf("cursorVisible=%v", got.CursorVisible),
			Context:  contextRows(expected, got, 0, contextWindow),
		})
	}
	if expected.AltScreen != got.AltScreen {
		divs = append(divs, Divergence{
			Row:      -1,
			Col:      -1,
			Expected: fmt.Sprintf("altScreen=%v", expected.AltScreen),
			Got:      fmt.Sprintf("altScreen=%v", got.AltScreen),
			Context:  contextRows(expected, got, 0, contextWindow),
		})
	}

	return divs
}

// rowOrEmpty returns row y padded to width w with blank cells.
func rowOrEmpty(rows [][]Cell, y, w int) []Cell {
	if y >= 0 && y < len(rows) {
		row := rows[y]
		if len(row) >= w {
			return row[:w]
		}
		padded := make([]Cell, w)
		copy(padded, row)
		return padded
	}
	return make([]Cell, w)
}

// cellString returns a compact representation of a cell for diff output.
func cellString(c Cell) string {
	if c.Text == "" {
		return fmt.Sprintf("[blank style=%+v]", c.Style)
	}
	return fmt.Sprintf("%q style=%+v", c.Text, c.Style)
}

// contextRows returns a few rows around y from each dump as strings, suitable
// for including in a divergence report. y is clamped to the available rows.
func contextRows(expected, got Dump, y, window int) []string {
	if expected.Height != got.Height {
		return nil
	}
	start := y - window
	if start < 0 {
		start = 0
	}
	end := y + window + 1
	if end > expected.Height {
		end = expected.Height
	}
	out := make([]string, 0, (end-start)*2)
	for row := start; row < end; row++ {
		out = append(out, fmt.Sprintf("E%d: %s", row, rowText(expected.Rows, row, expected.Width)))
		out = append(out, fmt.Sprintf("G%d: %s", row, rowText(got.Rows, row, got.Width)))
	}
	return out
}

// rowText renders a row as plain text, padding blanks to spaces.
func rowText(rows [][]Cell, y, w int) string {
	row := rowOrEmpty(rows, y, w)
	var b strings.Builder
	for _, c := range row {
		if c.Text == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}
