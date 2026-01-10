package termemu

import (
	"fmt"
	"strconv"
	"strings"
)

// KeyMod is a bitmask of active modifier keys.
// It follows the Kitty keyboard protocol bit assignments.
type KeyMod uint8

const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
	ModSuper
	ModHyper
	ModMeta
	ModCapsLock
	ModNumLock
)

// KeyEventType is press/repeat/release. Zero is treated as press.
type KeyEventType uint8

const (
	KeyPress KeyEventType = iota + 1
	KeyRepeat
	KeyRelease
)

// KeyboardEnhancement flags correspond to the Kitty progressive enhancement bits.
type KeyboardEnhancement int

const (
	KbdDisambiguate KeyboardEnhancement = 1 << iota
	KbdReportEvents
	KbdReportAlternates
	KbdReportAllKeys
	KbdReportText
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
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
	KeyF21
	KeyF22
	KeyF23
	KeyF24
	KeyF25
	KeyF26
	KeyF27
	KeyF28
	KeyF29
	KeyF30
	KeyF31
	KeyF32
	KeyF33
	KeyF34
	KeyF35
	KeyCapsLock
	KeyScrollLock
	KeyNumLock
	KeyPrintScreen
	KeyPause
	KeyMenu
	KeyKP0
	KeyKP1
	KeyKP2
	KeyKP3
	KeyKP4
	KeyKP5
	KeyKP6
	KeyKP7
	KeyKP8
	KeyKP9
	KeyKPDecimal
	KeyKPDivide
	KeyKPMultiply
	KeyKPSubtract
	KeyKPAdd
	KeyKPEnter
	KeyKPEqual
	KeyKPSeparator
	KeyKPLeft
	KeyKPRight
	KeyKPUp
	KeyKPDown
	KeyKPPageUp
	KeyKPPageDown
	KeyKPHome
	KeyKPEnd
	KeyKPInsert
	KeyKPDelete
	KeyKPBegin
	KeyMediaPlay
	KeyMediaPause
	KeyMediaPlayPause
	KeyMediaReverse
	KeyMediaStop
	KeyMediaFastForward
	KeyMediaRewind
	KeyMediaTrackNext
	KeyMediaTrackPrev
	KeyMediaRecord
	KeyVolumeDown
	KeyVolumeUp
	KeyVolumeMute
	KeyLeftShift
	KeyLeftControl
	KeyLeftAlt
	KeyLeftSuper
	KeyLeftHyper
	KeyLeftMeta
	KeyRightShift
	KeyRightControl
	KeyRightAlt
	KeyRightSuper
	KeyRightHyper
	KeyRightMeta
	KeyISOLevel3Shift
	KeyISOLevel5Shift
)

type KeyEvent struct {
	// Code is required. Use KeyRune for text-producing keys.
	Code KeyCode

	// Rune is required when Code == KeyRune. Otherwise it is ignored.
	Rune rune

	// Mod is a bitmask of active modifiers. For modifier key events, include
	// the modifier state resulting from the event.
	Mod KeyMod

	// Event defaults to KeyPress when zero. Repeat and release require the
	// report-events enhancement.
	Event KeyEventType

	// Shifted and BaseLayout are optional alternate key codes used when the
	// report-alternates enhancement is enabled. Shifted should only be set if
	// shift is pressed.
	Shifted    rune
	BaseLayout rune

	// Text is optional associated text codepoints used when report-text is
	// enabled with report-all-keys. If empty, Rune is used for KeyRune.
	Text []rune
}

func (t *terminal) SendKey(ev KeyEvent) (int, error) {
	seq := t.encodeKey(ev)
	if len(seq) == 0 {
		return 0, nil
	}
	return t.Write(seq)
}

func (t *terminal) encodeKey(ev KeyEvent) []byte {
	flags := t.keyboardFlags()
	if flags != 0 {
		if seq := t.encodeKittyKey(ev, flags); seq != nil {
			return seq
		}
	}
	return t.encodeLegacyKey(ev)
}

func (t *terminal) encodeLegacyKey(ev KeyEvent) []byte {
	if isKeypadKey(ev.Code) {
		if mapped, ok := keypadEquivalent(ev); ok {
			return t.encodeLegacyKey(mapped)
		}
	}
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
		if code, ok := kittyFunctionalCode(ev.Code); ok {
			return kittyCSIu(kittyKeyField(code, ev, 0), kittyModField(ev.Mod, ev.Event, 0), "")
		}
		return nil
	}
}

