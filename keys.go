package termemu

import "fmt"

type KeyMod uint8

const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
)

type KeyCode int

const (
	KeyRune KeyCode = iota
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyHome
	KeyEnd
	KeyInsert
	KeyDelete
	KeyPageUp
	KeyPageDown
	KeyBackspace
	KeyTab
	KeyEnter
	KeyEscape
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

type KeyEvent struct {
	Code KeyCode
	Rune rune
	Mod  KeyMod
}

func (t *terminal) SendKey(ev KeyEvent) (int, error) {
	seq := t.encodeKey(ev)
	if len(seq) == 0 {
		return 0, nil
	}
	return t.Write(seq)
}

func (t *terminal) encodeKey(ev KeyEvent) []byte {
	switch ev.Code {
	case KeyRune:
		return t.encodeRuneKey(ev.Rune, ev.Mod)
	case KeyUp:
		return t.encodeCursorKey('A', ev.Mod)
	case KeyDown:
		return t.encodeCursorKey('B', ev.Mod)
	case KeyRight:
		return t.encodeCursorKey('C', ev.Mod)
	case KeyLeft:
		return t.encodeCursorKey('D', ev.Mod)
	case KeyHome:
		return t.encodeHomeEndKey('H', ev.Mod)
	case KeyEnd:
		return t.encodeHomeEndKey('F', ev.Mod)
	case KeyInsert:
		return encodeTildeKey(2, ev.Mod)
	case KeyDelete:
		return encodeTildeKey(3, ev.Mod)
	case KeyPageUp:
		return encodeTildeKey(5, ev.Mod)
	case KeyPageDown:
		return encodeTildeKey(6, ev.Mod)
	case KeyBackspace:
		return t.encodeBackspaceKey(ev.Mod)
	case KeyTab:
		return t.encodeTabKey(ev.Mod)
	case KeyEnter:
		return t.encodeEnterKey(ev.Mod)
	case KeyEscape:
		return t.encodeEscapeKey(ev.Mod)
	case KeyF1:
		return encodeFunctionKey('P', ev.Mod)
	case KeyF2:
		return encodeFunctionKey('Q', ev.Mod)
	case KeyF3:
		return encodeFunctionKey('R', ev.Mod)
	case KeyF4:
		return encodeFunctionKey('S', ev.Mod)
	case KeyF5:
		return encodeTildeKey(15, ev.Mod)
	case KeyF6:
		return encodeTildeKey(17, ev.Mod)
	case KeyF7:
		return encodeTildeKey(18, ev.Mod)
	case KeyF8:
		return encodeTildeKey(19, ev.Mod)
	case KeyF9:
		return encodeTildeKey(20, ev.Mod)
	case KeyF10:
		return encodeTildeKey(21, ev.Mod)
	case KeyF11:
		return encodeTildeKey(23, ev.Mod)
	case KeyF12:
		return encodeTildeKey(24, ev.Mod)
	default:
		return nil
	}
}

func (t *terminal) encodeRuneKey(r rune, mod KeyMod) []byte {
	if r == 0 {
		return nil
	}
	if t.viewInts[VIModifyOtherKeys] > 0 && mod != 0 {
		return encodeModifyOtherKeys(int(r), mod)
	}
	if mod&ModCtrl != 0 {
		if b, ok := ctrlByte(r); ok {
			if mod&ModAlt != 0 {
				return []byte{0x1b, b}
			}
			return []byte{b}
		}
	}
	out := []byte(string(r))
	if mod&ModAlt != 0 {
		return append([]byte{0x1b}, out...)
	}
	return out
}

func (t *terminal) encodeCursorKey(final byte, mod KeyMod) []byte {
	if mod == 0 && t.viewFlags[VFAppCursorKeys] {
		return []byte{0x1b, 'O', final}
	}
	if mod == 0 {
		return []byte{0x1b, '[', final}
	}
	return []byte(fmt.Sprintf("\033[1;%d%c", xtermModParam(mod), final))
}

func (t *terminal) encodeHomeEndKey(final byte, mod KeyMod) []byte {
	if mod == 0 && t.viewFlags[VFAppCursorKeys] {
		return []byte{0x1b, 'O', final}
	}
	if mod == 0 {
		return []byte{0x1b, '[', final}
	}
	return []byte(fmt.Sprintf("\033[1;%d%c", xtermModParam(mod), final))
}

func (t *terminal) encodeBackspaceKey(mod KeyMod) []byte {
	if mod == 0 {
		return []byte{0x7f}
	}
	if t.viewInts[VIModifyOtherKeys] > 0 {
		return encodeModifyOtherKeys(127, mod)
	}
	if mod&ModAlt != 0 {
		return []byte{0x1b, 0x7f}
	}
	return []byte{0x7f}
}

func (t *terminal) encodeTabKey(mod KeyMod) []byte {
	if mod == 0 {
		return []byte{'\t'}
	}
	if mod == ModShift {
		return []byte{0x1b, '[', 'Z'}
	}
	if t.viewInts[VIModifyOtherKeys] > 0 {
		return encodeModifyOtherKeys(9, mod)
	}
	if mod&ModAlt != 0 {
		return []byte{0x1b, '\t'}
	}
	return []byte{'\t'}
}

func (t *terminal) encodeEnterKey(mod KeyMod) []byte {
	if mod == 0 {
		return []byte{'\r'}
	}
	if t.viewInts[VIModifyOtherKeys] > 0 {
		return encodeModifyOtherKeys(13, mod)
	}
	if mod&ModAlt != 0 {
		return []byte{0x1b, '\r'}
	}
	return []byte{'\r'}
}

func (t *terminal) encodeEscapeKey(mod KeyMod) []byte {
	if mod == 0 {
		return []byte{0x1b}
	}
	if t.viewInts[VIModifyOtherKeys] > 0 {
		return encodeModifyOtherKeys(27, mod)
	}
	return []byte{0x1b}
}

func encodeTildeKey(code int, mod KeyMod) []byte {
	if mod == 0 {
		return []byte(fmt.Sprintf("\033[%d~", code))
	}
	return []byte(fmt.Sprintf("\033[%d;%d~", code, xtermModParam(mod)))
}

func encodeFunctionKey(final byte, mod KeyMod) []byte {
	if mod == 0 {
		return []byte{0x1b, 'O', final}
	}
	return []byte(fmt.Sprintf("\033[1;%d%c", xtermModParam(mod), final))
}

func encodeModifyOtherKeys(code int, mod KeyMod) []byte {
	return []byte(fmt.Sprintf("\033[27;%d;%d~", xtermModParam(mod), code))
}

func xtermModParam(mod KeyMod) int {
	param := 1
	if mod&ModShift != 0 {
		param += 1
	}
	if mod&ModAlt != 0 {
		param += 2
	}
	if mod&ModCtrl != 0 {
		param += 4
	}
	return param
}

func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 1), true
	case r >= 'A' && r <= 'Z':
		return byte(r - 'A' + 1), true
	}
	switch r {
	case '@':
		return 0, true
	case '[':
		return 27, true
	case '\\':
		return 28, true
	case ']':
		return 29, true
	case '^':
		return 30, true
	case '_':
		return 31, true
	case '?':
		return 127, true
	default:
		return 0, false
	}
}
