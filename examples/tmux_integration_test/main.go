package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ricochet1k/termemu"
)

// Test sequences that exercise various terminal features
var testSequences = []struct {
	name     string
	sequence string
}{
	// Original tests (basic functionality)
	{"simple_text", "Hello, World!"},
	{"newlines", "Line 1\nLine 2\nLine 3"},
	{"colors", "\x1b[31mRed\x1b[0m \x1b[32mGreen\x1b[0m \x1b[34mBlue\x1b[0m"},
	{"bold_underline", "\x1b[1mBold\x1b[0m \x1b[4mUnderline\x1b[0m"},
	{"cursor_movement", "Start\x1b[5DMiddle\x1b[10Cend"},
	{"clear_line", "Before\x1b[2KAfter"},
	{"home_position", "A\x1b[HB"},
	{"tabs", "Col1\tCol2\tCol3"},
	{"backspace", "Hello\b\b\b\b\bWorld"},
	{"carriage_return", "First\rSecond"},
	{"sgr_combined", "\x1b[1;31;44mBold Red on Blue\x1b[0m"},
	{"erase_display", "Top\nMiddle\nBottom\x1b[2J\x1b[HCleared"},
	{"cursor_save_restore", "A\x1b[sB\x1b[uC"},
	{"reverse_video", "Normal \x1b[7mReverse\x1b[27m Normal"},
	{"256_colors", "\x1b[38;5;208mOrange\x1b[0m \x1b[48;5;21mBlue BG\x1b[0m"},

	// Edge cases and additional escape sequences
	{"empty_input", ""},
	{"multiple_tabs", "A\t\t\tB"},
	{"tab_at_eol", "Hello\t\nWorld"},
	{"multiple_backspaces", "ABCDEF\b\b\b123"},
	{"multiple_cr", "AAA\rBBB\rCCC"},
	{"nested_save_restore", "A\x1b[sB\x1b[sC\x1b[uD\x1b[uE"},
	{"cursor_down", "Line1\x1b[BLine2"},
	{"cursor_up", "Line1\x1b[ALine0"},
	{"cursor_right", "A\x1b[CB"},
	{"cursor_left", "AB\x1b[DC"},
	{"bold_italic_underline", "\x1b[1;3;4mAll on\x1b[0m"},
	{"dim_mode", "\x1b[2mDim\x1b[0m"},
	{"italic_mode", "\x1b[3mItalic\x1b[0m"},
	{"blink_mode", "\x1b[5mBlink\x1b[0m"},
	{"all_colors_basic", "\x1b[30m0\x1b[31m1\x1b[32m2\x1b[33m3\x1b[34m4\x1b[35m5\x1b[36m6\x1b[37m7\x1b[0m"},
	{"bright_colors", "\x1b[90mDark\x1b[91mRed\x1b[92mGreen\x1b[93mYellow\x1b[0m"},
	{"bg_colors_bright", "\x1b[100mBG Dark\x1b[101mBG Red\x1b[102mBG Green\x1b[0m"},
	{"256_color_grayscale", "\x1b[38;5;232mBlack\x1b[0m \x1b[38;5;255mWhite\x1b[0m"},
	{"mixed_256_colors", "\x1b[48;5;196m\x1b[38;5;255mRed BG White FG\x1b[0m"},
	{"clear_line_left", "Hello\x1b[1KWorld"},
	{"clear_line_right", "Hello\x1b[0KWorld"},
	{"erase_above", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[1JEnd"},
	{"erase_below", "Top\nMiddle\nBottom\nab\x1b[A\x1b[A\x1b[0JEnd"},
	{"enable_cursor", "Hidden\x1b[?25hVisible"},
	{"cursor_to_row_col", "A\x1b[5;10HB"},
	{"multiple_newlines", "Line1\n\n\nLine5"},
	{"mixed_colors_and_moves", "\x1b[31mRed\x1b[3CText\x1b[0m"},
	{"reset_in_middle", "\x1b[1;31mBold Red\x1b[0mNormal\x1b[1mBold"},
	{"consecutive_tabs", "\tA\tB\tC\t"},
	{"space_and_tab", "A B\tC D"},
	{"sgr_0_special_case", "\x1b[0mNormal"},
	{"sgr_reset_all", "\x1b[1;31;44mRGB\x1b[0;0mReset"},
	{"thick_text_modes", "\x1b[1;2;4;7;5mMultiple\x1b[0m"},
	{"strikethrough", "\x1b[9mStrike\x1b[0m"},
	{"double_underline", "\x1b[21mDouble\x1b[0m"},
	{"overline", "\x1b[53mOverline\x1b[0m"},
	{"emoji_overwrite", "🐹a\n🐹b\x1b[Dz\n🐹c\x1b[D\x1b[Dy\n🐹c\x1b[D\x1b[D\x1b[Dx\n"},
}

type TestResult struct {
	name         string
	passed       bool
	termemuMatch bool
	ttyMatch     bool
	tmuxOut      string
	termemuOut   string
	ttyOut       string
	err          error
}

func main() {
	width := flag.Int("width", 80, "terminal width")
	height := flag.Int("height", 24, "terminal height")
	verbose := flag.Bool("verbose", false, "show all outputs")
	single := flag.String("test", "", "run only the named test")
	flag.Parse()

	if !isInsideTmux() {
		fmt.Fprintln(os.Stderr, "Error: This test must be run inside a tmux session")
		fmt.Fprintln(os.Stderr, "Start tmux with: tmux new-session")
		os.Exit(1)
	}

	// Collect all results
	var results []TestResult

	for _, test := range testSequences {
		if *single != "" && test.name != *single {
			continue
		}

		result := TestResult{name: test.name}

		// 1. Capture raw through tmux
		tmuxOut, err := captureRawThroughTmux(test.sequence, *width, *height)
		if err != nil {
			result.err = fmt.Errorf("tmux capture: %w", err)
			results = append(results, result)
			continue
		}
		result.tmuxOut = tmuxOut

		// 2. Capture termemu through tmux
		termemuOut, err := captureTermemuThroughTmux(test.sequence, *width, *height)
		if err != nil {
			result.err = fmt.Errorf("termemu capture: %w", err)
			results = append(results, result)
			continue
		}
		result.termemuOut = termemuOut

		// 3. Capture TTY through tmux
		ttyOut, err := captureTTYThroughTmux(test.sequence, *width, *height)
		if err != nil {
			result.err = fmt.Errorf("TTY capture: %w", err)
			results = append(results, result)
			continue
		}
		result.ttyOut = ttyOut

		// Compare
		result.termemuMatch = (tmuxOut == termemuOut)
		result.ttyMatch = (tmuxOut == ttyOut)
		result.passed = result.termemuMatch && result.ttyMatch

		results = append(results, result)
	}

	// Print results on main screen
	printResults(results, *verbose)

	// Exit with error if any failed
	for _, r := range results {
		if !r.passed || r.err != nil {
			os.Exit(1)
		}
	}
}

func printResults(results []TestResult, verbose bool) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Integration Test Results")
	fmt.Fprintln(os.Stderr, "========================")
	fmt.Fprintln(os.Stderr)

	passed := 0
	failed := 0

	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: ERROR: %v\n", r.name, r.err)
			failed++
			continue
		}

		if r.passed {
			fmt.Fprintf(os.Stderr, "✓ %s\n", r.name)
			passed++
			if verbose {
				fmt.Fprintf(os.Stderr, "  Output:\n%s\n", indent(r.tmuxOut))
			}
		} else {
			fmt.Fprintf(os.Stderr, "❌ %s\n", r.name)
			failed++
			if !r.termemuMatch {
				fmt.Fprintln(os.Stderr, "  - termemu output differs from tmux")
				if verbose {
					fmt.Fprintf(os.Stderr, "    Tmux:\n%s\n", indent(r.tmuxOut))
					fmt.Fprintf(os.Stderr, "    Termemu:\n%s\n", indent(r.termemuOut))
				}
				fmt.Fprintln(os.Stderr, "    Diff (quoted):")
				printQuotedDiff(os.Stderr, r.tmuxOut, r.termemuOut)
			}
			if !r.ttyMatch {
				fmt.Fprintln(os.Stderr, "  - TTY output differs from tmux")
				if verbose {
					fmt.Fprintf(os.Stderr, "    Tmux:\n%s\n", indent(r.tmuxOut))
					fmt.Fprintf(os.Stderr, "    TTY:\n%s\n", indent(r.ttyOut))
				}
				fmt.Fprintln(os.Stderr, "    Diff (quoted):")
				printQuotedDiff(os.Stderr, r.tmuxOut, r.ttyOut)
			}
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Results: %d passed, %d failed\n", passed, failed)
}

func isInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// captureRawThroughTmux writes raw escape sequences directly to stdout on alt screen, captures
func captureRawThroughTmux(seq string, width, height int) (string, error) {
	// Switch to alternate screen
	fmt.Print("\x1b[?1049h")
	// Clear screen and move to home
	fmt.Print("\x1b[2J\x1b[H")

	// Resize if needed (tmux should handle this)
	cmd := exec.Command("tmux", "resize-pane", "-x", fmt.Sprint(width), "-y", fmt.Sprint(height))
	_ = cmd.Run() // Ignore error, might already be correct size

	// Write the sequence directly to stdout
	fmt.Print(seq)

	// Flush and wait a bit
	time.Sleep(100 * time.Millisecond)

	// Capture
	out, err := captureTmuxPane()

	// Switch back to main screen
	fmt.Print("\x1b[?1049l")

	return out, err
}

// captureTermemuThroughTmux processes through termemu, outputs to alt screen, captures
func captureTermemuThroughTmux(seq string, width, height int) (string, error) {
	// Create a pipe to simulate backend
	r, w, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create pipe: %w", err)
	}
	defer r.Close()

	backend := termemu.NewNoPTYBackend(r, w)
	frontend := &termemu.EmptyFrontend{}
	term := termemu.New(frontend, backend)
	if term == nil {
		return "", fmt.Errorf("failed to create terminal")
	}

	if err := term.Resize(width, height); err != nil {
		return "", fmt.Errorf("resize: %w", err)
	}

	// Write the sequence
	if _, err := w.Write([]byte(seq)); err != nil {
		return "", fmt.Errorf("write sequence: %w", err)
	}
	w.Close()

	// Give time for processing
	time.Sleep(50 * time.Millisecond)

	// Get ANSI output line by line
	var lines []string
	term.Lock()
	for y := 0; y < height; y++ {
		line := term.ANSILine(y)
		lines = append(lines, line)
	}
	term.Unlock()
	ansiOutput := strings.Join(lines, "\n")

	// Switch to alternate screen
	fmt.Print("\x1b[?1049h")
	// Clear screen and move to home
	fmt.Print("\x1b[2J\x1b[H")

	// Resize if needed
	cmd := exec.Command("tmux", "resize-pane", "-x", fmt.Sprint(width), "-y", fmt.Sprint(height))
	_ = cmd.Run()

	// Write the ANSI output
	fmt.Print(ansiOutput)

	// Flush and wait
	time.Sleep(100 * time.Millisecond)

	// Capture
	out, capErr := captureTmuxPane()

	// Switch back to main screen
	fmt.Print("\x1b[?1049l")

	return out, capErr
}

