package main

// Oracle feeds an emulator a VT byte stream and then extracts a comparable
// screen snapshot.
type Oracle interface {
	Feed(p []byte) error
	Dump() (Dump, error)
	Close() error
}

// Cell is one screen cell: the text that occupies it and a summary of its
// style. Empty Text represents a blank/erased cell.
type Cell struct {
	Text  string
	Style StyleSummary
}

// StyleSummary is a portable, string-based representation of a cell's style.
// Colors use the same notation in every oracle implementation so dumps from
// different emulators can be compared directly.
type StyleSummary struct {
	Fg, Bg, Underline string
	Modes             string // sorted, pipe-separated mode names
}

// Dump is a screen snapshot that can be diffed between emulators.
type Dump struct {
	Width  int
	Height int
	Rows   [][]Cell
	Cursor struct {
		X int
		Y int
	}
	CursorVisible bool
	AltScreen     bool
}
