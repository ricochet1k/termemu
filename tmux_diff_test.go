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
			runDifferentialCase(t, tmuxPath, sock, tc.seq, width, height, true)
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
			runDifferentialCase(t, tmuxPath, sock, seq, width, height, false)
		})
	}
}

func runDifferentialCase(t *testing.T, tmuxPath, sock, seq string, width, height int, strictStyle bool) {
	t.Helper()
	t.Logf("sequence: %q", seq)
	want := tmuxRender(t, tmuxPath, sock, []byte(seq), width, height)

	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	if err := term.Resize(width, height); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := term.testFeedTerminalInputFromBackend([]byte(seq), TextReadModeGrapheme); err != nil && !errors.Is(err, io.EOF) {
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
	gotRender := tmuxRender(t, tmuxPath, sock, []byte(strings.Join(lines, "\n")), width, height)
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

func tmuxRender(t *testing.T, tmuxPath, sock string, data []byte, width, height int) tmuxRenderResult {
	t.Helper()
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "input")
	donePath := filepath.Join(dir, "done")
	if err := os.WriteFile(dataPath, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	session := fmt.Sprintf("diff-%d", tmuxDiffSessionCounter.Add(1))
	script := fmt.Sprintf("cat %s; : > %s; exec sleep 3600",
		shellQuote(dataPath), shellQuote(donePath))
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
	prev := tmuxCommand(t, tmuxPath, sock, "capture-pane", "-p", "-e", "-t", session)
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		cur := tmuxCommand(t, tmuxPath, sock, "capture-pane", "-p", "-e", "-t", session)
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
