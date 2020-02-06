package termemu

// Line holds a list of text blocks with associated colors
type Line struct {
	Spans []StyledSpan
	Text  []rune
	Width uint32
}

// StyledSpan has style colors, and a width
type StyledSpan struct {
	FG, BG Color
	// todo: should distinguish between width of characters on screen
	// and length in terms of number of runes
	Width uint32
}

func (l *Line) Append(text string, fg Color, bg Color) {
	runes := []rune(text)
	l.AppendRunes(runes, fg, bg)
}
func (l *Line) AppendRunes(runes []rune, fg Color, bg Color) {
	l.Text = append(l.Text, runes...)
	l.Spans = append(l.Spans,
		StyledSpan{
			FG:    fg,
			BG:    bg,
			Width: uint32(len(runes)),
		})
	l.Width += uint32(len(runes))
}

func (l *Line) Repeat(r rune, rep uint, fg Color, bg Color) {
	runes := make([]rune, rep)
	for i := range runes {
		runes[i] = r
	}
	l.AppendRunes(runes, fg, bg)
}
