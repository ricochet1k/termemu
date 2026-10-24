package termemu

import (
	"bytes"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var testSequences = []struct {
	name     string
	sequence string
}{
	// // Original tests (basic functionality)
	// {"simple_text", "Hello, World!"},
	// {"newlines", "Line 1\nLine 2\nLine 3"},
	// {"colors", "\x1b[31mRed\x1b[0m \x1b[32mGreen\x1b[0m \x1b[34mBlue\x1b[0m"},
	// {"bold_underline", "\x1b[1mBold\x1b[0m \x1b[4mUnderline\x1b[0m"},
	// {"cursor_movement", "Start\x1b[5DMiddle\x1b[10Cend"},
	// {"clear_line", "Before\x1b[2KAfter"},
	// {"home_position", "A\x1b[HB"},
	// {"tabs", "Col1\tCol2\tCol3"},
	// {"backspace", "Hello\b\b\b\b\bWorld"},
	// {"carriage_return", "First\rSecond"},
	// {"sgr_combined", "\x1b[1;31;44mBold Red on Blue\x1b[0m"},
	// {"erase_display", "Top\nMiddle\nBottom\x1b[2J\x1b[HCleared"},
	// {"cursor_save_restore", "A\x1b[sB\x1b[uC"},
	// {"reverse_video", "Normal \x1b[7mReverse\x1b[27m Normal"},
	// {"256_colors", "\x1b[38;5;208mOrange\x1b[0m \x1b[48;5;21mBlue BG\x1b[0m"},

	// // Edge cases and additional escape sequences
	// {"empty_input", ""},
	// {"multiple_tabs", "A\t\t\tB"},
	// {"tab_at_eol", "Hello\t\nWorld"},
	// {"multiple_backspaces", "ABCDEF\b\b\b123"},
	// {"multiple_cr", "AAA\rBBB\rCCC"},
	// {"nested_save_restore", "A\x1b[sB\x1b[sC\x1b[uD\x1b[uE"},
	// {"cursor_down", "Line1\x1b[BLine2"},
	// {"cursor_up", "Line1\x1b[ALine0"},
	// {"cursor_right", "A\x1b[CB"},
	// {"cursor_left", "AB\x1b[DC"},
	// {"bold_italic_underline", "\x1b[1;3;4mAll on\x1b[0m"},
	// {"dim_mode", "\x1b[2mDim\x1b[0m"},
	// {"italic_mode", "\x1b[3mItalic\x1b[0m"},
	// {"blink_mode", "\x1b[5mBlink\x1b[0m"},
	// {"all_colors_basic", "\x1b[30m0\x1b[31m1\x1b[32m2\x1b[33m3\x1b[34m4\x1b[35m5\x1b[36m6\x1b[37m7\x1b[0m"},
	// {"bright_colors", "\x1b[90mDark\x1b[91mRed\x1b[92mGreen\x1b[93mYellow\x1b[0m"},
	// {"bg_colors_bright", "\x1b[100mBG Dark\x1b[101mBG Red\x1b[102mBG Green\x1b[0m"},
	// {"256_color_grayscale", "\x1b[38;5;232mBlack\x1b[0m \x1b[38;5;255mWhite\x1b[0m"},
	// {"mixed_256_colors", "\x1b[48;5;196m\x1b[38;5;255mRed BG White FG\x1b[0m"},
	// {"clear_line_left", "Hello\x1b[1KWorld"},
	// {"clear_line_right", "Hello\x1b[0KWorld"},
	// {"erase_above", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[1JEnd"},
	// {"erase_below", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[0JEnd"},
	// {"enable_cursor", "Hidden\x1b[?25hVisible"},
	// {"cursor_to_row_col", "A\x1b[5;10HB"},
	// {"multiple_newlines", "Line1\n\n\nLine5"},
	// {"mixed_colors_and_moves", "\x1b[31mRed\x1b[3CText\x1b[0m"},
	// {"reset_in_middle", "\x1b[1;31mBold Red\x1b[0mNormal\x1b[1mBold"},
	// {"consecutive_tabs", "\tA\tB\tC\t"},
	// {"space_and_tab", "A B\tC D"},
	// {"sgr_0_special_case", "\x1b[0mNormal"},
	// {"sgr_reset_all", "\x1b[1;31;44mRGB\x1b[0;0mReset"},
	// {"thick_text_modes", "\x1b[1;2;4;7;5mMultiple\x1b[0m"},
	// {"strikethrough", "\x1b[9mStrike\x1b[0m"},
	// {"double_underline", "\x1b[21mDouble\x1b[0m"},
	// {"overline", "\x1b[53mOverline\x1b[0m"},
	{"emoji_overwrite", "🐹a\n🐹b\x1b[Dz\n🐹c\x1b[D\x1b[Dy\n🐹c\x1b[D\x1b[D\x1b[Dx\n"},
}

// TestTTYFrontendNestedTerminalSpacing round-trips screen content through a
// TTYFrontend (render to ANSI, re-parse) and compares the two screens
// structurally.
func TestTTYFrontendNestedTerminalSpacing(t *testing.T) {
	width, height := 40, 4
	screenRegion := Region{X: 0, Y: 0, X2: width, Y2: height}

	for _, test := range testSequences {
		t.Run(test.name, func(t *testing.T) {
			r, w := io.Pipe()

			outer := NewWithMode(&EmptyFrontend{}, NewNoPTYBackend(r, io.Discard), TextReadModeRune).(*terminal)

			innerFrontend := NewTTYFrontend(w)
			inner := NewWithMode(innerFrontend, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeRune).(*terminal)

			_ = outer.Resize(width, height)
			_ = inner.Resize(width, height)
			innerFrontend.Attach(screenRegion)

			if err := inner.testFeedTerminalInputFromBackend([]byte(test.sequence), TextReadModeRune); err != nil {
				t.Fatal(err)
			}

			// The batched frontend writes one frame when the feed reaches its
			// batch boundary. A pipe Write returns once the outer read loop has
			// taken the bytes, not once it has parsed them, so close the writer
			// and wait for the outer loop to drain before comparing.
			_ = w.Close()
			<-outer.Done()
			if err := outer.Err(); err != nil {
				t.Fatal(err)
			}

			innerLines := inner.StyledLines(screenRegion)
			outerLines := outer.StyledLines(screenRegion)

			if len(innerLines) != len(outerLines) {
				t.Fatalf("line count differs: raw=%d TTYFrontend=%d", len(innerLines), len(outerLines))
			}
			for i := range innerLines {
				// Compare cell content, not span packing: the renderer emits
				// style deltas, so runs with the same style (for example a
				// text run followed by a blank run) merge when re-parsed.
				if innerLines[i].Equivalent(outerLines[i]) {
					continue
				}
				diff := cmp.Diff(innerLines[i], outerLines[i], cmp.AllowUnexported(Style{}))
				t.Fatalf("line %d differs between raw and TTYFrontend: %v", i, diff)
			}
		})
	}
}

// TestTTYFrontendBatchesDamage checks the batch contract: callbacks only
// accumulate, PrepareBatch/WriteBatch emit one frame, and an unchanged row is
// not re-emitted.
func TestTTYFrontendBatchesDamage(t *testing.T) {
	var out bytes.Buffer
	f := NewTTYFrontend(&out)
	term := newTerminal(f, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeGrapheme)
	f.SetTerminal(term)
	if err := term.Resize(10, 3); err != nil {
		t.Fatal(err)
	}
	f.Attach(Region{X: 0, Y: 0, X2: 10, Y2: 3})
	if out.Len() == 0 {
		t.Fatal("Attach did not paint")
	}

	before := out.Len()
	term.View(func(v LockedView) {
		f.RegionChanged(v, Region{X: 0, Y: 0, X2: 10, Y2: 1}, CRText)
	})
	if out.Len() != before {
		t.Fatalf("RegionChanged wrote %d bytes before the batch flush", out.Len()-before)
	}

	// The attach frame already painted row 0, so this flush is a no-op.
	term.flushFrontend()
	if out.Len() != before {
		t.Fatalf("flush repainted an unchanged row: %d bytes", out.Len()-before)
	}

	// A cursor move is batched too, and written by the flush.
	term.View(func(v LockedView) { f.CursorMoved(v, 2, 1) })
	if out.Len() != before {
		t.Fatalf("CursorMoved wrote %d bytes before the batch flush", out.Len()-before)
	}
	term.flushFrontend()
	if out.Len() == before {
		t.Fatal("flush did not write the pending cursor move")
	}
	if !bytes.Contains(out.Bytes()[before:], []byte("\x1b[2;3H")) {
		t.Fatalf("cursor update missing from %q", out.Bytes()[before:])
	}

	// A changed row is painted at the next flush.
	mark := out.Len()
	term.View(func(v LockedView) {
		term.screen().setCursorPos(0, 0)
		term.screen().writeString("hi", 2, false, TextReadModeGrapheme)
	})
	term.flushFrontend()
	if !bytes.Contains(out.Bytes()[mark:], []byte("hi")) {
		t.Fatalf("changed row not painted: %q", out.Bytes()[mark:])
	}
}
