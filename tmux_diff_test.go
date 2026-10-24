package termemu

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// tmuxDiffSequences are validated terminal sequences for differential testing
// against tmux. They deliberately avoid OSC 8 hyperlinks (capture-pane support
// and encoding differ between tmux versions) and image protocols. They also
// avoid DEC character sets (ESC ( 0, SO/SI): tmux does not translate them in
// its grid, it keeps the designation/shift bytes as cells and leaves the
// mapping to the outer terminal, so capture-pane never shows the rendered
// glyphs. Charsets are unit-tested against xterm's dec2ucs table instead.
var tmuxDiffSequences = []struct {
	name string
	seq  string
}{
	{"simple_text", "Hello, World!"},
	{"newlines", "Line 1\nLine 2\nLine 3"},
	{"crlf", "Line 1\r\nLine 2\r\nLine 3"},
	{"tabs", "Col1\tCol2\tCol3"},
	{"tab_at_eol", "Hello\t\nWorld"},
	{"backspace", "Hello\b\b\b\b\bWorld"},
	{"carriage_return", "First\rSecond"},
	{"cursor_movement", "Start\x1b[5DMiddle\x1b[10Cend"},
	{"cursor_up_down", "Line1\x1b[BLine2\x1b[ALine0"},
	{"cursor_to_row_col", "A\x1b[5;10HB"},
	{"home_position", "A\x1b[HB"},
	{"clear_line", "Before\x1b[2KAfter"},
	{"clear_line_left", "Hello\x1b[1KWorld"},
	{"clear_line_right", "Hello\x1b[0KWorld"},
	{"erase_above", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[1JEnd"},
	{"erase_below", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[0JEnd"},
	{"erase_display", "Top\nMiddle\nBottom\x1b[2J\x1b[HCleared"},
	{"colors", "\x1b[31mRed\x1b[0m \x1b[32mGreen\x1b[0m \x1b[34mBlue\x1b[0m"},
	{"bright_colors", "\x1b[90mDark\x1b[91mRed\x1b[92mGreen\x1b[93mYellow\x1b[0m"},
	{"bg_colors_bright", "\x1b[100mBG Dark\x1b[101mBG Red\x1b[102mBG Green\x1b[0m"},
	{"256_colors", "\x1b[38;5;208mOrange\x1b[0m \x1b[48;5;21mBlue BG\x1b[0m"},
	{"mixed_256_colors", "\x1b[48;5;196m\x1b[38;5;255mRed BG White FG\x1b[0m"},
	{"sgr_combined", "\x1b[1;31;44mBold Red on Blue\x1b[0m"},
	{"sgr_colon_rgb", "\x1b[38:2::255:0:0mRed\x1b[0m"},
	{"sgr_colon_rgb_no_cs", "\x1b[38:2:0:255:0mGreen\x1b[0m"},
	{"sgr_colon_256", "\x1b[38:5:208mOrange\x1b[0m"},
	{"sgr_colon_bg", "\x1b[48:2::1:2:3mBG\x1b[0m"},
	{"sgr_colon_mixed", "\x1b[1;38:2::1:2:3;4;48:2::4:5:6mMixed\x1b[0m"},
	{"sgr_underline_color", "\x1b[4;58:2::255:0:0mUnderline\x1b[0m"},
	{"sgr_underline_color_256", "\x1b[4;58;5;21mU\x1b[0m"},
	{"styles", "\x1b[1mBold\x1b[0m \x1b[4mUnderline\x1b[0m \x1b[7mReverse\x1b[0m"},
	{"bold_italic_underline", "\x1b[1;3;4mAll on\x1b[0m"},
	{"strikethrough", "\x1b[9mStrike\x1b[0m"},
	{"double_underline", "\x1b[21mDouble\x1b[0m"},
	{"overline", "\x1b[53mOverline\x1b[0m"},
	{"cursor_save_restore", "A\x1b[sB\x1b[uC"},
	{"nested_save_restore", "A\x1b[sB\x1b[sC\x1b[uD\x1b[uE"},
	{"insert_delete_lines", "one\r\ntwo\r\nthree\x1b[2;1H\x1b[Linserted\x1b[3;1H\x1b[M"},
	{"scroll_region", "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[4;1H\x1b[Sx"},
	{"autowrap_off", "1234567890\x1b[?7lABCDEFGHIJ\x1b[?7h"},
	{"wide_chars", "世界abc\x1b[4D界世"},
	{"emoji_overwrite", "🐹a\n🐹b\x1b[Dz\n🐹c\x1b[D\x1b[Dy\n🐹c\x1b[D\x1b[D\x1b[Dx\n"},
	{"combining_marks", "e\u0301 x\u0301 café"},
	{"cursor_next_previous_line", "abc\x1b[2Edef\x1b[Fghi"},
	{"tab_forward_back", "a\t\t\tX\x1b[2Zb"},
	{"alt_1047_clears_on_exit", "AB\x1b[?1047hX\x1b[?1047l\x1b[?1047h"},
	{"save_restore_1048", "AB\x1b[?1048h\x1b[?1048lX"},
	{"decaln", "abc\x1b#8"},
	{"alt_screen_roundtrip", "main\x1b[?1049halt\x1b[?1049lmain"},
	{"tabs_default", "col0\tcol1\tcol2"},
	{"tabs_hts", "abc\x1bH\r\tX\tY"},
	{"tabs_clear_current", "\tX\x1b[g\rY\tZ"},
	{"ich", "abcdef\x1b[1;3H\x1b[3@X"},
	{"ich_default_param", "abcdef\x1b[1;2H\x1b[@X"},
	{"ich_truncates", "0123456789\x1b[1;8H\x1b[5@X"},
	{"irm", "abcdef\x1b[4h\x1b[1;1HXY\x1b[4lZ"},
	{"irm_full_row", "abcde\x1b[4h\x1b[1;1HXYZW"},
	{"rep", "abc\x1b[3bX"},
	{"rep_default_param", "a\x1b[b"},
	{"rep_zero_param", "a\x1b[0b"},
	{"rep_cleared_by_sgr", "\x1b[31mA\x1b[0m\x1b[2b"},
	{"rep_styled", "\x1b[31mA\x1b[2b\x1b[0m"},
	{"rep_chained", "ab\x1b[2b\x1b[2b"},
	{"rep_wide_char", "界\x1b[2b"},
	{"rep_cleared_by_cursor_move", "abc\x1b[1;1H\x1b[2b"},
	{"rep_cleared_by_control", "A\r\x1b[2b"},
	{"origin_mode_cup", "\x1b[5;20r\x1b[?6h\x1b[1;1HA\x1b[3;1HB\x1b[99;1HC"},
	{"origin_mode_cup_reset", "\x1b[5;20r\x1b[?6h\x1b[3;1HB\x1b[?6lC"},
	{"origin_mode_vpa", "\x1b[5;20r\x1b[?6h\x1b[2dA\x1b[99dB"},
	{"origin_mode_movement", "\x1b[5;20r\x1b[?6h\x1b[1;1H\x1b[9AA\x1b[99;1H\x1b[9BB"},
	{"origin_mode_scroll", "\x1b[5;20r\x1b[?6h\x1b[1;1H\x1b[99;1H\nX"},
	{"origin_mode_decsc", "\x1b[5;20r\x1b[?6h\x1b[2;1H\x1b7\x1b[?6l\x1b8X"},
}

// tmuxDiffRawSequences are rendered with OPOST disabled in the pane, so bare
// LF/VT/FF reach the terminal instead of the kernel's CRLF translation. They
// cover line-feed semantics that the normal corpus cannot: a line feed moves
// down without returning to column 0 (the "staircase" effect visible with
// `stty -onlcr`).
var tmuxDiffRawSequences = []struct {
	name string
	seq  string
}{
	{"raw_lf_keeps_column", "abc\ndef"},
	{"raw_lf_multiple", "a\nb\nc\nd"},
	{"raw_lf_scrolls_at_bottom", "one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix"},
	{"raw_vt_ff_keep_column", "ab\vcd\fef"},
	{"raw_cr_same_row", "abc\rX"},
	{"raw_crlf", "abc\r\ndef"},
	{"raw_lf_then_cr", "abc\n\rdef"},
	{"raw_lf_at_right_margin", "123456789012345678901234567890\ndef"},
	{"raw_tab_then_lf", "ab\tcd\nef"},
}

var tmuxDiffSessionCounter atomic.Uint64

// TestDifferentialTmux compares termemu's screen and cursor against a real
// tmux pane. It starts a private tmux server, so it does not need to run
// inside tmux. Opt in with:
//
//	TERMEMU_TMUX_DIFF=1 go test -run '^TestDifferentialTmux$' -v .
func TestDifferentialTmux(t *testing.T) {
	if os.Getenv("TERMEMU_TMUX_DIFF") == "" {
		t.Skip("set TERMEMU_TMUX_DIFF=1 to run the tmux differential test")
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	sock := startTmuxDiffServer(t, tmuxPath)

	const width, height = 80, 24
	for _, tc := range tmuxDiffSequences {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runDifferentialCase(t, tmuxPath, sock, tc.seq, width, height, true, false)
		})
	}
	for _, tc := range tmuxDiffRawSequences {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runDifferentialCase(t, tmuxPath, sock, tc.seq, width, height, true, true)
		})
	}
}

// TestDifferentialTmuxRandom generates deterministic random sequences from
// the same feature set as the curated corpus. Run it with the same
// TERMEMU_TMUX_DIFF=1 opt-in.
func TestDifferentialTmuxRandom(t *testing.T) {
	if os.Getenv("TERMEMU_TMUX_DIFF") == "" {
		t.Skip("set TERMEMU_TMUX_DIFF=1 to run the tmux differential test")
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	sock := startTmuxDiffServer(t, tmuxPath)

	const width, height = 80, 24
	for seed := int64(1); seed <= 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		seq := randomTerminalSequence(rng, 8+rng.Intn(18))
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferentialCase(t, tmuxPath, sock, seq, width, height, false, false)
		})
	}
}

