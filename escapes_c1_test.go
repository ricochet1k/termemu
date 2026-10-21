package termemu

import (
	"strings"
	"testing"
)

func feedC1(t *testing.T, t1 *terminal, mode TextReadMode, data string) {
	t.Helper()
	if err := t1.testFeedTerminalInputFromBackend([]byte(data), mode); err != nil {
		t.Fatalf("feed %q: %v", data, err)
	}
}

func TestC1_StandaloneBytesDoNotCreateCells(t *testing.T) {
	for _, mode := range []TextReadMode{TextReadModeRune, TextReadModeGrapheme} {
		t.Run("mode", func(t *testing.T) {
			for _, raw := range []string{"\x80", "\x9b", "\x9c"} {
				_, t1, _ := MakeTerminalWithMock(mode)
				feedC1(t, t1, mode, raw)
				if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
					t.Fatalf("raw C1 %#x created cells: %q", raw[0], got)
				}
			}

			// Raw C1 bytes interleaved with text leave the valid text contiguous.
			_, t1, _ := MakeTerminalWithMock(mode)
			feedC1(t, t1, mode, "a\x80b\x9bc\x9cd")
			if got := strings.TrimSpace(lineBlank(t, t1, 0)); got != "abcd" {
				t.Fatalf("mixed C1/text line = %q, want %q", got, "abcd")
			}
		})
	}
}

func TestC1_ValidUTF8StillDecodes(t *testing.T) {
	for _, mode := range []TextReadMode{TextReadModeRune, TextReadModeGrapheme} {
		t.Run("mode", func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(mode)
			feedC1(t, t1, mode, "A\xc3\x9cB")
			if got := strings.TrimSpace(lineBlank(t, t1, 0)); got != "AÜB" {
				t.Fatalf("line = %q, want %q", got, "AÜB")
			}

			_, t2, _ := MakeTerminalWithMock(mode)
			feedC1(t, t2, mode, "caf\xc3\xa9")
			if got := strings.TrimSpace(lineBlank(t, t2, 0)); got != "café" {
				t.Fatalf("line = %q, want %q", got, "café")
			}

			// U+0080 (C2 80) is a valid UTF-8 encoding of a C1 control and
			// must not be displayed.
			_, t3, _ := MakeTerminalWithMock(mode)
			feedC1(t, t3, mode, "X\xc2\x80Y")
			if got := strings.TrimSpace(lineBlank(t, t3, 0)); got != "XY" {
				t.Fatalf("line = %q, want %q", got, "XY")
			}
		})
	}
}
