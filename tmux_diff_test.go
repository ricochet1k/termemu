package termemu

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tmuxDiffSequences are validated terminal sequences for differential testing
// against tmux. They deliberately avoid OSC 8 hyperlinks (capture-pane support
// and encoding differ between tmux versions) and image protocols.
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
			wantScreen, wantCursor := tmuxRender(t, tmuxPath, sock, []byte(tc.seq), width, height)

			term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
			if err := term.Resize(width, height); err != nil {
				t.Fatalf("Resize: %v", err)
			}
			if err := term.testFeedTerminalInputFromBackend([]byte(tc.seq), TextReadModeGrapheme); err != nil && !errors.Is(err, io.EOF) {
				t.Fatalf("feed: %v", err)
			}
			gotCursor := term.CursorPos()
			var lines []string
			term.View(func(v LockedView) {
				for y := 0; y < height; y++ {
					lines = append(lines, v.ANSILine(y))
				}
			})
			gotScreen, _ := tmuxRender(t, tmuxPath, sock, []byte(strings.Join(lines, "\r\n")), width, height)

			if gotScreen != wantScreen {
				reportTmuxScreenDiff(t, wantScreen, gotScreen)
			}
			if gotCursor != wantCursor {
				t.Errorf("cursor = %+v, tmux = %+v", gotCursor, wantCursor)
			}
		})
	}
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
func tmuxRender(t *testing.T, tmuxPath, sock string, data []byte, width, height int) (string, Pos) {
	t.Helper()
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "input")
	donePath := filepath.Join(dir, "done")
	if err := os.WriteFile(dataPath, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	session := fmt.Sprintf("diff-%d", tmuxDiffSessionCounter.Add(1))
	script := fmt.Sprintf("stty -opost 2>/dev/null; cat %s; : > %s; exec sleep 3600",
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

	cursorOut := tmuxCommand(t, tmuxPath, sock, "display-message", "-p", "-t", session, "#{cursor_x} #{cursor_y}")
	var x, y int
	if _, err := fmt.Sscanf(strings.TrimSpace(cursorOut), "%d %d", &x, &y); err != nil {
		t.Fatalf("parse cursor %q: %v", cursorOut, err)
	}
	return prev, Pos{X: x, Y: y}
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

func reportTmuxScreenDiff(t *testing.T, want, got string) {
	t.Helper()
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			t.Errorf("screen line %d differs\n tmux:    %q\n termemu: %q", i, wantLines[i], gotLines[i])
			return
		}
	}
	if len(wantLines) != len(gotLines) {
		t.Errorf("screen line count differs: tmux %d, termemu %d", len(wantLines), len(gotLines))
		return
	}
	t.Errorf("screens differ but no line difference found")
}