func runDifferentialCase(t *testing.T, tmuxPath, sock, seq string, width, height int, strictStyle, raw bool) {
	t.Helper()
	t.Logf("sequence: %q", seq)
	want := tmuxRender(t, tmuxPath, sock, []byte(seq), width, height, raw)

	// The pane runs `cat` on a tty with output post-processing enabled, so the
	// kernel gives tmux CRLF for every LF. Feed termemu the same wire bytes;
	// the raw corpus disables OPOST and keeps bare LF.
	wire := seq
	if !raw {
		wire = strings.ReplaceAll(seq, "\n", "\r\n")
	}

	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	if err := term.Resize(width, height); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	// The terminal starts at its default size, so the resize above pushes
	// blank rows into scrollback; that is a setup artifact, not output.
	term.Scrollback().Clear()
	if err := term.testFeedTerminalInputFromBackend([]byte(wire), TextReadModeGrapheme); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("feed: %v", err)
	}
	gotCursor := term.CursorPos()
	gotVisible := term.viewFlags[VFShowCursor]
	gotAlt := term.onAltScreen
	var lines []string
	term.View(func(v LockedView) {
		for y := 0; y < height; y++ {
			lines = append(lines, v.ANSILine(y))
		}
	})
	// The rendered screen is fed back through tmux with the normal (CRLF)
	// terminal settings: termemu's line renderer emits whole rows, not the
	// raw byte stream.
	gotRender := tmuxRender(t, tmuxPath, sock, []byte(strings.Join(lines, "\n")), width, height, false)
	wantScreen := normalizeTmuxCapture(want.screen)
	gotScreen := normalizeTmuxCapture(gotRender.screen)

	if strictStyle {
		if gotScreen != wantScreen {
			reportTmuxScreenDiff(t, wantScreen, gotScreen)
		}
	} else {
		// Random sequences often leave attributes on blank cells, which
		// capture-pane represents differently from termemu's ANSILine output.
		// The curated corpus asserts styles; randomized cases compare the
		// visible text and cursor.
		wantText, gotText := plainCapture(wantScreen), plainCapture(gotScreen)
		if gotText != wantText {
			reportTmuxScreenDiff(t, wantText, gotText)
		}
	}
	if gotCursor != want.cursor {
		t.Errorf("cursor = %+v, tmux = %+v", gotCursor, want.cursor)
	}
	if gotVisible != want.cursorVisible {
		t.Errorf("cursor visible = %v, tmux = %v", gotVisible, want.cursorVisible)
	}
	if gotAlt != want.altActive {
		t.Errorf("alt screen = %v, tmux = %v", gotAlt, want.altActive)
	}
}

