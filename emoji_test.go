package termemu

import (
	"testing"
)

func TestTraceReplaceRange(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	screen := term.screen()
	spanScreen := screen.(*spanScreen)
	
	// Create scenario: "🐹c" in a single span
	spanScreen.rawWriteSpan(0, 0, Span{Style: NewStyle(), Text: "🐹c", Width: 3}, CRText)
	
	// Manually call replaceRange to trace what happens
	line := &spanScreen.lines[0]
	insert := Span{Style: NewStyle(), Text: "y", Width: 1}
	
	t.Logf("Calling replaceRange(line, x=1, n=1, insert='y')")
	t.Logf("Line before: spans=%d, width=%d", len(line.spans), line.width)
	for i, sp := range line.spans[:min(3, len(line.spans))] {
		t.Logf("  Span %d: Text=%q, Width=%d", i, sp.Text, sp.Width)
	}
	
	replaceRange(line, 1, 1, insert, TextReadModeRune)
	
	t.Logf("Line after: spans=%d, width=%d", len(line.spans), line.width)
	for i, sp := range line.spans[:min(5, len(line.spans))] {
		if sp.Width > 0 {
			t.Logf("  Span %d: Text=%q, Rune=%q, Width=%d", i, sp.Text, sp.Rune, sp.Width)
		}
	}
}