// captureTTYThroughTmux processes through TTY frontend, outputs to alt screen, captures
func captureTTYThroughTmux(seq string, width, height int) (string, error) {
	// Create a pipe to simulate backend
	r, w, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create pipe: %w", err)
	}
	defer r.Close()

	// Create a buffer to capture TTY output
	var ttyBuf bytes.Buffer

	backend := termemu.NewNoPTYBackend(r, w)
	ttyFrontend := termemu.NewTTYFrontend(nil, &ttyBuf)
	term := termemu.New(ttyFrontend, backend)
	if term == nil {
		return "", fmt.Errorf("failed to create terminal")
	}
	ttyFrontend.SetTerminal(term)

	if err := term.Resize(width, height); err != nil {
		return "", fmt.Errorf("resize: %w", err)
	}

	// Attach the TTY frontend
	ttyFrontend.Attach(termemu.Region{X: 0, Y: 0, X2: width, Y2: height})

	// Write the sequence
	if _, err := w.Write([]byte(seq)); err != nil {
		return "", fmt.Errorf("write sequence: %w", err)
	}
	w.Close()

	// Give time for processing
	time.Sleep(50 * time.Millisecond)

	ttyFrontend.Detach()

	// Get the TTY output
	ttyOutput := ttyBuf.String()

	// Switch to alternate screen
	fmt.Print("\x1b[?1049h")
	// Clear screen and move to home
	fmt.Print("\x1b[2J\x1b[H")

	// Resize if needed
	cmd := exec.Command("tmux", "resize-pane", "-x", fmt.Sprint(width), "-y", fmt.Sprint(height))
	_ = cmd.Run()

	// Write the TTY output
	fmt.Print(ttyOutput)

	// Flush and wait
	time.Sleep(100 * time.Millisecond)

	// Capture
	out, capErr := captureTmuxPane()

	// Switch back to main screen
	fmt.Print("\x1b[?1049l")

	return out, capErr
}

// captureTmuxPane captures the current tmux pane with escape sequences
func captureTmuxPane() (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-p", "-e")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capture pane: %w", err)
	}
	return string(out), nil
}

// indent adds "  " to the start of each line
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// printQuotedDiff prints a line-by-line diff of two strings in quoted format
func printQuotedDiff(w *os.File, expected, actual string) {
	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")

	maxLines := len(expectedLines)
	if len(actualLines) > maxLines {
		maxLines = len(actualLines)
	}

	for i := 0; i < maxLines; i++ {
		var expLine, actLine string
		if i < len(expectedLines) {
			expLine = expectedLines[i]
		}
		if i < len(actualLines) {
			actLine = actualLines[i]
		}

		if expLine != actLine {
			fmt.Fprintf(w, "      Line %d:\n", i+1)
			fmt.Fprintf(w, "        Expected: %q\n", expLine)
			fmt.Fprintf(w, "        Actual:   %q\n", actLine)
		}
	}
}