var tmuxRandomTokens = []string{
	"abc", "Hello", "世界", "🐹", "e\u0301", "1\ufe0f\u20e3", "👍🏽",
	"\r", "\n", "\t", "\b",
	"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[3;7H", "\x1b[G", "\x1b[3d",
	"\x1b[K", "\x1b[1K", "\x1b[2K", "\x1b[J", "\x1b[1J", "\x1b[2J",
	"\x1b[E", "\x1b[F", "\x1b[2E", "\x1b[2F", "\x1b[Z",
	"\x1b[L", "\x1b[M", "\x1b[S", "\x1b[T", "\x1b[P", "\x1b[X",
	"\x1b[31m", "\x1b[1m", "\x1b[4m", "\x1b[7m", "\x1b[38;5;123m", "\x1b[48;2;1;2;3m", "\x1b[0m",
	"\x1b[?7l", "\x1b[?7h", "\x1b[?25l", "\x1b[?25h",
	"\x1b[s", "\x1b[u", "\x1b7", "\x1b8",
	"\x1b[2;4r", "\x1b[r", "\x1b[?1049h", "\x1b[?1049l",
}

func randomTerminalSequence(rng *rand.Rand, tokens int) string {
	var b strings.Builder
	for i := 0; i < tokens; i++ {
		b.WriteString(tmuxRandomTokens[rng.Intn(len(tmuxRandomTokens))])
	}
	return b.String()
}

