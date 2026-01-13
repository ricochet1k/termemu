package termemu

import "unicode/utf8"

// Span represents a run of text with consistent styling.
// If Text is not empty, it contains the text content.
// If Text is empty, it represents Rune repeated Width times.
type Span struct {
	Style Style // Complete text styling (colors and modes)
	Text  string
	Rune  rune
	Width int
}

// Line holds a list of spans
type Line struct {
	Spans []Span
	Width int
}

func (l *Line) Append(text string, style *Style) {
	if len(text) == 0 {
		return
	}
	// Note: this assumes width == rune count, which is only true for simple text.
	// Real terminal logic should provide the width.
	width := utf8.RuneCountInString(text)
	l.Spans = append(l.Spans, Span{
		Style: *style,
		Text:  text,
		Width: width,
	})
	l.Width += width
}

func (l *Line) AppendRunes(runes []rune, style *Style) {
	if len(runes) == 0 {
		return
	}
	l.Append(string(runes), style)
}

func (l *Line) Repeat(r rune, rep int, style *Style) {
	if rep <= 0 {
		return
	}
	l.Spans = append(l.Spans, Span{
		Style: *style,
		Rune:  r,
		Width: rep,
	})
	l.Width += rep
}
