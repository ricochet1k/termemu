package termemu

import "testing"

func TestEncodeKey_ModifyOtherKeys(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.viewInts[VIModifyOtherKeys] = 2

	out := string(t1.encodeKey(KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl}))
	if out != "\x1b[27;5;97~" {
		t.Fatalf("expected modifyOtherKeys sequence, got %q", out)
	}
}

func TestEncodeKey_CtrlRuneDefault(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	out := t1.encodeKey(KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl})
	if len(out) != 1 || out[0] != 0x01 {
		t.Fatalf("expected ctrl-a, got %q", string(out))
	}
}

func TestEncodeKey_KittyDisambiguateAlt(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.keyboardMain.flags = int(KbdDisambiguate)

	out := string(t1.encodeKey(KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModAlt}))
	if out != "\x1b[97;3u" {
		t.Fatalf("expected kitty disambiguate alt sequence, got %q", out)
	}
}

func TestEncodeKey_KittyReportEventsRepeat(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.keyboardMain.flags = int(KbdReportAllKeys | KbdReportEvents)

	out := string(t1.encodeKey(KeyEvent{Code: KeyUp, Event: KeyRepeat}))
	if out != "\x1b[1;1:2A" {
		t.Fatalf("expected kitty repeat sequence, got %q", out)
	}
}
