package termemu

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

type dupReader struct {
	reader *bufio.Reader
	buf    *bytes.Buffer
	t      *terminal
}

func (r *dupReader) ReadByte() (byte, error) {
	b, err := r.reader.ReadByte()
	if err == nil && r.t.dup != nil {
		r.t.dup.Write([]byte{b})
	}
	if r.buf != nil {
		r.buf.Write([]byte{b})
	}
	return b, err
}

func (r *dupReader) ReadRune() (rune, int, error) {
	b, n, err := r.reader.ReadRune()
	if err == nil {
		byts := []byte(string(b))
		if r.t.dup != nil {
			r.t.dup.Write(byts)
		}
		if r.buf != nil {
			r.buf.Write(byts)
		}
	}
	return b, n, err
}
func (r *dupReader) Buffered() int {
	return r.reader.Buffered()
}

func (t *terminal) ptyReadLoop() {

	r := &dupReader{
		reader: bufio.NewReader(t.pty),
		buf:    nil,
		t:      t,
	}

	for {
		b, _, err := r.ReadRune()
		if err != nil {
			debugPrintln(debugErrors, err)
			return
		}

		// printables
		runes := []rune{}
		for {
			if b < 32 || b > 126 && b < 128 {
				break
			}

			runes = append(runes, rune(b))

			if r.Buffered() == 0 {
				break
			}

			b, _, err = r.ReadRune()
			if err != nil {
				debugPrintln(debugErrors, err)
				return
			}
		}
		if len(runes) > 0 {
			t.WithLock(func() {
				t.screen().writeRunes(runes)
			})
			debugPrintf(debugTxt, "\033[32mtxt: %#v\033[0m %v\n", string(runes), len(runes))

			if r.Buffered() == 0 {
				continue
			}
		}

		// ABCDEFGHIJKLMNOPQRSTUVWXYZ

		switch b {
		case 0: // NUL Null byte, ignore

		case 5: // ENQ ^E Return Terminal Status
			debugPrintln(debugTodo, "TODO: ENQ")
		case 7: // BEL ^G Bell
			t.WithLock(func() {
				t.frontend.Bell()
			})

		case 8: // BS ^H Backspace
			t.WithLock(func() {
				t.screen().moveCursor(-1, 0, false, false)
			})

		case 9: // HT ^I Horizontal TAB
			debugPrintln(debugTodo, "TODO: tab")

		case 10: // LF ^J Linefeed (newline)
			t.WithLock(func() {
				t.screen().moveCursor(0, 1, true, true)
			})

		case 11: // VT ^K Vertical TAB
			debugPrintln(debugTodo, "TODO: vtab")

		case 12: // FF ^L Formfeed (also: New page NP)
			t.WithLock(func() {
				t.screen().moveCursor(0, 1, false, true)
			})

		case 13: // CR ^M Carriage Return
			t.WithLock(func() {
				t.screen().moveCursor(-t.screen().cursorPos.X, 0, true, true)
			})

		case 27: // ESC ^[ Escape Character

			var cmdBytes bytes.Buffer
			r.buf = &cmdBytes

			t.WithLock(func() {
				success := t.handleCommand(r)
				r.buf = nil
				cmd := cmdBytes.Bytes()

				if success {
					debugPrintf(debugCmd, "%v cmd: %#v\n", t.screen().cursorPos, string(cmd))
					if string(cmd) == "[?25l" {
						// hide cursor
						if t.dup != nil {
							t.dup.Write([]byte(string("\033[?25h")))
						}
					}
				} else {
					debugPrintf(debugTodo, "TODO: Unhandled command: %#v\n", string(cmd))
				}
			})
			continue

		case 127: // DEL  Delete Character
			debugPrintln(debugTodo, "TODO: delete character")
		default:
			debugPrintf(debugTodo, "TODO: unhandled char %v %#v\n", b, string(b))
			continue
		}
	}
}

func (t *terminal) handleCommand(r *dupReader) bool {
	b, _, err := r.ReadRune()
	if err != nil {
		debugPrintln(debugErrors, err)
		return false
	}

	// short commands
	switch b {

	case 'c': // reset
		debugPrintln(debugTodo, "TODO: cmd: reset") // TODO

	case 'D': // Index, scroll down if necessary
		t.screen().moveCursor(0, 1, false, true)

	case 'M': // Reverse index, scroll up if necessary
		t.screen().moveCursor(0, -1, false, true)

	case '[': // CSI Control Sequence Introducer
		return t.handleCmdCSI(r)

	case ']': // OSC Operating System Commands
		return t.handleCmdOSC(r)

	case '(': // G0
		fallthrough
	case ')': // G1
		fallthrough
	case '*': // G2
		fallthrough
	case '+': // G3
		C, _, err := r.ReadRune()
		if err != nil {
			debugPrintln(debugErrors, err)
			return false
		}

		debugPrintf(debugCharSet, "TODO: Character Set %c %c\n", b, C)

	case '=': // Application Keypad
		debugPrintln(debugTodo, "TODO: Application Keypad") // TODO

	case '>': // Normal Keypad
		debugPrintln(debugTodo, "TODO: Normal Keypad") // TODO

	/*case 'l': // Memory Lock
		debugPrintln("Memory Lock") // TODO

	case 'm': // Memory Unlock
		debugPrintln("Memory Unlock") // TODO*/

	default:
		return false
	}
	return true
}

