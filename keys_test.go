package termemu

import "testing"

func TestEncodeKey_ModifyOtherKeys(t *testing.T) {
	t1, _ := MakeTerminalWithMock()
	t1.viewInts[VIModifyOtherKeys] = 2

	out := string(t1.encodeKey(KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl}))
	if out != "\x1b[27;5;97~" {
		t.Fatalf("expected modifyOtherKeys sequence, got %q", out)
	}
}

func TestEncodeKey_CtrlRuneDefault(t *testing.T) {
	t1, _ := MakeTerminalWithMock()

	out := t1.encodeKey(KeyEvent{Code: KeyRune, Rune: 'a', Mod: ModCtrl})
	if len(out) != 1 || out[0] != 0x01 {
		t.Fatalf("expected ctrl-a, got %q", string(out))
	}
}