func (t *terminal) encodeKittyKey(ev KeyEvent, flags int) []byte {
	if isKeypadKey(ev.Code) && flags&int(KbdDisambiguate) == 0 {
		if mapped, ok := keypadEquivalent(ev); ok {
			return t.encodeKittyKey(mapped, flags)
		}
	}
	modField := kittyModField(ev.Mod, ev.Event, flags)
	switch ev.Code {
	case KeyRune:
		return t.encodeKittyRune(ev, flags)
	case KeyUp:
		return kittyCSI1('A', modField)
	case KeyDown:
		return kittyCSI1('B', modField)
	case KeyRight:
		return kittyCSI1('C', modField)
	case KeyLeft:
		return kittyCSI1('D', modField)
	case KeyHome:
		return kittyCSI1('H', modField)
	case KeyEnd:
		return kittyCSI1('F', modField)
	case KeyInsert:
		return kittyCSITilde(2, modField)
	case KeyDelete:
		return kittyCSITilde(3, modField)
	case KeyPageUp:
		return kittyCSITilde(5, modField)
	case KeyPageDown:
		return kittyCSITilde(6, modField)
	case KeyF1:
		return kittyCSI1('P', modField)
	case KeyF2:
		return kittyCSI1('Q', modField)
	case KeyF3:
		return kittyCSI1('R', modField)
	case KeyF4:
		return kittyCSI1('S', modField)
	case KeyF5:
		return kittyCSITilde(15, modField)
	case KeyF6:
		return kittyCSITilde(17, modField)
	case KeyF7:
		return kittyCSITilde(18, modField)
	case KeyF8:
		return kittyCSITilde(19, modField)
	case KeyF9:
		return kittyCSITilde(20, modField)
	case KeyF10:
		return kittyCSITilde(21, modField)
	case KeyF11:
		return kittyCSITilde(23, modField)
	case KeyF12:
		return kittyCSITilde(24, modField)
	case KeyEscape:
		if flags&int(KbdReportAllKeys) != 0 || flags&int(KbdDisambiguate) != 0 {
			return kittyCSIu(kittyKeyField(27, ev, flags), modField, kittyTextField(ev, flags))
		}
		return nil
	case KeyEnter:
		if flags&int(KbdReportAllKeys) != 0 {
			return kittyCSIu(kittyKeyField(13, ev, flags), modField, kittyTextField(ev, flags))
		}
		return nil
	case KeyTab:
		if flags&int(KbdReportAllKeys) != 0 {
			return kittyCSIu(kittyKeyField(9, ev, flags), modField, kittyTextField(ev, flags))
		}
		return nil
	case KeyBackspace:
		if flags&int(KbdReportAllKeys) != 0 {
			return kittyCSIu(kittyKeyField(127, ev, flags), modField, kittyTextField(ev, flags))
		}
		return nil
	default:
		if code, ok := kittyFunctionalCode(ev.Code); ok {
			return kittyCSIu(kittyKeyField(code, ev, flags), modField, kittyTextField(ev, flags))
		}
		return nil
	}
}

func (t *terminal) encodeKittyRune(ev KeyEvent, flags int) []byte {
	if ev.Rune == 0 {
		return nil
	}
	if flags&int(KbdReportAllKeys) != 0 {
		return kittyCSIu(kittyKeyField(int(ev.Rune), ev, flags), kittyModField(ev.Mod, ev.Event, flags), kittyTextField(ev, flags))
	}
	if flags&int(KbdDisambiguate) != 0 {
		if ev.Mod&(ModAlt|ModCtrl|ModSuper|ModHyper|ModMeta) != 0 {
			return kittyCSIu(kittyKeyField(int(ev.Rune), ev, flags), kittyModField(ev.Mod, ev.Event, flags), "")
		}
	}
	return nil
}

func isKeypadKey(code KeyCode) bool {
	switch code {
	case KeyKP0, KeyKP1, KeyKP2, KeyKP3, KeyKP4, KeyKP5, KeyKP6, KeyKP7, KeyKP8, KeyKP9,
		KeyKPDecimal, KeyKPDivide, KeyKPMultiply, KeyKPSubtract, KeyKPAdd, KeyKPEnter,
		KeyKPEqual, KeyKPSeparator, KeyKPLeft, KeyKPRight, KeyKPUp, KeyKPDown,
		KeyKPPageUp, KeyKPPageDown, KeyKPHome, KeyKPEnd, KeyKPInsert, KeyKPDelete, KeyKPBegin:
		return true
	default:
		return false
	}
}

