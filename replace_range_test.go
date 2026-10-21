package termemu

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

func spanLineWidth(line *spanLine) int {
	total := 0
	for _, sp := range line.spans {
		total += sp.Width
	}
	return total
}

func spanLineText(line *spanLine) string {
	return Line{Spans: line.spans, Width: line.width}.PlainTextString()
}

func checkSpanLineInvariant(t *testing.T, line *spanLine, context string) {
	t.Helper()
	sum := spanLineWidth(line)
	if line.width != sum {
		t.Fatalf("%s: line.width = %d, sum(span.Width) = %d (spans: %#v)", context, line.width, sum, line.spans)
	}
	for i, sp := range line.spans {
		if sp.Width < 0 {
			t.Fatalf("%s: span %d has negative width: %#v", context, i, sp)
		}
	}
	plain := spanLineText(line)
	if got := uniseg.StringWidth(plain); got != line.width {
		t.Fatalf("%s: rendered width = %d, line.width = %d (plain: %q, spans: %#v)", context, got, line.width, plain, line.spans)
	}
}

func TestReplaceRange_SameWidthBoundaryReplacementKeepsLineWidth(t *testing.T) {
	style := NewStyle()
	lineValue := blankSpanLine(10, style)
	line := &lineValue

	replaceRange(line, 0, 5, Span{Style: style, Text: "hello", Width: 5}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after first write")

	// Replacing the whole first span ends exactly on a span boundary. The
	// old implementation derived the line width from the boundary scan and
	// shrank the line from 10 to 5.
	replaceRange(line, 0, 5, Span{Style: style, Text: "HELLO", Width: 5}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after boundary replacement")

	if got := spanLineText(line); got != "HELLO     " {
		t.Fatalf("plain text = %q, want %q", got, "HELLO     ")
	}
}

func TestReplaceRange_WideCharacterOverwrite(t *testing.T) {
	tests := []struct {
		name string
		x    int
		n    int
	}{
		{name: "left half", x: 0, n: 1},
		{name: "right half", x: 1, n: 1},
		{name: "both cells", x: 0, n: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			style := NewStyle()
			lineValue := blankSpanLine(10, style)
			line := &lineValue
			replaceRange(line, 0, 2, Span{Style: style, Text: "🐹", Width: 2}, TextReadModeGrapheme)
			checkSpanLineInvariant(t, line, "after wide write")

			replaceRange(line, tt.x, tt.n, Span{Style: style, Text: "x", Width: 1}, TextReadModeGrapheme)
			checkSpanLineInvariant(t, line, "after overwrite")

			if line.width != 9 {
				t.Fatalf("line.width = %d, want 9 (wide char replaced by one cell)", line.width)
			}
			plain := spanLineText(line)
			if strings.Contains(plain, "🐹") {
				t.Fatalf("wide character was not fully replaced: %q", plain)
			}
			if plain != "x        " {
				t.Fatalf("plain text = %q, want %q", plain, "x        ")
			}
		})
	}
}

func TestReplaceRange_DeleteThroughWideCharacter(t *testing.T) {
	style := NewStyle()
	lineValue := blankSpanLine(10, style)
	line := &lineValue
	replaceRange(line, 2, 2, Span{Style: style, Text: "🐹", Width: 2}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after wide write")

	// Delete the blank at cell 1 plus the left half of the wide character;
	// the window expands to consume the whole wide character.
	replaceRange(line, 1, 2, Span{}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after delete")

	if line.width != 7 {
		t.Fatalf("line.width = %d, want 7", line.width)
	}
	if plain := spanLineText(line); plain != "       " {
		t.Fatalf("plain text = %q, want seven spaces", plain)
	}
}

func TestReplaceRange_WideOverNarrowAndEndOfLine(t *testing.T) {
	style := NewStyle()
	lineValue := blankSpanLine(10, style)
	line := &lineValue

	replaceRange(line, 0, 1, Span{Style: style, Text: "a", Width: 1}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after narrow write")

	replaceRange(line, 0, 2, Span{Style: style, Text: "🐹", Width: 2}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after wide over narrow")
	if plain := spanLineText(line); plain != "🐹        " {
		t.Fatalf("plain text = %q, want wide char plus spaces", plain)
	}

	replaceRange(line, 9, 1, Span{Style: style, Text: "Z", Width: 1}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after end-of-line replacement")
	if plain := spanLineText(line); plain != "🐹       Z" {
		t.Fatalf("plain text = %q, want wide char plus trailing Z", plain)
	}

	// Appending exactly at the end of the line.
	replaceRange(line, line.width, 0, Span{Style: style, Text: "Q", Width: 1}, TextReadModeGrapheme)
	checkSpanLineInvariant(t, line, "after append")
	if line.width != 11 {
		t.Fatalf("line.width = %d, want 11 after append", line.width)
	}
}

func TestReplaceRange_RandomizedInvariant(t *testing.T) {
	style := NewStyle()
	inserts := []Span{
		{Style: style, Width: 1},
		{Style: style, Width: 3},
		{Style: style, Text: "a", Width: 1},
		{Style: style, Text: "bc", Width: 2},
		{Style: style, Text: "🐹", Width: 2},
		{Style: style, Text: "世", Width: 2},
		{Style: style, Text: "界世", Width: 4},
		{Style: style, Text: "e\u0301", Width: 1},
		{Style: style, Text: "\u0301", Width: 0},
		{},
	}

	seeds := []int64{1, 2, 3, 42, 0x5ee1}
	for _, seed := range seeds {
		rng := rand.New(rand.NewSource(seed))
		lineValue := blankSpanLine(12, style)
		line := &lineValue
		for step := 0; step < 1000; step++ {
			x := 0
			if line.width > 0 {
				x = rng.Intn(line.width + 1)
			}
			maxDelete := line.width - x
			if maxDelete > 4 {
				maxDelete = 4
			}
			n := 0
			if maxDelete > 0 {
				n = rng.Intn(maxDelete + 1)
			}
			insert := inserts[rng.Intn(len(inserts))]
			op := fmt.Sprintf("seed %d step %d: replaceRange(x=%d, n=%d, insert=%#v), line.width=%d", seed, step, x, n, insert, line.width)
			replaceRange(line, x, n, insert, TextReadModeGrapheme)
			checkSpanLineInvariant(t, line, op)
			if t.Failed() {
				return
			}
		}
	}
}