func (t *terminal) handleCmdCSI(r *dupReader) bool {

	b, _, err := r.ReadRune()
	if err != nil {
		debugPrintln(debugErrors, err)
		return false
	}

	var prefix = []byte{}
	if b == '?' || b == '>' {
		prefix = append(prefix, byte(b))

		b, _, err = r.ReadRune()
		if err != nil {
			debugPrintln(debugErrors, err)
			return false
		}
	}

	paramBytes := []byte{}
	for b == ';' || (b >= '0' && b <= '9') {
		paramBytes = append(paramBytes, byte(b))

		b, _, err = r.ReadRune()
		if err != nil {
			debugPrintln(debugErrors, err)
			return false
		}
	}

	paramParts := strings.Split(string(paramBytes), ";")
	if len(paramParts) == 1 && paramParts[0] == "" {
		paramParts = []string{}
	}
	params := make([]int, len(paramParts))
	for i, p := range paramParts {
		params[i], _ = strconv.Atoi(p)
	}

	if string(prefix) == "" {
		switch b {
		case 'A': // Move cursor up
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().moveCursor(0, -params[0], false, true)
		case 'B': // Move cursor down
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().moveCursor(0, params[0], false, true)
		case 'C': // Move cursor forward
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().moveCursor(params[0], 0, false, false)
		case 'D': // Move cursor backward
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().moveCursor(-params[0], 0, false, false)

		case 'G': // Cursor Character Absolute
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().setCursorPos(params[0]-1, t.screen().cursorPos.Y)

		case 'c': // Send Device Attributes
			if len(params) == 0 {
				params = []int{1}
			}
			switch params[0] {
			case 0:
				t.pty.Write([]byte("\033[?1;2c"))
			}

		case 'd': // Line Position Absolute
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().setCursorPos(t.screen().cursorPos.X, params[0]-1)

		case 'f', 'H': // Cursor Home
			x := 1
			y := 1
			if len(params) >= 1 {
				y = params[0]
			}
			if len(params) >= 2 {
				x = params[1]
			}
			// debugPrintf("cursor home: %v, %v\n", x, y)
			t.screen().setCursorPos(x-1, y-1)

		case 'h', 'l': // h=Set, l=Reset Mode
			var value bool
			if b == 'h' {
				value = true
			}

			if len(params) != 1 {
				debugPrintln(debugTodo, "TODO: Unhandled CSI mode params: ", params, b)
				return false
			}

			switch params[0] {
			case 4:
				debugPrintln(debugTodo, "TODO: Insert Mode = ", value) // TODO
			default:
				debugPrintln(debugTodo, "TODO: Unhandled CSI mode param: ", params[0])
				return false
			}

		case 'm': // Set color/mode
			if len(params) == 0 {
				params = []int{0}
			}

			fc := t.screen().frontColor
			bc := t.screen().backColor

			for i := 0; i < len(params); i++ {
				p := params[i]
				switch {
				case p == 0: // reset mode
					fc = ColWhite
					bc = ColBlack

				case p >= 1 && p <= 8:
					fc = fc.SetMode(ColorModes[p-1])

				case p == 22:
					fc = fc.ResetMode(ModeBold).ResetMode(ModeDim)

				case p == 23:
					fc = fc.ResetMode(ModeItalic)

				case p == 24:
					fc = fc.ResetMode(ModeUnderline)

				case p == 27:
					fc = fc.ResetMode(ModeReverse)

				case p >= 30 && p <= 37:
					fc = fc.SetColor(Colors8[p-30])

				case p == 39: // default color
					fc = fc.SetColor(ColWhite)

				case p >= 40 && p <= 47:
					bc = bc.SetColor(Colors8[p-40])

				case p == 49: // default color
					bc = bc.SetColor(ColBlack)

				case p == 38 || p == 48: // extended set color
					if i+2 < len(params) {
						switch params[i+1] {
						case 5: // 256 color
							if p == 38 {
								fc = fc.SetColor(Color(params[i+2] & 0xff))
							} else {
								bc = bc.SetColor(Color(params[i+2] & 0xff))
							}
							i += 2
						case 2: // RGB Color
							if i+4 < len(params) {
								if p == 38 {
									fc = fc.SetColorRGB(params[i+2], params[i+3], params[i+4])
								} else {
									bc = bc.SetColorRGB(params[i+2], params[i+3], params[i+4])
								}
								i += 4
							}
						default:
							debugPrintln(debugTodo, "TODO: unhandled extended color: ", params[i+1])
							continue
						}
					}

				case p >= 90 && p <= 97:
					fc = fc.SetColor(Color(p - 90 + 8))

				case p >= 100 && p <= 107:
					bc = bc.SetColor(Color(p - 100 + 8))

				default:
					debugPrintln(debugTodo, "TODO: Unhandled set color: ", p)
					return false
				}

				// debugPrintf("m %v: %x, %x\n", p, fc, bc)

				t.screen().setColors(fc, bc)
			}

		case 't': // Window manipulation
			debugPrintln(debugTodo, "TODO: Unhandled window manipulation: ", params)

		case 'K': // Erase
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// debugPrintln("erase: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to end of line
				t.screen().eraseRegion(Region{
					X:  t.screen().cursorPos.X,
					Y:  t.screen().cursorPos.Y,
					X2: t.screen().size.X,
					Y2: t.screen().cursorPos.Y + 1,
				}, CRClear)
			case params[0] == 1: // Erase to start of line
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  t.screen().cursorPos.Y,
					X2: t.screen().cursorPos.X,
					Y2: t.screen().cursorPos.Y + 1,
				}, CRClear)
			case params[0] == 2: // Erase entire line
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  t.screen().cursorPos.Y,
					X2: t.screen().size.X,
					Y2: t.screen().cursorPos.Y + 1,
				}, CRClear)
			default:
				debugPrintln(debugTodo, "TODO: Unhandled K params: ", params)
				return false
			}

		case 'J': // Erase Lines
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// debugPrintln("erase lines: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to bottom of screen
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  t.screen().cursorPos.Y,
					X2: t.screen().size.X,
					Y2: t.screen().size.Y,
				}, CRClear)
			case params[0] == 1: // Erase to top of screen
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  0,
					X2: t.screen().size.X,
					Y2: t.screen().cursorPos.Y,
				}, CRClear)
			case params[0] == 2: // Erase screen and home cursor
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  0,
					X2: t.screen().size.X,
					Y2: t.screen().size.Y,
				}, CRClear)
				t.screen().setCursorPos(0, 0)
			default:
				debugPrintln(debugTodo, "TODO: Unhandled J params: ", params)
				return false
			}

		case 'L': // Insert lines, scroll down
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().scroll(t.screen().cursorPos.Y, t.screen().bottomMargin, params[0])

		case 'M': // Delete lines, scroll up
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().scroll(t.screen().cursorPos.Y, t.screen().bottomMargin, -params[0])

		case 'S': // Scroll up
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().scroll(t.screen().topMargin, t.screen().bottomMargin, -params[0])

		case 'T': // Scroll down
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().scroll(t.screen().topMargin, t.screen().bottomMargin, params[0])

		case 'P': // Delete n characters
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().eraseRegion(Region{
				X:  t.screen().cursorPos.X,
				Y:  t.screen().cursorPos.Y,
				X2: t.screen().cursorPos.X + params[0],
				Y2: t.screen().cursorPos.Y + 1,
			}, CRClear)
			debugPrintln(debugTodo, "TODO: Delete", params[0], "chars")

		case 'X': // Erase from cursor pos to the right
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen().eraseRegion(Region{
				X:  t.screen().cursorPos.X,
				Y:  t.screen().cursorPos.Y,
				X2: t.screen().cursorPos.X + params[0],
				Y2: t.screen().cursorPos.Y + 1,
			}, CRClear)

		case 'r': // Set Scroll margins
			top := 1
			bottom := t.screen().size.Y
			if len(params) >= 1 {
				top = params[0]
			}
			if len(params) >= 2 {
				bottom = params[1]
			}
			// debugPrintf("cursor home: %v, %v\n", x, y)
			t.screen().setScrollMarginTopBottom(top-1, bottom-1)

		case 'n': // Device Status Report

		default:
			debugPrintf(debugTodo, "TODO: Unhandled CSI Command: %v %#v\n", params, string(b))
			return true
		}
	} else if string(prefix) == "?" {
		switch b {
		case 'h', 'l': // h == set, l == reset  for various modes
			var value bool
			if b == 'h' {
				value = true
			}

			for _, p := range params {
				switch p {
				case 1: // Application / Normal Cursor Keys
					debugPrintln(debugTodo, "TODO: Application Cursor Keys =", value) // TODO

				case 7: // Wraparound
					t.screen().autoWrap = value

				case 9: // Send MouseXY on press
					debugPrintln(debugTodo, "TODO: Send MouseXY on press =", value) // TODO
					if value {
						t.setViewInt(VIMouseMode, MMPress)
					} else {
						t.setViewInt(VIMouseMode, MMNone)
					}

				case 12: // Blink Cursor
					t.setViewFlag(VFBlinkCursor, value)

				case 25: // Show Cursor
					t.setViewFlag(VFShowCursor, value)

				case 1000: // Send MouseXY on press/release
					if value {
						t.setViewInt(VIMouseMode, MMPressRelease)
					} else {
						t.setViewInt(VIMouseMode, MMNone)
					}

				case 1002: // Cell Motion Mouse Tracking
					if value {
						t.setViewInt(VIMouseMode, MMPressReleaseMove)
					} else {
						t.setViewInt(VIMouseMode, MMNone)
					}

				case 1003: // All Motion Mouse Tracking
					if value {
						t.setViewInt(VIMouseMode, MMPressReleaseMoveAll)
					} else {
						t.setViewInt(VIMouseMode, MMNone)
					}

				case 1004: // Report focus changed
					t.setViewFlag(VFReportFocus, value)

				case 1005: // xterm UTF-8 extended mouse reporting
					if value {
						t.setViewInt(VIMouseEncoding, MEUTF8)
					} else {
						t.setViewInt(VIMouseEncoding, MEX10)
					}

				case 1006: // xterm SGR extended mouse reporting
					if value {
						t.setViewInt(VIMouseEncoding, MESGR)
					} else {
						t.setViewInt(VIMouseEncoding, MEX10)
					}

				case 1034:
					debugPrintf(debugTodo, "TODO: Interpret Meta key = %v\n", value)

				case 1049: // Save/Restore cursor and alternate screen
					t.switchScreen()

				case 2004: // Bracketed paste
					t.setViewFlag(VFBracketedPaste, value)

				default:
					debugPrintf(debugTodo, "TODO: Unhandled flag: %#v %v, %v %#v\n", string(prefix), params, p, string(b))
				}
			}

		default:
			debugPrintf(debugTodo, "TODO: Unhandled ? command: %#v %v, %#v\n", string(prefix), params, string(b))
		}
	} else if string(prefix) == ">" {
		switch b {
		case 'c': // Send Device Attributes
			attrs := "\x1b[>1;4402;0c"
			n, err := t.pty.WriteString(attrs)
			if err != nil || n != len(attrs) {
				debugPrintln(debugErrors, "Error sending device attrs:", err)
			}

		default:
			return false
		}
	} else {
		debugPrintf(debugTodo, "TODO: Unhandled prefix: %#v %v, %#v\n", string(prefix), params, string(b))
		return true
	}

	return true
}