func keypadEquivalent(ev KeyEvent) (KeyEvent, bool) {
	mapped := ev
	switch ev.Code {
	case KeyKP0:
		mapped.Code, mapped.Rune = KeyRune, '0'
	case KeyKP1:
		mapped.Code, mapped.Rune = KeyRune, '1'
	case KeyKP2:
		mapped.Code, mapped.Rune = KeyRune, '2'
	case KeyKP3:
		mapped.Code, mapped.Rune = KeyRune, '3'
	case KeyKP4:
		mapped.Code, mapped.Rune = KeyRune, '4'
	case KeyKP5:
		mapped.Code, mapped.Rune = KeyRune, '5'
	case KeyKP6:
		mapped.Code, mapped.Rune = KeyRune, '6'
	case KeyKP7:
		mapped.Code, mapped.Rune = KeyRune, '7'
	case KeyKP8:
		mapped.Code, mapped.Rune = KeyRune, '8'
	case KeyKP9:
		mapped.Code, mapped.Rune = KeyRune, '9'
	case KeyKPDecimal:
		mapped.Code, mapped.Rune = KeyRune, '.'
	case KeyKPDivide:
		mapped.Code, mapped.Rune = KeyRune, '/'
	case KeyKPMultiply:
		mapped.Code, mapped.Rune = KeyRune, '*'
	case KeyKPSubtract:
		mapped.Code, mapped.Rune = KeyRune, '-'
	case KeyKPAdd:
		mapped.Code, mapped.Rune = KeyRune, '+'
	case KeyKPEnter:
		mapped.Code, mapped.Rune = KeyEnter, 0
	case KeyKPEqual:
		mapped.Code, mapped.Rune = KeyRune, '='
	case KeyKPSeparator:
		mapped.Code, mapped.Rune = KeyRune, ','
	case KeyKPLeft:
		mapped.Code = KeyLeft
	case KeyKPRight:
		mapped.Code = KeyRight
	case KeyKPUp:
		mapped.Code = KeyUp
	case KeyKPDown:
		mapped.Code = KeyDown
	case KeyKPPageUp:
		mapped.Code = KeyPageUp
	case KeyKPPageDown:
		mapped.Code = KeyPageDown
	case KeyKPHome:
		mapped.Code = KeyHome
	case KeyKPEnd:
		mapped.Code = KeyEnd
	case KeyKPInsert:
		mapped.Code = KeyInsert
	case KeyKPDelete:
		mapped.Code = KeyDelete
	case KeyKPBegin:
		mapped.Code, mapped.Rune = KeyRune, '5'
	default:
		return KeyEvent{}, false
	}
	return mapped, true
}

