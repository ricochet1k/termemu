package termemu

import (
	"strings"
	"testing"
)

func TestReplaceRange_ReplacesCompleteStyle(t *testing.T) {
	boldRed := NewStyle()
	if err := boldRed.SetColor(ComponentFG, Color256(1)); err != nil {
		t.Fatal(err)
	}
	boldRed.SetMode(ModeBold)

	dimRed := boldRed
	dimRed.Reset(ModeBold)
	dimRed.SetMode(ModeDim)

	italic := NewStyle()
	italic.SetMode(ModeItalic)

	underlineRed := NewStyle()
	underlineRed.SetMode(ModeUnderline)
	if err := underlineRed.SetColor(ComponentUnderline, Color256(1)); err != nil {
		t.Fatal(err)
	}

	underlineBlue := underlineRed
	if err := underlineBlue.SetColor(ComponentUnderline, Color256(4)); err != nil {
		t.Fatal(err)
	}

	manyModes := NewStyle()
	manyModes.SetMode(ModeReverse, ModeInvisible, ModeStrike)
	strikeOnly := NewStyle()
	strikeOnly.SetMode(ModeStrike)

	tests := []struct {
		name    string
		old     Style
		new     Style
		oldLink string
		newLink string
	}{
		{name: "bold to dim", old: boldRed, new: dimRed},
		{name: "italic on to off", old: italic, new: NewStyle()},
		{name: "italic off to on", old: NewStyle(), new: italic},
		{name: "underline color change", old: underlineRed, new: underlineBlue},
		{name: "reverse invisible strike change", old: manyModes, new: strikeOnly},
		{name: "same style different text", old: boldRed, new: boldRed},
		{name: "different link", old: boldRed, new: boldRed, oldLink: "https://old.example", newLink: "https://new.example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := blankSpanLine(10, tt.old)
			replaceRange(&line, 0, 1, Span{Style: tt.old, Text: "A", Width: 1, Link: tt.oldLink}, TextReadModeGrapheme)
			replaceRange(&line, 0, 1, Span{Style: tt.new, Text: "B", Width: 1, Link: tt.newLink}, TextReadModeGrapheme)

			idx, _ := findSpanAtX(&line, 0)
			if idx >= len(line.spans) {
				t.Fatalf("no span at cell 0: %#v", line.spans)
			}
			got := line.spans[idx]
			if got.Text != "B" || got.Width != 1 {
				t.Fatalf("cell content = %q width %d, want B width 1", got.Text, got.Width)
			}
			if got.Style != tt.new {
				t.Fatalf("style = %#v, want %#v (old mode bits must be gone)", got.Style, tt.new)
			}
			if got.Link != tt.newLink {
				t.Fatalf("link = %q, want %q", got.Link, tt.newLink)
			}
			if line.width != 10 {
				t.Fatalf("line width = %d, want 10", line.width)
			}
		})
	}
}

func TestTerminalOverwriteReplacesOldModes(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeGrapheme)
	if err := term.testFeedTerminalInputFromBackend([]byte("\x1b[1;31mA\x1b[H\x1b[0;2;31mB"), TextReadModeGrapheme); err != nil {
		t.Fatal(err)
	}

	line := term.StyledLine(0, 1, 0)
	if len(line.Spans) == 0 {
		t.Fatal("no spans")
	}
	got := line.Spans[0]
	if got.Text != "B" {
		t.Fatalf("cell text = %q, want B", got.Text)
	}
	if got.Style.TestMode(ModeBold) {
		t.Fatal("overwritten cell kept bold mode")
	}
	if !got.Style.TestMode(ModeDim) {
		t.Fatal("overwritten cell is missing dim mode")
	}
	if fg, _ := got.Style.GetColor(ComponentFG); fg != Color256(1) {
		t.Fatalf("foreground = %#v, want color 1", fg)
	}
}

func TestReplaceRange_SameStyleSplicesText(t *testing.T) {
	style := NewStyle()
	line := blankSpanLine(6, style)
	replaceRange(&line, 0, 3, Span{Style: style, Text: "AAA", Width: 3}, TextReadModeGrapheme)
	replaceRange(&line, 1, 1, Span{Style: style, Text: "B", Width: 1}, TextReadModeGrapheme)

	if got := spanLineText(&line); got != "ABA   " {
		t.Fatalf("plain text = %q, want %q", got, "ABA   ")
	}
	textSpans := 0
	for _, sp := range line.spans {
		if sp.Text != "" {
			textSpans++
		}
	}
	if textSpans != 1 {
		t.Fatalf("same-style overwrite should stay in one text span, got %d spans: %#v", textSpans, line.spans)
	}
}

func TestReplaceRange_DifferentLinkDoesNotMerge(t *testing.T) {
	style := NewStyle()
	const link = "https://example.com"
	line := blankSpanLine(6, style)
	replaceRange(&line, 0, 3, Span{Style: style, Text: "AAA", Width: 3}, TextReadModeGrapheme)
	replaceRange(&line, 1, 1, Span{Style: style, Text: "B", Width: 1, Link: link}, TextReadModeGrapheme)

	if got := spanLineText(&line); got != "ABA   " {
		t.Fatalf("plain text = %q, want %q", got, "ABA   ")
	}

	var linked []Span
	for _, sp := range line.spans {
		if sp.Link != "" {
			linked = append(linked, sp)
		}
	}
	if len(linked) != 1 {
		t.Fatalf("linked content should stay in its own span, got %#v", line.spans)
	}
	if linked[0].Text != "B" || linked[0].Width != 1 {
		t.Fatalf("linked span = %#v, want just B", linked[0])
	}
	// The unlinked neighbors must not have been merged into the linked span.
	for _, sp := range line.spans {
		if sp.Link == "" && strings.Contains(sp.Text, "B") {
			t.Fatalf("linked text leaked into an unlinked span: %#v", line.spans)
		}
	}
}

func TestMergeIntoPreviousCell_DoesNotMergeDifferentStyleOrLink(t *testing.T) {
	const mark = "\u0301"
	tests := []struct {
		name string
		link string
	}{
		{name: "different style"},
		{name: "different link", link: "https://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSpanScreen(&EmptyFrontend{})
			s.setSize(10, 2)
			baseStyle := s.style
			s.writeString("A", 1, false, TextReadModeGrapheme)

			newStyle := baseStyle
			if tt.link == "" {
				newStyle.SetMode(ModeBold)
			}
			s.style = newStyle
			s.setLink(tt.link)
			s.mergeIntoPreviousCell(mark)

			line := &s.lines[0]
			if got := spanLineText(line); !strings.HasPrefix(got, "A"+mark) {
				t.Fatalf("plain text = %q, want prefix %q", got, "A"+mark)
			}
			if line.spans[0].Text != "A" || line.spans[0].Style != baseStyle || line.spans[0].Link != "" {
				t.Fatalf("base cell was modified: %#v", line.spans[0])
			}
			if len(line.spans) < 2 || line.spans[1].Width != 0 || line.spans[1].Text != mark {
				t.Fatalf("mark should be a separate zero-width span, got %#v", line.spans)
			}
			if line.spans[1].Style != newStyle || line.spans[1].Link != tt.link {
				t.Fatalf("mark span style/link = %#v/%q, want %#v/%q", line.spans[1].Style, line.spans[1].Link, newStyle, tt.link)
			}
		})
	}
}
