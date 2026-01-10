package termemu

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type dupReader struct {
	reader *bufio.Reader
	t      *terminal
}

func (r *dupReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.teeBytes(p[:n])
	}
	return n, err
}

func (r *dupReader) ReadByte() (byte, error) {
	b, err := r.reader.ReadByte()
	if err == nil {
		r.teeBytes([]byte{b})
	}
	return b, err
}
func (r *dupReader) Buffered() int {
	return r.reader.Buffered()
}

func (r *dupReader) teeBytes(b []byte) {
	if len(b) == 0 {
		return
	}
	if r.t.dup != nil {
		r.t.dup.Write(b)
	}
}

func (t *terminal) ptyReadLoop() {

	r := &dupReader{
		reader: bufio.NewReader(t.pty),
		t:      t,
	}
	gr := NewGraphemeReaderWithMode(r, t.textReadMode)

	for {
		tokens, err := gr.ReadPrintableTokens()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadPrintableTokens:", err)
			}
			return
		}
		if len(tokens) > 0 {
			runes := make([]rune, 0, len(tokens))
			for _, tok := range tokens {
				runes = append(runes, []rune(tok.Text)...)
			}
			if len(runes) > 0 {
				t.WithLock(func() {
					t.screen().writeRunes(runes)
				})
				debugPrintf(debugTxt, "\033[32mtxt: %#v\033[0m %v\n", string(runes), len(runes))
			}
			continue
		}

		b, err := gr.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte:", err)
			}
			return
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

			t.WithLock(func() {
				cmdReader := &captureReader{r: gr, buf: &cmdBytes}
				success := t.handleCommand(cmdReader)
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

		case 127: // DEL  Delete Character (treat as backspace)
			t.WithLock(func() {
				t.screen().moveCursor(-1, 0, false, false)
			})
		default:
			debugPrintf(debugTodo, "TODO: unhandled char %v %#v\n", b, string(b))
			continue
		}
	}
}

type escapeReader interface {
	ReadByte() (byte, error)
	Buffered() int
}

type captureReader struct {
	r   escapeReader
	buf *bytes.Buffer
}

func (c *captureReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil && c.buf != nil {
		c.buf.WriteByte(b)
	}
	return b, err
}

func (c *captureReader) Buffered() int {
	return c.r.Buffered()
}

func (t *terminal) handleCommand(r escapeReader) bool {
	b, err := r.ReadByte()
	if err != nil {
		if err != io.EOF {
			debugPrintln(debugErrors, "ERR ReadByte3:", err)
		}
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

	case 'P': // DCS Device Control String
		return t.handleDCS(r)

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
		C, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte4:", err)
			}
			return false
		}
		_ = C
		return true

	case '=': // Application Keypad
		t.setViewFlag(VFAppKeypad, true)

	case '>': // Normal Keypad
		t.setViewFlag(VFAppKeypad, false)

	case '\\': // ST String Terminator
		return true

	/*case 'l': // Memory Lock
		debugPrintln("Memory Lock") // TODO

	case 'm': // Memory Unlock
		debugPrintln("Memory Unlock") // TODO*/

	default:
		return false
	}
	return true
}