func kittyFunctionalCode(code KeyCode) (int, bool) {
	switch code {
	case KeyCapsLock:
		return 57358, true
	case KeyScrollLock:
		return 57359, true
	case KeyNumLock:
		return 57360, true
	case KeyPrintScreen:
		return 57361, true
	case KeyPause:
		return 57362, true
	case KeyMenu:
		return 57363, true
	case KeyF13:
		return 57376, true
	case KeyF14:
		return 57377, true
	case KeyF15:
		return 57378, true
	case KeyF16:
		return 57379, true
	case KeyF17:
		return 57380, true
	case KeyF18:
		return 57381, true
	case KeyF19:
		return 57382, true
	case KeyF20:
		return 57383, true
	case KeyF21:
		return 57384, true
	case KeyF22:
		return 57385, true
	case KeyF23:
		return 57386, true
	case KeyF24:
		return 57387, true
	case KeyF25:
		return 57388, true
	case KeyF26:
		return 57389, true
	case KeyF27:
		return 57390, true
	case KeyF28:
		return 57391, true
	case KeyF29:
		return 57392, true
	case KeyF30:
		return 57393, true
	case KeyF31:
		return 57394, true
	case KeyF32:
		return 57395, true
	case KeyF33:
		return 57396, true
	case KeyF34:
		return 57397, true
	case KeyF35:
		return 57398, true
	case KeyKP0:
		return 57399, true
	case KeyKP1:
		return 57400, true
	case KeyKP2:
		return 57401, true
	case KeyKP3:
		return 57402, true
	case KeyKP4:
		return 57403, true
	case KeyKP5:
		return 57404, true
	case KeyKP6:
		return 57405, true
	case KeyKP7:
		return 57406, true
	case KeyKP8:
		return 57407, true
	case KeyKP9:
		return 57408, true
	case KeyKPDecimal:
		return 57409, true
	case KeyKPDivide:
		return 57410, true
	case KeyKPMultiply:
		return 57411, true
	case KeyKPSubtract:
		return 57412, true
	case KeyKPAdd:
		return 57413, true
	case KeyKPEnter:
		return 57414, true
	case KeyKPEqual:
		return 57415, true
	case KeyKPSeparator:
		return 57416, true
	case KeyKPLeft:
		return 57417, true
	case KeyKPRight:
		return 57418, true
	case KeyKPUp:
		return 57419, true
	case KeyKPDown:
		return 57420, true
	case KeyKPPageUp:
		return 57421, true
	case KeyKPPageDown:
		return 57422, true
	case KeyKPHome:
		return 57423, true
	case KeyKPEnd:
		return 57424, true
	case KeyKPInsert:
		return 57425, true
	case KeyKPDelete:
		return 57426, true
	case KeyKPBegin:
		return 57427, true
	case KeyMediaPlay:
		return 57428, true
	case KeyMediaPause:
		return 57429, true
	case KeyMediaPlayPause:
		return 57430, true
	case KeyMediaReverse:
		return 57431, true
	case KeyMediaStop:
		return 57432, true
	case KeyMediaFastForward:
		return 57433, true
	case KeyMediaRewind:
		return 57434, true
	case KeyMediaTrackNext:
		return 57435, true
	case KeyMediaTrackPrev:
		return 57436, true
	case KeyMediaRecord:
		return 57437, true
	case KeyVolumeDown:
		return 57438, true
	case KeyVolumeUp:
		return 57439, true
	case KeyVolumeMute:
		return 57440, true
	case KeyLeftShift:
		return 57441, true
	case KeyLeftControl:
		return 57442, true
	case KeyLeftAlt:
		return 57443, true
	case KeyLeftSuper:
		return 57444, true
	case KeyLeftHyper:
		return 57445, true
	case KeyLeftMeta:
		return 57446, true
	case KeyRightShift:
		return 57447, true
	case KeyRightControl:
		return 57448, true
	case KeyRightAlt:
		return 57449, true
	case KeyRightSuper:
		return 57450, true
	case KeyRightHyper:
		return 57451, true
	case KeyRightMeta:
		return 57452, true
	case KeyISOLevel3Shift:
		return 57453, true
	case KeyISOLevel5Shift:
		return 57454, true
	default:
		return 0, false
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

func kittyModParam(mod KeyMod) int {
	return 1 + int(mod)
}

func kittyModField(mod KeyMod, event KeyEventType, flags int) string {
	event = normalizeEventType(event)
	modParam := kittyModParam(mod)
	if flags&int(KbdReportEvents) != 0 && event != KeyPress {
		return fmt.Sprintf("%d:%d", modParam, event)
	}
	if mod == 0 {
		return ""
	}
	return strconv.Itoa(modParam)
}

func kittyKeyField(code int, ev KeyEvent, flags int) string {
	keyField := strconv.Itoa(code)
	if flags&int(KbdReportAlternates) == 0 {
		return keyField
	}
	shifted := 0
	if ev.Shifted != 0 && ev.Mod&ModShift != 0 {
		shifted = int(ev.Shifted)
	}
	base := 0
	if ev.BaseLayout != 0 {
		base = int(ev.BaseLayout)
	}
	switch {
	case shifted != 0 && base != 0:
		return fmt.Sprintf("%d:%d:%d", code, shifted, base)
	case shifted != 0:
		return fmt.Sprintf("%d:%d", code, shifted)
	case base != 0:
		return fmt.Sprintf("%d::%d", code, base)
	default:
		return keyField
	}
}

func kittyTextField(ev KeyEvent, flags int) string {
	if flags&int(KbdReportAllKeys) == 0 || flags&int(KbdReportText) == 0 {
		return ""
	}
	text := ev.Text
	if len(text) == 0 && ev.Code == KeyRune && ev.Rune != 0 {
		text = []rune{ev.Rune}
	}
	if len(text) == 0 {
		return ""
	}
	parts := make([]string, 0, len(text))
	for _, r := range text {
		parts = append(parts, strconv.Itoa(int(r)))
	}
	return strings.Join(parts, ":")
}

func kittyCSI1(final byte, modField string) []byte {
	if modField == "" {
		return []byte{0x1b, '[', final}
	}
	return []byte(fmt.Sprintf("\033[1;%s%c", modField, final))
}

func kittyCSITilde(code int, modField string) []byte {
	if modField == "" {
		return []byte(fmt.Sprintf("\033[%d~", code))
	}
	return []byte(fmt.Sprintf("\033[%d;%s~", code, modField))
}

func kittyCSIu(keyField, modField, textField string) []byte {
	params := []string{keyField}
	if modField != "" || textField != "" {
		if modField == "" {
			modField = "1"
		}
		params = append(params, modField)
		if textField != "" {
			params = append(params, textField)
		}
	}
	return []byte("\033[" + strings.Join(params, ";") + "u")
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

func normalizeEventType(event KeyEventType) KeyEventType {
	if event == 0 {
		return KeyPress
	}
	return event
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
