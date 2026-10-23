package termemu

import (
	"fmt"
	"testing"
)

func keyCodeName(code KeyCode) string {
	return fmt.Sprintf("KeyCode(%d)", int(code))
}

func allKeyCodes() []KeyCode {
	codes := make([]KeyCode, 0, int(KeyISOLevel5Shift-KeyRune)+1)
	for code := KeyRune; code <= KeyISOLevel5Shift; code++ {
		codes = append(codes, code)
	}
	return codes
}

// TestKeyboardEncodingCoversAllKeys verifies that every KeyCode has an
// encoding in both legacy and Kitty modes. This catches newly added KeyCode
// constants that were never wired into the encoder tables.
func TestKeyboardEncodingCoversAllKeys(t *testing.T) {
	ev := KeyEvent{
		Code:       KeyRune,
		Rune:       'a',
		Mod:        ModShift | ModAlt | ModCtrl,
		Event:      KeyPress,
		Shifted:    'A',
		BaseLayout: 'a',
		Text:       []rune("é"),
	}
	for _, code := range allKeyCodes() {
		ev.Code = code
		_, term, _ := MakeTerminalWithMock(TextReadModeRune)

		term.keyboardMain.flags = 0
		if got := term.encodeKey(ev); len(got) == 0 {
			t.Errorf("legacy encoding for %s is empty", keyCodeName(code))
		}

		term.keyboardMain.flags = int(KbdDisambiguate | KbdReportEvents | KbdReportAlternates | KbdReportAllKeys | KbdReportText)
		if got := term.encodeKey(ev); len(got) == 0 {
			t.Errorf("kitty encoding for %s is empty", keyCodeName(code))
		}
	}
}

// TestKeyboardFunctionalCodeTable is a property check over the Kitty
// functional key table: codes are unique, in the allocated range, and every
// keypad key has exactly one non-keypad equivalent.
func TestKeyboardFunctionalCodeTable(t *testing.T) {
	seen := make(map[int]KeyCode)
	for _, code := range allKeyCodes() {
		if fc, ok := kittyFunctionalCode(code); ok {
			if fc < 57358 || fc > 57454 {
				t.Errorf("%s maps to kitty code %d outside the functional range", keyCodeName(code), fc)
			}
			if prev, dup := seen[fc]; dup {
				t.Errorf("%s and %s both map to kitty code %d", keyCodeName(prev), keyCodeName(code), fc)
			}
			seen[fc] = code
		}

		if !isKeypadKey(code) {
			continue
		}
		mapped, ok := keypadEquivalent(KeyEvent{Code: code, Mod: ModShift, Event: KeyRepeat})
		if !ok {
			t.Errorf("keypad key %s has no equivalent", keyCodeName(code))
			continue
		}
		if isKeypadKey(mapped.Code) {
			t.Errorf("keypad key %s maps to another keypad key %s", keyCodeName(code), keyCodeName(mapped.Code))
		}
		if mapped.Mod != ModShift || mapped.Event != KeyRepeat {
			t.Errorf("keypad equivalent for %s did not preserve modifiers/event", keyCodeName(code))
		}
	}
}

