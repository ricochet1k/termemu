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

func TestTTYFrontendNestedTerminalSpacing(t *testing.T) {
	width, height := 40, 4
	screenRegion := Region{X: 0, Y: 0, X2: width, Y2: height}

	for _, test := range testSequences {
		t.Run(test.name, func(t *testing.T) {
			r, w := io.Pipe()

			outer := NewWithMode(&EmptyFrontend{}, NewNoPTYBackend(r, io.Discard), TextReadModeRune).(*terminal)

			innerFrontend := NewTTYFrontend(nil, w)
			inner := NewWithMode(innerFrontend, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeRune).(*terminal)
			innerFrontend.SetTerminal(inner)

			outer.Resize(width, height)
			inner.Resize(width, height)
			innerFrontend.Attach(screenRegion)

			if err := inner.testFeedTerminalInputFromBackend([]byte(test.sequence), TextReadModeRune); err != nil {
				t.Fatal(err)
			}

			inner.Lock()
			innerLines := inner.StyledLines(screenRegion)
			inner.Unlock()

			outer.Lock()
			outerLines := outer.StyledLines(screenRegion)
			outer.Unlock()

			diff := cmp.Diff(innerLines, outerLines, cmp.AllowUnexported(Style{}))
			if diff != "" {
				t.Fatalf("difference between raw and TTYFrontend: %v", diff)
			}
		})
	}
}