func startTmuxDiffServer(t *testing.T, tmuxPath string) string {
	t.Helper()
	sock := fmt.Sprintf("termemu-diff-%d-%d", os.Getpid(), time.Now().UnixNano())
	tmuxCommand(t, tmuxPath, sock, "-f", "/dev/null",
		"new-session", "-d", "-s", "keepalive", "-x", "80", "-y", "24",
		"sh -c "+shellQuote("exec sleep 3600"))
	tmuxCommand(t, tmuxPath, sock, "set-option", "-g", "status", "off")
	// tmux's default scroll-on-clear pushes erased screen contents into
	// history; xterm (and termemu) discard them, so turn the option off to
	// compare scrollback after CSI 2 J.
	tmuxCommand(t, tmuxPath, sock, "set-option", "-g", "scroll-on-clear", "off")
	tmuxCommand(t, tmuxPath, sock, "set-option", "-g", "window-size", "manual")
	t.Cleanup(func() {
		cmd := exec.Command(tmuxPath, "-L", sock, "kill-server")
		_ = cmd.Run()
	})
	return sock
}

// tmuxRender writes data to a fresh pane and returns the canonical captured
// screen (with SGR) plus the final cursor position.
type tmuxRenderResult struct {
	screen        string
	cursor        Pos
	cursorVisible bool
	altActive     bool
}

func tmuxRender(t *testing.T, tmuxPath, sock string, data []byte, width, height int, raw bool) tmuxRenderResult {
	t.Helper()
	return tmuxRenderPane(t, tmuxPath, sock, data, width, height, raw, 0)
}

// tmuxRenderPane is tmuxRender with an optional history capture: historyRows >
// 0 also captures that many rows of scrollback above the visible screen.
func tmuxRenderPane(t *testing.T, tmuxPath, sock string, data []byte, width, height int, raw bool, historyRows int) tmuxRenderResult {
	t.Helper()
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "input")
	donePath := filepath.Join(dir, "done")
	if err := os.WriteFile(dataPath, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	session := fmt.Sprintf("diff-%d", tmuxDiffSessionCounter.Add(1))
	prefix := ""
	if raw {
		// Disable output post-processing so bare LF/VT/FF reach the pane.
		prefix = "stty -opost; "
	}
	script := fmt.Sprintf("%scat %s; : > %s; exec sleep 3600",
		prefix, shellQuote(dataPath), shellQuote(donePath))
	tmuxCommand(t, tmuxPath, sock, "new-session", "-d", "-s", session,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height), "sh -c "+shellQuote(script))
	defer func() {
		cmd := exec.Command(tmuxPath, "-L", sock, "kill-session", "-t", session)
		_ = cmd.Run()
	}()
	tmuxCommand(t, tmuxPath, sock, "resize-window", "-t", session, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(donePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for tmux render")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Wait for tmux to settle on the final frame; capture-pane can race the
	// pty bytes that `cat` just wrote.
	time.Sleep(50 * time.Millisecond)
	captureArgs := []string{"capture-pane", "-p", "-e", "-t", session}
	if historyRows > 0 {
		captureArgs = []string{"capture-pane", "-p", "-e", "-S", strconv.Itoa(-historyRows), "-t", session}
	}
	prev := tmuxCommand(t, tmuxPath, sock, captureArgs...)
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		cur := tmuxCommand(t, tmuxPath, sock, captureArgs...)
		if cur == prev {
			break
		}
		prev = cur
	}

	stateOut := tmuxCommand(t, tmuxPath, sock, "display-message", "-p", "-t", session,
		"#{cursor_x} #{cursor_y} #{cursor_flag} #{alternate_on}")
	var x, y, visible, alt int
	if _, err := fmt.Sscanf(strings.TrimSpace(stateOut), "%d %d %d %d", &x, &y, &visible, &alt); err != nil {
		t.Fatalf("parse pane state %q: %v", stateOut, err)
	}
	return tmuxRenderResult{
		screen:        prev,
		cursor:        Pos{X: x, Y: y},
		cursorVisible: visible != 0,
		altActive:     alt != 0,
	}
}

