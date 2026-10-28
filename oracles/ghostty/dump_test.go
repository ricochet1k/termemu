package main

import (
	"strings"
	"testing"
)

func TestDump_plainText(t *testing.T) {
	oracle := newTermemuOracle(10, 5)
	if err := oracle.Feed([]byte("hello")); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if d.Width != 10 || d.Height != 5 {
		t.Fatalf("size = %dx%d, want 10x5", d.Width, d.Height)
	}
	rowText := rowTextString(d.Rows[0])
	if !strings.HasPrefix(rowText, "hello") {
		t.Fatalf("first row = %q, want prefix %q", rowText, "hello")
	}
	if d.Cursor.X != 5 || d.Cursor.Y != 0 {
		t.Fatalf("cursor = (%d,%d), want (5,0)", d.Cursor.X, d.Cursor.Y)
	}
	if d.AltScreen {
		t.Fatal("expected main screen")
	}
}

func TestDump_cursorMoveAndColor(t *testing.T) {
	oracle := newTermemuOracle(20, 10)
	input := []byte("\x1b[2;3H\x1b[31mred\x1b[0m")
	if err := oracle.Feed(input); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if d.Cursor.X != 5 || d.Cursor.Y != 1 {
		t.Fatalf("cursor = (%d,%d), want (5,1)", d.Cursor.X, d.Cursor.Y)
	}
	// CUP is 1-indexed, so 2;3H -> (2,1) zero-based. "red" advances to (5,1).
	// Check the styled cell.
	cell := d.Rows[1][2]
	if cell.Text != "r" {
		t.Fatalf("cell text = %q, want %q", cell.Text, "r")
	}
	if cell.Style.Fg != "palette:1" {
		t.Fatalf("cell fg = %q, want palette:1", cell.Style.Fg)
	}
	if cell.Style.Modes != "" {
		t.Fatalf("cell modes = %q, want empty", cell.Style.Modes)
	}
}

func TestDump_altScreen(t *testing.T) {
	oracle := newTermemuOracle(20, 10)
	input := []byte("\x1b[?1049halt\x1b[?1049l")
	if err := oracle.Feed(input); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if d.AltScreen {
		t.Fatal("expected to be back on main screen after leaving alt")
	}
}

func TestDump_clearAndStyle(t *testing.T) {
	oracle := newTermemuOracle(8, 4)
	input := []byte("\x1b[1mbold\x1b[0m\x1b[2J\x1b[Hplain")
	if err := oracle.Feed(input); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	rowText := rowTextString(d.Rows[0])
	if !strings.HasPrefix(rowText, "plain") {
		t.Fatalf("first row = %q, want prefix %q", rowText, "plain")
	}
	pCell := d.Rows[0][0]
	if pCell.Text != "p" {
		t.Fatalf("first cell = %q, want %q", pCell.Text, "p")
	}
	if pCell.Style.Modes != "" {
		t.Fatalf("plain cell modes = %q, want empty", pCell.Style.Modes)
	}
}

func TestDump_wideCharacter(t *testing.T) {
	oracle := newTermemuOracle(10, 3)
	if err := oracle.Feed([]byte("a中b")); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if got := d.Rows[0][0].Text; got != "a" {
		t.Fatalf("cell 0 = %q, want %q", got, "a")
	}
	if got := d.Rows[0][1].Text; got != "中" {
		t.Fatalf("cell 1 = %q, want %q", got, "中")
	}
	if got := d.Rows[0][2].Text; got != "" {
		t.Fatalf("cell 2 (wide continuation) = %q, want empty", got)
	}
	if got := d.Rows[0][3].Text; got != "b" {
		t.Fatalf("cell 3 = %q, want %q", got, "b")
	}
}

func TestDump_combiningMark(t *testing.T) {
	oracle := newTermemuOracle(10, 3)
	// e followed by combining acute accent.
	if err := oracle.Feed([]byte("e\u0301x")); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	d, err := oracle.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if got := d.Rows[0][0].Text; got != "e\u0301" {
		t.Fatalf("cell 0 = %q, want %q", got, "e\u0301")
	}
	if got := d.Rows[0][1].Text; got != "x" {
		t.Fatalf("cell 1 = %q, want %q", got, "x")
	}
}

func rowTextString(row []Cell) string {
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