func (t *terminal) handleCmdCSI(r escapeReader) bool {

	b, err := r.ReadByte()
	if err != nil {
		if err != io.EOF {
			debugPrintln(debugErrors, "ERR ReadByte5:", err)
		}
		return false
	}

	var prefix = []byte{}
	if b == '?' || b == '>' || b == '<' || b == '=' {
		prefix = append(prefix, byte(b))

		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte6:", err)
			}
			return false
		}
	}

	paramBytes := []byte{}
	for b == ';' || (b >= '0' && b <= '9') {
		paramBytes = append(paramBytes, byte(b))

		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte7:", err)
			}
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
					fc = ColDefault
					bc = ColDefault

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

				case p == 29: // not crossed-out (ignored)

				case p >= 30 && p <= 37:
					fc = fc.SetColor(Colors8[p-30])

				case p == 39: // default color
					fc = ColDefault

				case p >= 40 && p <= 47:
					bc = bc.SetColor(Colors8[p-40])

				case p == 49: // default color
					bc = ColDefault

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
					continue
				}

				// debugPrintf("m %v: %x, %x\n", p, fc, bc)

				t.screen().setColors(fc, bc)
			}

		case 't': // Window manipulation
			if len(params) > 0 {
				switch params[0] {
				case 22, 23:
					// save/restore window title/icon; no-op for now
					if *debugCmd {
						debugPrintf(debugCmd, "CSI t params: %v\n", params)
					}
					return true
				}
			}
			debugPrintln(debugTodo, "TODO: Window manipulation: ", params)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI t params: %v\n", params)
			}

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
			t.screen().deleteChars(t.screen().cursorPos.X, t.screen().cursorPos.Y, params[0], CRClear)

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
			if len(params) == 0 {
				params = []int{0}
			}
			switch params[0] {
			case 5:
				_, _ = t.Write([]byte("\033[0n"))
			case 6:
				row := t.screen().cursorPos.Y + 1
				col := t.screen().cursorPos.X + 1
				_, _ = t.Write([]byte(fmt.Sprintf("\033[%d;%dR", row, col)))
			default:
				debugPrintln(debugTodo, "TODO: Unhandled DSR params: ", params)
			}

		case '%': // Select character set (ignored)
			// ignored

		case '<': // SGR mouse or other private mode (ignored)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI < params: %v\n", params)
			}

		default:
			debugPrintf(debugTodo, "TODO: Unhandled CSI Command: %v %#v\n", params, string(b))
			return true
		}
	} else if string(prefix) == "?" {
		switch b {
		case 'u': // Query keyboard mode
			flags := t.keyboardFlags()
			_, _ = t.Write([]byte(fmt.Sprintf("\033[?%du", flags)))
			return true
		case 'm': // Private SGR (ignored)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI ? m params: %v\n", params)
			}
			return true
		case 'h', 'l': // h == set, l == reset  for various modes
			var value bool
			if b == 'h' {
				value = true
			}

			for _, p := range params {
				switch p {
				case 1: // Application / Normal Cursor Keys
					t.setViewFlag(VFAppCursorKeys, value)

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

				case 1015: // urxvt mouse mode
					if value {
						t.setViewInt(VIMouseEncoding, MEUTF8)
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

		case 'm': // modifyOtherKeys
			mode := -1
			for i := 0; i < len(params); i++ {
				if params[i] == 4 {
					if i+1 < len(params) {
						mode = params[i+1]
					} else {
						mode = 0
					}
				}
			}
			if mode >= 0 {
				t.setViewInt(VIModifyOtherKeys, mode)
			}
			if *debugCmd {
				debugPrintf(debugCmd, "CSI > m params: %v mode=%d\n", params, mode)
			}

		case 'u': // key encoding mode (ignored)
			flags := 0
			if len(params) > 0 {
				flags = params[0]
			}
			t.pushKeyboardFlags(flags)

		default:
			debugPrintf(debugTodo, "TODO: Unhandled > command: %#v %v, %#v\n", string(prefix), params, string(b))
			return true
		}
	} else if string(prefix) == "<" {
		switch b {
		case 'u': // key encoding mode pop
			count := 1
			if len(params) > 0 {
				count = params[0]
			}
			t.popKeyboardFlags(count)
			return true
		case 'M', 'm': // SGR mouse report (ignored)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI < mouse params: %v %c\n", params, b)
			}
			return true
		default:
			debugPrintf(debugTodo, "TODO: Unhandled < command: %#v %v, %#v\n", string(prefix), params, string(b))
			return true
		}
	} else if string(prefix) == "=" {
		switch b {
		case 'u': // key encoding mode set
			flags := 0
			mode := 1
			if len(params) > 0 {
				flags = params[0]
			}
			if len(params) > 1 {
				mode = params[1]
			}
			t.updateKeyboardFlags(flags, mode)
			return true
		default:
			debugPrintf(debugTodo, "TODO: Unhandled = command: %#v %v, %#v\n", string(prefix), params, string(b))
			return true
		}
	} else {
		debugPrintf(debugTodo, "TODO: Unhandled prefix: %#v %v, %#v\n", string(prefix), params, string(b))
		return true
	}

	return true
}

func (t *terminal) handleCmdOSC(r escapeReader) bool {
	paramBytes := []byte{}
	var err error
	var b byte
	for {
		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte8:", err)
			}
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
			b, err = r.ReadByte()
			if err != nil {
				if err != io.EOF {
					debugPrintln(debugErrors, "ERR ReadByte9:", err)
				}
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

	case 10:
		debugPrintln(debugTodo, "TODO: OSC foreground color: ", string(param2))

	case 11:
		debugPrintln(debugTodo, "TODO: OSC background color: ", string(param2))

	case 104:
		debugPrintln(debugTodo, "TODO: Reset Color Palette", string(param2))

	case 112:
		debugPrintln(debugTodo, "TODO: Reset Cursor Color", string(param2))

	default:
		debugPrintln(debugTodo, "TODO: Unhandled OSC Command: ", param, string(b))
		return true
	}

	if *debugCmd {
		debugPrintf(debugCmd, "OSC %d: %q\n", param, string(param2))
	}

	return true
}

func (t *terminal) handleDCS(r escapeReader) bool {
	prev := byte(0)
	var payload []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteDCS:", err)
			}
			return false
		}
		if b == 0x9c {
			if *debugCmd {
				debugPrintf(debugCmd, "DCS payload: %q\n", string(payload))
			}
			return true
		}
		if prev == 27 && b == '\\' {
			if *debugCmd {
				if len(payload) > 0 && payload[len(payload)-1] == 27 {
					payload = payload[:len(payload)-1]
				}
				debugPrintf(debugCmd, "DCS payload: %q\n", string(payload))
			}
			return true
		}
		if *debugCmd {
			payload = append(payload, b)
		}
		prev = b
	}
}