// tmuxScrollbackSequences exercise the scrollback path: enough output to push
// rows off the top of a short screen, with wrapping, wide characters, tabs,
// erases, and blank lines mixed in.
var tmuxScrollbackSequences = []struct {
	name   string
	seq    string
	width  int
	height int
}{
	{"history_plain", numberedLines(1, 40), 40, 6},
	{"history_exact_fit", numberedLines(1, 6), 40, 6},
	{"history_one_over", numberedLines(1, 7), 40, 6},
	{"history_blank_lines", "one\n\n\n\nfour\nfive\nsix\nseven\neight", 40, 6},
	{"history_wrapped", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJ\nshort\n" + numberedLines(1, 12), 20, 5},
	{"history_wide", "界界界界界界界界界界\n" + numberedLines(1, 8) + "界界世\n", 10, 4},
	// Tabs are skipped: capture-pane reports the tab character itself rather
	// than the expanded columns, which the screen comparison cannot model.
	{"history_erase_screen", numberedLines(1, 9) + "\x1b[2Jafter\n" + numberedLines(20, 24), 40, 6},
	{"history_erase_line", numberedLines(1, 8) + "\x1b[2Kcleared\n", 30, 5},
	// Mid-screen scroll regions are skipped: tmux's capture-pane interleaves
	// region scrolls into history in a way that does not match its own visible
	// screen. termemu recycles region rows in place, like xterm.
	{"history_alt_roundtrip", numberedLines(1, 10) + "\x1b[?1049h" + numberedLines(50, 60) + "\x1b[?1049l" + numberedLines(11, 12), 40, 5},
	{"history_long_128", numberedLines(1, 128), 80, 24},
}

// numberedLines returns n CRLF-terminated lines labelled from start.
func numberedLines(start, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line%05d\n", start+i)
	}
	return b.String()
}

// TestDifferentialTmuxScrollback compares the scrollback history (not just the
// visible screen) against tmux. Like the other differential tests it is opt-in:
//
//	TERMEMU_TMUX_DIFF=1 go test -run '^TestDifferentialTmuxScrollback$' -v .
func TestDifferentialTmuxScrollback(t *testing.T) {
	if os.Getenv("TERMEMU_TMUX_DIFF") == "" {
		t.Skip("set TERMEMU_TMUX_DIFF=1 to run the tmux differential test")
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	sock := startTmuxDiffServer(t, tmuxPath)

	for _, tc := range tmuxScrollbackSequences {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runScrollbackDifferentialCase(t, tmuxPath, sock, tc.seq, tc.width, tc.height)
		})
	}
}