func TestLegacyKeyEncoding(t *testing.T) {
	tests := []struct {
		name string
		ev   KeyEvent
		app  bool
		want string
	}{
		{name: "up", ev: KeyEvent{Code: KeyUp}, want: "\x1b[A"},
		{name: "up app cursor", ev: KeyEvent{Code: KeyUp}, app: true, want: "\x1bOA"},
		{name: "up ctrl", ev: KeyEvent{Code: KeyUp, Mod: ModCtrl}, want: "\x1b[1;5A"},
		{name: "home", ev: KeyEvent{Code: KeyHome}, want: "\x1b[H"},
		{name: "home app cursor", ev: KeyEvent{Code: KeyHome}, app: true, want: "\x1bOH"},
		{name: "end shift", ev: KeyEvent{Code: KeyEnd, Mod: ModShift}, want: "\x1b[1;2F"},
		{name: "insert", ev: KeyEvent{Code: KeyInsert}, want: "\x1b[2~"},
		{name: "delete", ev: KeyEvent{Code: KeyDelete}, want: "\x1b[3~"},
		{name: "page up", ev: KeyEvent{Code: KeyPageUp}, want: "\x1b[5~"},
		{name: "page down alt", ev: KeyEvent{Code: KeyPageDown, Mod: ModAlt}, want: "\x1b[6;3~"},
		{name: "f1", ev: KeyEvent{Code: KeyF1}, want: "\x1bOP"},
		{name: "f5", ev: KeyEvent{Code: KeyF5}, want: "\x1b[15~"},
		{name: "f5 ctrl", ev: KeyEvent{Code: KeyF5, Mod: ModCtrl}, want: "\x1b[15;5~"},
		{name: "backspace", ev: KeyEvent{Code: KeyBackspace}, want: "\x7f"},
		{name: "backspace alt", ev: KeyEvent{Code: KeyBackspace, Mod: ModAlt}, want: "\x1b\x7f"},
		{name: "tab", ev: KeyEvent{Code: KeyTab}, want: "\t"},
		{name: "shift tab", ev: KeyEvent{Code: KeyTab, Mod: ModShift}, want: "\x1b[Z"},
		{name: "enter", ev: KeyEvent{Code: KeyEnter}, want: "\r"},
		{name: "escape", ev: KeyEvent{Code: KeyEscape}, want: "\x1b"},
		{name: "keypad 0", ev: KeyEvent{Code: KeyKP0}, want: "0"},
		{name: "keypad enter", ev: KeyEvent{Code: KeyKPEnter}, want: "\r"},
		{name: "keypad left", ev: KeyEvent{Code: KeyKPLeft}, want: "\x1b[D"},
		{name: "ctrl a", ev: KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl}, want: "\x01"},
		{name: "alt x", ev: KeyEvent{Code: KeyRune, Rune: 'x', Mod: ModAlt}, want: "\x1bx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, term, _ := MakeTerminalWithMock(TextReadModeRune)
			term.viewFlags[VFAppCursorKeys] = tt.app
			if got := string(term.encodeKey(tt.ev)); got != tt.want {
				t.Fatalf("encodeKey(%+v) = %q, want %q", tt.ev, got, tt.want)
			}
		})
	}
}

func TestKittyKeyEncoding(t *testing.T) {
	const all = int(KbdDisambiguate | KbdReportEvents | KbdReportAlternates | KbdReportAllKeys | KbdReportText)
	tests := []struct {
		name  string
		flags int
		ev    KeyEvent
		want  string
	}{
		{
			name:  "enter report all",
			flags: int(KbdReportAllKeys),
			ev:    KeyEvent{Code: KeyEnter},
			want:  "\x1b[13u",
		},
		{
			name:  "enter report text",
			flags: int(KbdReportAllKeys | KbdReportText),
			ev:    KeyEvent{Code: KeyEnter, Text: []rune("é")},
			want:  "\x1b[13;1;233u",
		},
		{
			name:  "rune alternates",
			flags: int(KbdReportAllKeys | KbdReportAlternates),
			ev:    KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModShift, Shifted: 'A', BaseLayout: 'a'},
			want:  "\x1b[97:65:97;2u",
		},
		{
			name:  "rune release event",
			flags: int(KbdReportAllKeys | KbdReportEvents),
			ev:    KeyEvent{Code: KeyRune, Rune: 'a', Event: KeyRelease},
			want:  "\x1b[97;1:3u",
		},
		{
			name:  "rune ctrl disambiguate",
			flags: int(KbdDisambiguate),
			ev:    KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl},
			want:  "\x1b[97;5u",
		},
		{
			name:  "f1 report all",
			flags: int(KbdReportAllKeys),
			ev:    KeyEvent{Code: KeyF1},
			want:  "\x1b[P",
		},
		{
			name:  "keypad with disambiguate",
			flags: all,
			ev:    KeyEvent{Code: KeyKP1},
			want:  "\x1b[57400u",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, term, _ := MakeTerminalWithMock(TextReadModeRune)
			term.keyboardMain.flags = tt.flags
			if got := string(term.encodeKey(tt.ev)); got != tt.want {
				t.Fatalf("encodeKey(%+v) = %q, want %q", tt.ev, got, tt.want)
			}
		})
	}
}
