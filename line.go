package termemu

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Span represents a run of text with consistent styling.
// If Text is not empty, it contains the text content. If Text is empty, the
// span represents Width spaces; a Width of zero (or less) is empty.
type Span struct {
	Style Style // Complete text styling (colors and modes)
	Text  string
	Width int
	// Link is the OSC 8 hyperlink URI active when the span was written.
	// Empty means no link. Blank/erased spans never carry a link.
	Link string
}

// Line holds a list of spans
type Line struct {
	Spans []Span
	Width int
}

func (l Line) PlainTextString() string {
	var out strings.Builder
	for _, sp := range l.Spans {
		if sp.Text != "" {
			out.WriteString(sp.Text)
			continue
		}
		for i := 0; i < sp.Width; i++ {
			out.WriteRune(' ')
		}
	}
	return out.String()
}

// Equivalent reports whether l and other cover the same cells with the same
// content, style, and OSC 8 link. It is a semantic comparison: span packing is
// ignored, so a run of blanks stored as Span{Width: n} (empty Text) is
// equivalent to the same blanks stored as a Text span of spaces. Wide
// characters occupy their full cell width, and zero-width content (combining
// marks, for example) is compared in order at the same cell position rather
// than being dropped.
//
// Equivalent does not compare the structural representation of the lines; use
// reflect.DeepEqual or cmp for that.
func (l Line) Equivalent(other Line) bool {
	if l.Width != other.Width {
		return false
	}

	left := lineCellWalker{line: &l}
	right := lineCellWalker{line: &other}
	for {
		a, okA := left.next()
		b, okB := right.next()
		if okA != okB {
			return false
		}
		if !okA {
			return true
		}
		if a != b {
			return false
		}
	}
}

// lineCell is one comparable unit of a line: either a positive-width cell or a
// zero-width rune at pos.
type lineCell struct {
	pos   int
	width int
	text  string
	style Style
	link  string
}

// lineCellWalker streams cells out of a Line without building an intermediate
// per-cell slice.
type lineCellWalker struct {
	line    *Line
	idx     int
	pos     int
	started bool

	// Current span state.
	blankLeft int
	textOff   int
}

func (w *lineCellWalker) next() (lineCell, bool) {
	for w.idx < len(w.line.Spans) {
		sp := &w.line.Spans[w.idx]
		if !w.started {
			w.started = true
			w.textOff = 0
			w.blankLeft = sp.Width
			if w.blankLeft < 0 {
				w.blankLeft = 0
			}
		}

		if sp.Text == "" {
			// Empty Text means a run of spaces, one cell per unit of Width.
			if w.blankLeft > 0 {
				w.blankLeft--
				cell := lineCell{pos: w.pos, width: 1, text: " ", style: sp.Style, link: sp.Link}
				w.pos++
				return cell, true
			}
			w.idx++
			w.started = false
			continue
		}

		// Text spans are walked rune by rune. Rune width is derived from the
		// rune itself so that a combining mark becomes a zero-width cell and a
		// wide rune occupies two cells, independent of how the text was packed.
		if w.textOff >= len(sp.Text) {
			w.idx++
			w.started = false
			continue
		}
		r, size := utf8.DecodeRuneInString(sp.Text[w.textOff:])
		w.textOff += size
		width := cellWidth(r)
		if width <= 0 {
			return lineCell{pos: w.pos, text: string(r), style: sp.Style, link: sp.Link}, true
		}
		cell := lineCell{pos: w.pos, width: width, text: string(r), style: sp.Style, link: sp.Link}
		w.pos += width
		return cell, true
	}
	return lineCell{}, false
}

// cellWidth returns the display width of r. ASCII takes the common fast path;
// other scripts (wide CJK/emoji, zero-width combining marks) use uniseg.
func cellWidth(r rune) int {
	if r >= 0x20 && r < 0x7f {
		return 1
	}
	if r < 0x20 || r == 0x7f {
		return 0
	}
	return uniseg.StringWidth(string(r))
}