func (t *terminal) handleCmdOSC(r *dupReader) bool {
	paramBytes := []byte{}
	var err error
	var b rune
	for {
		b, _, err = r.ReadRune()
		if err != nil {
			debugPrintln(debugErrors, err)
			return false
		}

		if b >= '0' && b <= '9' {
			paramBytes = append(paramBytes, byte(b))
		} else {
			break
		}
	}

	param, _ := strconv.Atoi(string(paramBytes))

	param2 := []byte{}

	if b == ';' {
		// skip the ';'
		for {
			b, _, err = r.ReadRune()
			if err != nil {
				debugPrintln(debugErrors, err)
				return false
			}
			if b == 7 || b == 0x9c { // BEL , ST
				break
			}
			if len(param2) > 0 && param2[len(param2)-1] == 27 && b == '\\' { // ESC \ is also ST
				param2 = param2[:len(param2)-1]
				break
			}

			param2 = append(param2, byte(b))
		}
	} else if b != 7 && b != 0x9c { // BEL, ST
		debugPrintln(debugErrors, "OSC command number not followed by ;, BEL, or ST?", b)
		return false
	}

	switch param {
	case 0:
		t.setViewString(VSWindowTitle, string(param2))

	case 2:
		t.setViewString(VSWindowTitle, string(param2))

	case 4:
		debugPrintf(debugTodo, "TODO: change color : %#v\n", string(param2))

	case 6:
		t.setViewString(VSCurrentDirectory, string(param2))

	case 7:
		t.setViewString(VSCurrentFile, string(param2))

	case 104:
		debugPrintln(debugTodo, "TODO: Reset Color Palette", string(param2))

	case 112:
		debugPrintln(debugTodo, "TODO: Reset Cursor Color", string(param2))

	default:
		debugPrintln(debugTodo, "TODO: Unhandled OSC Command: ", param, string(b))
		return false
	}

	return true
}
