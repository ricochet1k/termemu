package main

import (
	"strings"
	"testing"
)

func TestDiff_identical(t *testing.T) {
	a := newDump(3, 2, []string{
		"abc",
		"def",
	})
	b := newDump(3, 2, []string{
		"abc",
		"def",
	})
	divs := Diff(a, b)
	if len(divs) != 0 {
		t.Fatalf("expected no divergences, got %d: %v", len(divs), divs)
	}
}

func TestDiff_sizeMismatch(t *testing.T) {
	a := newDump(3, 2, []string{"abc", "def"})
	b := newDump(4, 2, []string{"abcx", "defx"})
	divs := Diff(a, b)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence, got %d", len(divs))
	}
	if divs[0].Row != -1 || divs[0].Col != -1 {
		t.Fatalf("size mismatch should be at (-1,-1), got (%d,%d)", divs[0].Row, divs[0].Col)
	}
}

func TestDiff_cellMismatch(t *testing.T) {
	a := newDump(3, 2, []string{"abc", "def"})
	b := newDump(3, 2, []string{"abX", "def"})
	divs := Diff(a, b)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence, got %d: %v", len(divs), divs)
	}
	d := divs[0]
	if d.Row != 0 || d.Col != 2 {
		t.Fatalf("divergence at (%d,%d), want (0,2)", d.Row, d.Col)
	}
	if !strings.Contains(d.Expected, "c") || !strings.Contains(d.Got, "X") {
		t.Fatalf("expected/got mismatch: %v", d)
	}
	if len(d.Context) == 0 {
		t.Fatal("expected context rows")
	}
}

func TestDiff_cursor(t *testing.T) {
	a := newDump(2, 1, []string{"ab"})
	b := newDump(2, 1, []string{"ab"})
	a.Cursor.X = 0
	b.Cursor.X = 1
	divs := Diff(a, b)
	found := false
	for _, d := range divs {
		if strings.Contains(d.Expected, "cursor") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cursor divergence, got %v", divs)
	}
}

func TestDiff_altScreen(t *testing.T) {
	a := newDump(2, 1, []string{"ab"})
	b := newDump(2, 1, []string{"ab"})
	a.AltScreen = false
	b.AltScreen = true
	divs := Diff(a, b)
	found := false
	for _, d := range divs {
		if strings.Contains(d.Expected, "altScreen") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected alt-screen divergence, got %v", divs)
	}
}

func TestDiff_styleMismatch(t *testing.T) {
	a := newDump(2, 1, []string{"ab"})
	b := newDump(2, 1, []string{"ab"})
	b.Rows[0][1].Style.Fg = "rgb:ff0000"
	divs := Diff(a, b)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divergence, got %d: %v", len(divs), divs)
	}
	if divs[0].Row != 0 || divs[0].Col != 1 {
		t.Fatalf("divergence at (%d,%d), want (0,1)", divs[0].Row, divs[0].Col)
	}
}

// newDump builds a Dump of the given size filled with the supplied rows.
// Each row string is split into one Cell per rune; all styles are default.
func newDump(width, height int, rows []string) Dump {
	d := Dump{
		Width:  width,
		Height: height,
		Rows:   make([][]Cell, height),
	}
	defaultStyle := StyleSummary{Fg: "default", Bg: "default", Underline: "default"}
	for y := 0; y < height; y++ {
		d.Rows[y] = make([]Cell, width)
		if y < len(rows) {
			runes := []rune(rows[y])
			for x := 0; x < width && x < len(runes); x++ {
				d.Rows[y][x] = Cell{Text: string(runes[x]), Style: defaultStyle}
			}
		}
	}
	return d
}
