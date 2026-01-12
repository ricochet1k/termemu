package termemu

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTTYFrontendNestedTerminalSpacing(t *testing.T) {
	var buf bytes.Buffer

	outerFrontend := NewTTYFrontend(nil, &buf)
	outerBackend := NewNoPTYBackend(bytes.NewReader(nil), io.Discard)
	outer := NewWithMode(outerFrontend, outerBackend, TextReadModeRune).(*terminal)
	outerFrontend.SetTerminal(outer)
	outer.Resize(40, 2)
	outerFrontend.Attach(Region{X: 0, Y: 0, X2: 40, Y2: 2})

	outer.WithLock(func() {
		outer.screen().writeRunes([]rune("🐹 v1.24.3 took"))
	})

	innerBackend := NewNoPTYBackend(bytes.NewReader(nil), io.Discard)
	inner := NewWithMode(&EmptyFrontend{}, innerBackend, TextReadModeRune).(*terminal)
	inner.Resize(40, 2)
	if err := feedTTYOutput(inner, buf.Bytes()); err != nil {
		t.Fatalf("feedTTYOutput: %v", err)
	}

	got := stripNul(inner.Line(0))
	if !strings.Contains(got, "v1.24.3 took") {
		t.Fatalf("expected space to be preserved, got %q", got)
	}
}

func feedTTYOutput(t *terminal, data []byte) error {
	gr := NewGraphemeReaderWithMode(bytes.NewReader(data), TextReadModeRune)
	for {
		tokens, err := gr.ReadPrintableTokens()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(tokens) > 0 {
			runes := make([]rune, 0, len(tokens))
			for _, tok := range tokens {
				runes = append(runes, []rune(tok.Text)...)
			}
			t.WithLock(func() {
				t.screen().writeRunes(runes)
			})
			continue
		}
		b, err := gr.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if b == 27 {
			t.WithLock(func() {
				_ = t.handleCommand(&captureReader{r: gr})
			})
		}
	}
}

func stripNul(runes []rune) string {
	var buf []rune
	for _, r := range runes {
		if r == 0 {
			continue
		}
		buf = append(buf, r)
	}
	return string(buf)
}
