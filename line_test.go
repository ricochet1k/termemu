package termemu

import "testing"

func TestLineEquivalent(t *testing.T) {
	def := NewStyle()
	bold := NewStyle()
	bold.SetMode(ModeBold)

	line := func(width int, spans ...Span) Line {
		return Line{Spans: spans, Width: width}
	}

	tests := []struct {
		name string
		a    Line
		b    Line
		want bool
	}{
		{
			name: "text versus repeated rune content differs",
			a:    line(2, Span{Style: def, Text: "ab", Width: 2}),
			b:    line(2, Span{Style: def, Rune: 'a', Width: 2}),
			want: false,
		},
		{
			name: "blank packing differs but cells match",
			a:    line(2, Span{Style: def, Text: "  ", Width: 2}),
			b:    line(2, Span{Style: def, Rune: ' ', Width: 2}),
			want: true,
		},
		{
			name: "same content different style",
			a:    line(1, Span{Style: def, Text: "a", Width: 1}),
			b:    line(1, Span{Style: bold, Text: "a", Width: 1}),
			want: false,
		},
		{
			name: "same content different link",
			a:    line(1, Span{Style: def, Text: "a", Width: 1, Link: "https://one.example"}),
			b:    line(1, Span{Style: def, Text: "a", Width: 1, Link: "https://two.example"}),
			want: false,
		},
		{
			name: "different widths",
			a:    line(1, Span{Style: def, Text: "a", Width: 1}),
			b:    line(2, Span{Style: def, Text: "a ", Width: 2}),
			want: false,
		},
		{
			name: "wide character with trailing blank padding",
			a: line(3,
				Span{Style: def, Text: "🐹", Width: 2},
				Span{Style: def, Rune: ' ', Width: 1},
			),
			b:    line(3, Span{Style: def, Text: "🐹 ", Width: 3}),
			want: true,
		},
		{
			name: "wide character alignment differs",
			a: line(3,
				Span{Style: def, Rune: ' ', Width: 1},
				Span{Style: def, Text: "🐹", Width: 2},
			),
			b:    line(3, Span{Style: def, Text: " 🐹", Width: 3}),
			want: true,
		},
		{
			name: "wide character versus narrow text",
			a:    line(2, Span{Style: def, Text: "🐹", Width: 2}),
			b:    line(2, Span{Style: def, Text: "ab", Width: 2}),
			want: false,
		},
		{
			name: "combining mark packed in one span",
			a:    line(1, Span{Style: def, Text: "a\u0301", Width: 1}),
			b:    line(1, Span{Style: def, Text: "a\u0301", Width: 1}),
			want: true,
		},
		{
			name: "combining mark split into zero-width text span",
			a: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0301", Width: 0},
			),
			b:    line(1, Span{Style: def, Text: "a\u0301", Width: 1}),
			want: true,
		},
		{
			name: "combining mark as zero-width rune span",
			a: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Rune: '\u0301', Width: 0},
			),
			b: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0301", Width: 0},
			),
			want: true,
		},
		{
			name: "zero-width ordering differs",
			a: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0301", Width: 0},
				Span{Style: def, Text: "\u0302", Width: 0},
			),
			b: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0302", Width: 0},
				Span{Style: def, Text: "\u0301", Width: 0},
			),
			want: false,
		},
		{
			name: "zero-width content is not dropped",
			a: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0301", Width: 0},
			),
			b:    line(1, Span{Style: def, Text: "a", Width: 1}),
			want: false,
		},
		{
			name: "zero-width style differs",
			a: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: def, Text: "\u0301", Width: 0},
			),
			b: line(1,
				Span{Style: def, Text: "a", Width: 1},
				Span{Style: bold, Text: "\u0301", Width: 0},
			),
			want: false,
		},
		{
			name: "empty lines",
			a:    line(0),
			b:    line(0),
			want: true,
		},
		{
			name: "empty versus zero-width content",
			a:    line(0),
			b:    line(0, Span{Style: def, Text: "\u0301", Width: 0}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equivalent(tt.b); got != tt.want {
				t.Errorf("a.Equivalent(b) = %v, want %v\na: %#v\nb: %#v", got, tt.want, tt.a, tt.b)
			}
			// Equivalence is symmetric.
			if got := tt.b.Equivalent(tt.a); got != tt.want {
				t.Errorf("b.Equivalent(a) = %v, want %v\na: %#v\nb: %#v", got, tt.want, tt.a, tt.b)
			}
		})
	}
}

func TestRowEquivalent(t *testing.T) {
	def := NewStyle()
	text := Row{Spans: []Span{{Style: def, Text: "  ", Width: 2}}, Width: 2, Wrapped: true, ID: 7}
	blank := Row{Spans: []Span{{Style: def, Rune: ' ', Width: 2}}, Width: 2, ID: 9}
	if !text.Equivalent(blank) {
		t.Fatal("rows with the same cells should be equivalent regardless of packing, Wrapped, or ID")
	}
	different := Row{Spans: []Span{{Style: def, Text: "ab", Width: 2}}, Width: 2}
	if text.Equivalent(different) {
		t.Fatal("rows with different cell content should not be equivalent")
	}
}