func runScrollbackDifferentialCase(t *testing.T, tmuxPath, sock, seq string, width, height int) {
	t.Helper()
	t.Logf("sequence: width=%d height=%d %q", width, height, seq)

	const historyRows = 500
	want := tmuxRenderPane(t, tmuxPath, sock, []byte(seq), width, height, false, historyRows)
	wantRows := splitCaptureRows(want.screen)

	// The pane tty translates LF to CRLF before tmux sees it; feed termemu the
	// same wire bytes.
	wire := strings.ReplaceAll(seq, "\n", "\r\n")
	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	if err := term.Resize(width, height); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	// The terminal starts at its default size, so the resize above pushes
	// blank rows into scrollback; that is a setup artifact, not output.
	term.Scrollback().Clear()
	if err := term.testFeedTerminalInputFromBackend([]byte(wire), TextReadModeGrapheme); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("feed: %v", err)
	}

	var gotRows []string
	sb := term.Scrollback()
	base, next := sb.Range()
	for id := base; id < next; id++ {
		row, ok := sb.Row(id)
		if !ok {
			t.Fatalf("scrollback row %d missing", id)
		}
		gotRows = append(gotRows, row.PlainTextString())
	}
	for y := 0; y < height; y++ {
		gotRows = append(gotRows, term.Line(y))
	}
	for i := range gotRows {
		gotRows[i] = strings.TrimRight(gotRows[i], " ")
	}

	wantRows = trimScrollbackRows(wantRows)
	gotRows = trimScrollbackRows(gotRows)

	if len(gotRows) != len(wantRows) {
		t.Fatalf("row count: termemu %d (history+screen), tmux %d\ntermemu: %q\ntmux:    %q",
			len(gotRows), len(wantRows), gotRows, wantRows)
	}
	for i := range wantRows {
		if gotRows[i] != wantRows[i] {
			t.Fatalf("row %d differs\ntermemu: %q\ntmux:    %q", i, gotRows[i], wantRows[i])
		}
	}
}

// splitCaptureRows splits a capture-pane result into rows with trailing blanks
// removed.
func splitCaptureRows(capture string) []string {
	rows := strings.Split(capture, "\n")
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return rows
}

// trimScrollbackRows drops trailing blank rows and the leading blank rows that
// tmux and termemu disagree about (the pane starts with a cleared screen, and
// capture-pane reports fewer leading history rows than were actually cleared).
func trimScrollbackRows(rows []string) []string {
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	for len(rows) > 0 && rows[0] == "" {
		rows = rows[1:]
	}
	return rows
}

func tmuxCommand(t *testing.T, tmuxPath, sock string, args ...string) string {
	t.Helper()
	args = append([]string{"-L", sock}, args...)
	cmd := exec.Command(tmuxPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// normalizeTmuxCapture expands the tab cells that capture-pane preserves for
// literal tabs, then drops trailing blanks the same way capture-pane would.
// ANSI SGR sequences are copied without advancing the column.
func normalizeTmuxCapture(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		var b strings.Builder
		col := 0
		for j := 0; j < len(line); {
			if line[j] == 0x1b {
				start := j
				j++
				if j < len(line) && line[j] == '[' {
					j++
					for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
						j++
					}
					if j < len(line) {
						j++
					}
				} else if j < len(line) {
					j++
				}
				b.WriteString(line[start:j])
				continue
			}
			if line[j] == '\t' {
				n := 8 - col%8
				b.WriteString(strings.Repeat(" ", n))
				col += n
				j++
				continue
			}
			r, size := utf8.DecodeRuneInString(line[j:])
			b.WriteString(line[j : j+size])
			col += cellWidth(r)
			j += size
		}
		line = strings.TrimRight(b.String(), " ")
		// capture-pane omits attributes on entirely blank lines; termemu's
		// ANSILine can emit SGR for a blank line. Compare visible content and
		// ignore style-only blank lines.
		if strings.TrimSpace(stripANSI(line)) == "" {
			line = ""
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// plainCapture drops SGR and trailing blanks per line, leaving the visible
// text used by randomized comparisons.
func plainCapture(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(stripANSI(line), " ")
	}
	return strings.Join(lines, "\n")
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i < len(s) && s[i] == '[' {
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < len(s) {
				i++
			}
		} else if i < len(s) {
			i++
		}
	}
	return b.String()
}

func reportTmuxScreenDiff(t *testing.T, want, got string) {
	t.Helper()
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	diffs := 0
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			t.Errorf("screen line %d differs\n tmux:    %q\n termemu: %q", i, wantLines[i], gotLines[i])
			diffs++
			if diffs >= 8 {
				return
			}
		}
	}
	if len(wantLines) != len(gotLines) {
		t.Errorf("screen line count differs: tmux %d, termemu %d", len(wantLines), len(gotLines))
		return
	}
	if diffs == 0 {
		t.Errorf("screens differ but no line difference found")
	}
}
