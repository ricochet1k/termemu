package termemu

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Version is the emulator version reported by XTVERSION (CSI > q).
const Version = "0.1"

// colorModes maps SGR codes 1-7 to their Mode values
var colorModes = [7]Mode{
	ModeBold,      // SGR 1
	ModeDim,       // SGR 2
	ModeItalic,    // SGR 3
	ModeUnderline, // SGR 4
	ModeBlink,     // SGR 5
	ModeReverse,   // SGR 7 (skipping 6 which is RapidBlink in BG)
	ModeInvisible, // SGR 8
}

// writeReply writes an escape-handler reply directly to the backend without
// taking the terminal lock. ptyReadOne calls handleCommand with the lock held,
// so reply handlers must not use the public, locking Write method (that would
// self-deadlock). The caller must hold t.Lock.
func (t *terminal) writeReply(b []byte) error {
	backend := t.backend
	if backend == nil {
		return errors.New("backend is nil")
	}
	for len(b) > 0 {
		n, err := backend.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

// ptyReadLoop consumes backend output until a read fails, then reports the
// terminal error: the first read error encountered. The caller normalizes
// io.EOF to nil (clean shutdown).
func (t *terminal) ptyReadLoop() error {
	reader := bufio.NewReader(t.backend)
	gr := NewGraphemeReaderWithMode(reader, t.textReadMode)

	for {
		if err := t.ptyReadOne(gr); err != nil {
			return err
		}
	}
}

func (t *terminal) ptyReadOne(gr *GraphemeReader) error {
	bw, useBytes := t.screen().(interface {
		writeString(string, int, bool, TextReadMode)
	})
	if useBytes {
		maxWidth := 0
		t.withLock(func() {
			if t.screen().AutoWrap() {
				maxWidth = t.screen().Size().X - t.screen().CursorPos().X
				if maxWidth < 1 {
					maxWidth = 1
				}
			}
		})
		data, width, merge, err := gr.ReadPrintableBytes(maxWidth)
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadPrintableBytes:", err)
			}
			return err
		}
		if len(data) > 0 {
			t.withLock(func() {
				bw.writeString(data, width, merge, t.textReadMode)
			})
			if *debugTxt {
				debugPrintf(debugTxt, "\033[32mtxt: %#v\033[0m %v\n", data, len(data))
			}
			return nil
		}
	} else {
		tokens, err := gr.ReadPrintableTokens(0)
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadPrintableTokens:", err)
			}
			return err
		}
		if len(tokens) > 0 {
			t.withLock(func() {
				t.screen().writeTokens(tokens)
			})
			if *debugTxt {
				var buf bytes.Buffer
				for _, tok := range tokens {
					buf.Write(tok.Bytes)
				}
				debugPrintf(debugTxt, "\033[32mtxt: %#v\033[0m %v\n", buf.String(), buf.Len())
			}
			return nil
		}
	}

	b, err := gr.ReadByte()
	if err != nil {
		if err != io.EOF {
			debugPrintln(debugErrors, "ERR ReadByte:", err)
		}
		return err
	}

	// ABCDEFGHIJKLMNOPQRSTUVWXYZ

	switch b {
	case 0: // NUL Null byte, ignore

	case 5: // ENQ ^E Return Terminal Status
		debugPrintln(debugTodo, "TODO: ENQ")
	case 7: // BEL ^G Bell
		t.withLock(func() {
			t.frontend.Bell(LockedView{t: t})
		})

	case 8: // BS ^H Backspace
		t.withLock(func() {
			t.screen().moveCursor(-1, 0, false, false)
		})

	case 9: // HT ^I Horizontal TAB
		t.withLock(func() {
			cursorPos := t.screen().CursorPos()
			cursorPos.X = ((cursorPos.X >> 3) + 1) << 3
			t.screen().setCursorPos(cursorPos.X, cursorPos.Y)
		})

	case 10: // LF ^J Linefeed (newline)
		t.withLock(func() {
			cursorPos := t.screen().CursorPos()
			t.screen().setCursorPos(0, cursorPos.Y)
			t.screen().moveCursor(0, 1, true, true)
		})

	case 11: // VT ^K Vertical TAB
		debugPrintln(debugTodo, "TODO: vtab")

	case 12: // FF ^L Formfeed (also: New page NP)
		t.withLock(func() {
			t.screen().moveCursor(0, 1, false, true)
		})

	case 13: // CR ^M Carriage Return
		t.withLock(func() {
			t.screen().moveCursor(-t.screen().CursorPos().X, 0, true, true)
		})

	case 27: // ESC ^[ Escape Character
		// Read the whole command before taking the lock: handleCommand
		// parses from memory, so a child that stops mid-sequence cannot
		// block Snapshot/Restore (which need the lock) behind a pending
		// ReadByte on the backend.
		cmd, err := readEscapeCommand(gr)
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR readEscapeCommand:", err)
			}
			return err
		}

		t.withLock(func() {
			success := t.handleCommand(bytes.NewReader(cmd))
			if *debugCmd || *debugTodo {
				if success {
					debugPrintf(debugCmd, "%v cmd: %#v\n", t.screen().CursorPos(), string(cmd))
				} else {
					debugPrintf(debugTodo, "TODO: Unhandled command: %#v\n", string(cmd))
				}
			}
		})

	case 127: // DEL  Delete Character (treat as backspace)
		t.withLock(func() {
			t.screen().moveCursor(-1, 0, false, false)
		})
	default:
		debugPrintf(debugTodo, "TODO: unhandled char %v %#v\n", b, string(b))
	}
	return nil
}

type escapeReader interface {
	ReadByte() (byte, error)
}

// readEscapeCommand reads one complete ESC-initiated command from r, whose
// leading ESC has already been consumed. It returns the command bytes (not
// including the ESC) so the caller can dispatch them under the terminal lock
// from an in-memory reader instead of blocking on backend I/O while locked.
//
// CSI commands end at a final byte in 0x40-0x7E. OSC, DCS, APC, PM and SOS
// strings end at ST (ESC \\ or 0x9C) or BEL, honoring UTF-8 continuation bytes.
// Two-byte commands consume their argument, and everything else is a single
// command byte. The bytes are buffered even when debugging is off; the debug
// capture uses the returned slice directly.
func readEscapeCommand(r escapeReader) ([]byte, error) {
	var buf []byte
	read := func() (byte, error) {
		b, err := r.ReadByte()
		if err == nil {
			buf = append(buf, b)
		}
		return b, err
	}

	b, err := read()
	if err != nil {
		return nil, err
	}

	switch b {
	case '[': // CSI: parameter/intermediate bytes then a final byte
		for {
			b, err := read()
			if err != nil {
				return buf, err
			}
			if b >= 0x40 && b <= 0x7e {
				return buf, nil
			}
			if b < 0x20 || b > 0x3f {
				// Malformed (not a parameter, intermediate, or final
				// byte): stop here and let the handler treat it as the
				// final byte instead of swallowing following text.
				return buf, nil
			}
		}

	case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS
		var scanner stringTermScanner
		for {
			b, err := read()
			if err != nil {
				return buf, err
			}
			if b == 0x07 { // BEL
				return buf, nil
			}
			if scanner.isST(b) {
				return buf, nil
			}
		}

	case '%', ' ', '-', '.', '/', '(', ')', '*', '+', '#':
		// Two-byte commands with one argument byte.
		if _, err := read(); err != nil {
			return buf, err
		}
	}

	return buf, nil
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

	case '7': // DECSC: save cursor position (same as CSI s)
		t.screen().saveCursorPos()

	case '8': // DECRC: restore cursor position (same as CSI u)
		t.screen().restoreCursorPos()

	case 'c': // RIS: hard reset screen state
		t.resetTerminal()

	case '_', '^', 'X': // APC, PM, SOS: consume string until ST or BEL
		return t.consumeString(r)

	case '%', ' ', '-', '.', '/': // Two-byte sequences: consume the argument byte
		if _, err := r.ReadByte(); err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteESCarg:", err)
			}
			return false
		}

	case '#':
		arg, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteESCHash:", err)
			}
			return false
		}
		if arg == '8' { // DECALN: fill screen with E
			t.screen().decaln()
		}

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

// handleDECRQM implements DECRQM (CSI ? Ps $ p / CSI Ps $ p), reporting the
// current mode state: 1 = set, 2 = reset, 0 = not recognized.
func (t *terminal) handleDECRQM(prefix byte, params []int) {
	if prefix != 0 && prefix != '?' {
		debugPrintf(debugTodo, "TODO: Unhandled DECRQM prefix: %#v\n", string(prefix))
		return
	}
	ps := 0
	if len(params) > 0 {
		ps = params[0]
	}
	pm := 0
	prefixStr := ""
	if prefix == '?' {
		prefixStr = "?"
		pm = t.privateModeState(ps)
	}
	_ = t.writeReply([]byte(fmt.Sprintf("\033[%s%d;%d$y", prefixStr, ps, pm)))
}

// privateModeState reports the state of a DEC private mode: 1 set, 2 reset,
// or 0 when the mode is not recognized/supported.
func (t *terminal) privateModeState(mode int) int {
	set := false
	switch mode {
	case 1: // DECCKM
		set = t.viewFlags[VFAppCursorKeys]
	case 7: // DECAWM
		set = t.screen().AutoWrap()
	case 9: // Send MouseXY on press
		set = t.viewInts[VIMouseMode] == MMPress
	case 12: // Cursor blink
		set = t.viewFlags[VFBlinkCursor]
	case 25: // Cursor visible
		set = t.viewFlags[VFShowCursor]
	case 47, 1047, 1048, 1049: // Alt-screen family
		set = t.onAltScreen
	case 1000:
		set = t.viewInts[VIMouseMode] == MMPressRelease
	case 1002:
		set = t.viewInts[VIMouseMode] == MMPressReleaseMove
	case 1003:
		set = t.viewInts[VIMouseMode] == MMPressReleaseMoveAll
	case 1004: // Focus reporting
		set = t.viewFlags[VFReportFocus]
	case 1005: // UTF-8 mouse encoding
		set = t.viewInts[VIMouseEncoding] == MEUTF8
	case 1006: // SGR mouse encoding
		set = t.viewInts[VIMouseEncoding] == MESGR
	case 1015: // urxvt mouse encoding (shares the UTF-8 encoding state)
		set = t.viewInts[VIMouseEncoding] == MEUTF8
	case 2004: // Bracketed paste
		set = t.viewFlags[VFBracketedPaste]
	case 2026: // Synchronized output
		set = t.viewFlags[VFSynchronizedOutput]
	default:
		return 0
	}
	if set {
		return 1
	}
	return 2
}

// handleDECSTR implements DECSTR (CSI ! p): soft terminal reset. It restores
// default style, full scroll margins, auto-wrap, and cursor visibility without
// touching screen contents.
func (t *terminal) handleDECSTR() {
	for _, s := range []screen{t.mainScreen, t.altScreen} {
		s.setStyle(NewStyle())
		s.setScrollMarginTopBottom(0, s.Size().Y-1)
		s.SetAutoWrap(true)
	}
	t.setViewFlag(VFShowCursor, true)
}

func (t *terminal) handleCmdCSI(r escapeReader) bool {

	b, err := r.ReadByte()
	if err != nil {
		if err != io.EOF {
			debugPrintln(debugErrors, "ERR ReadByte5:", err)
		}
		return false
	}

	prefix := byte(0)
	if b == '?' || b == '>' || b == '<' || b == '=' {
		prefix = b
		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte6:", err)
			}
			return false
		}
	}

	// This cannot escape to the heap! Use append([]int(nil), params...) instead of params to make a copy if needed, such as to debug calls
	var paramStore [8]int
	paramCount := 0
	param := 0
	paramSet := false
	sawSeparator := false
	for b == ';' || (b >= '0' && b <= '9') {
		if b == ';' {
			value := 0
			if paramSet || sawSeparator || paramCount > 0 {
				value = param
			}
			if paramCount < len(paramStore) {
				paramStore[paramCount] = value
				paramCount++
			}
			param = 0
			paramSet = false
			sawSeparator = true
		} else {
			param = param*10 + int(b-'0')
			paramSet = true
			sawSeparator = false
		}

		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte7:", err)
			}
			return false
		}
	}
	if paramSet || sawSeparator {
		value := 0
		if paramSet {
			value = param
		}
		if paramCount < len(paramStore) {
			paramStore[paramCount] = value
			paramCount++
		}
	}

	params := paramStore[:paramCount]

	// CSI grammar allows intermediate bytes (0x20-0x2F) between the
	// parameters and the final byte. Collect them so the final byte is not
	// mistaken for printable text.
	var interStore [4]byte
	interCount := 0
	for b >= 0x20 && b <= 0x2f {
		if interCount < len(interStore) {
			interStore[interCount] = b
			interCount++
		}
		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte7:", err)
			}
			return false
		}
	}
	intermediates := interStore[:interCount]

	if len(intermediates) > 0 {
		switch {
		case len(intermediates) == 1 && intermediates[0] == '$' && b == 'p':
			// DECRQM: request mode
			t.handleDECRQM(prefix, params)
		case len(intermediates) == 1 && intermediates[0] == '!' && b == 'p':
			// DECSTR: soft terminal reset
			t.handleDECSTR()
		default:
			debugPrintf(debugTodo, "TODO: Unhandled CSI intermediates: %#v %v, %#v\n", string(intermediates), append([]int(nil), params...), string(b))
			return true
		}
		return true
	}

	if prefix == 0 {
		switch b {
		case 'A': // Move cursor up
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().moveCursor(0, -params[0], false, false)
		case 'B': // Move cursor down
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().moveCursor(0, params[0], false, true)
		case 'C': // Move cursor forward
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().moveCursor(params[0], 0, false, false)
		case 'D': // Move cursor backward
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().moveCursor(-params[0], 0, false, false)

		case 'G': // Cursor Character Absolute
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().setCursorPos(params[0]-1, t.screen().CursorPos().Y)

		case 'c': // Send Device Attributes
			if paramCount == 0 {
				paramStore[0] = 0
				paramCount = 1
				params = paramStore[:paramCount]
			}
			switch params[0] {
			case 0:
				_ = t.writeReply([]byte("\033[?1;2c"))
			}

		case 'd': // Line Position Absolute
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().setCursorPos(t.screen().CursorPos().X, params[0]-1)

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
				debugPrintln(debugTodo, "TODO: Unhandled CSI mode params: ", append([]int(nil), params...), b)
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
			if paramCount == 0 {
				paramStore[0] = 0
				paramCount = 1
				params = paramStore[:paramCount]
			}

			style := t.screen().Style()

			for i := 0; i < len(params); i++ {
				p := params[i]
				switch {
				case p == 0: // reset
					style.Reset()

				case p >= 1 && p <= 5:
					style.SetMode(colorModes[p-1])

				case p == 6: // rapid blink
					style.SetMode(ModeRapidBlink)

				case p == 7:
					style.SetMode(ModeReverse)

				case p == 8:
					style.SetMode(ModeInvisible)

				case p == 9: // strikethrough / crossed-out
					style.SetMode(ModeStrike)

				case p == 21: // double underline
					style.SetMode(ModeDoubleUnderline)

				case p == 22:
					style.Reset(ModeBold, ModeDim)

				case p == 23:
					style.Reset(ModeItalic)

				case p == 24:
					style.Reset(ModeUnderline, ModeDoubleUnderline)

				case p == 25:
					style.Reset(ModeBlink, ModeRapidBlink)

				case p == 27:
					style.Reset(ModeReverse)

				case p == 28:
					style.Reset(ModeInvisible)

				case p == 29: // not crossed-out
					style.Reset(ModeStrike)

				case p == 51: // framed
					style.SetMode(ModeFramed)

				case p == 52: // encircled
					style.SetMode(ModeEncircled)

				case p == 53: // overline
					style.SetMode(ModeOverline)

				case p == 54: // not framed, not encircled
					style.Reset(ModeFramed, ModeEncircled)

				case p == 55: // not overline
					style.Reset(ModeOverline)

				case p >= 30 && p <= 37:
					_ = style.SetColor(ComponentFG, Color256(p-30))

				case p == 39: // default color
					_ = style.SetColor(ComponentFG, DefaultColor())

				case p >= 40 && p <= 47:
					_ = style.SetColor(ComponentBG, Color256(p-40))

				case p == 49: // default color
					_ = style.SetColor(ComponentBG, DefaultColor())

				case p == 38 || p == 48: // extended set color
					if i+2 < len(params) {
						switch params[i+1] {
						case 5: // 256 color
							component := ComponentFG
							if p == 48 {
								component = ComponentBG
							}
							_ = style.SetColor(component, Color256(params[i+2]&0xff))
							i += 2
						case 2: // RGB Color
							if i+4 < len(params) {
								component := ComponentFG
								if p == 48 {
									component = ComponentBG
								}
								_ = style.SetColor(component, RGB(params[i+2], params[i+3], params[i+4]))
								i += 4
							}
						default:
							debugPrintln(debugTodo, "TODO: unhandled extended color: ", params[i+1])
							continue
						}
					}

				case p >= 90 && p <= 97:
					_ = style.SetColor(ComponentFG, BrightColor(int(p-90)))

				case p >= 100 && p <= 107:
					_ = style.SetColor(ComponentBG, BrightColor(int(p-100)))

				default:
					debugPrintln(debugTodo, "TODO: Unhandled set color: ", p)
					continue
				}
			}

			t.screen().setStyle(style)

		case 's': // Save cursor pos
			t.screen().saveCursorPos()

		case 'u': // Restore cursor pos
			t.screen().restoreCursorPos()

		case 't': // Window manipulation
			if len(params) > 0 {
				switch params[0] {
				case 22, 23:
					// save/restore window title/icon; no-op for now
					if *debugCmd {
						debugPrintf(debugCmd, "CSI t params: %v\n", append([]int(nil), params...))
					}
					return true
				}
			}
			debugPrintln(debugTodo, "TODO: Window manipulation: ", append([]int(nil), params...))
			if *debugCmd {
				debugPrintf(debugCmd, "CSI t params: %v\n", append([]int(nil), params...))
			}

		case 'K': // Erase
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// debugPrintln("erase: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to end of line
				t.screen().eraseRegion(Region{
					X:  t.screen().CursorPos().X,
					Y:  t.screen().CursorPos().Y,
					X2: t.screen().Size().X,
					Y2: t.screen().CursorPos().Y + 1,
				}, CRClear)
			case params[0] == 1: // Erase to start of line
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  t.screen().CursorPos().Y,
					X2: t.screen().CursorPos().X,
					Y2: t.screen().CursorPos().Y + 1,
				}, CRClear)
			case params[0] == 2: // Erase entire line
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  t.screen().CursorPos().Y,
					X2: t.screen().Size().X,
					Y2: t.screen().CursorPos().Y + 1,
				}, CRClear)
			default:
				debugPrintln(debugTodo, "TODO: Unhandled K params: ", append([]int(nil), params...))
				return false
			}

		case 'J': // Erase Lines
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// debugPrintln("erase lines: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to bottom of screen
				cursorPos := t.screen().CursorPos()
				// Erase from cursor to end of current line
				t.screen().eraseRegion(Region{
					X:  cursorPos.X,
					Y:  cursorPos.Y,
					X2: t.screen().Size().X,
					Y2: cursorPos.Y + 1,
				}, CRClear)
				// Erase all lines below current line
				if cursorPos.Y+1 < t.screen().Size().Y {
					t.screen().eraseRegion(Region{
						X:  0,
						Y:  cursorPos.Y + 1,
						X2: t.screen().Size().X,
						Y2: t.screen().Size().Y,
					}, CRClear)
				}
			case params[0] == 1: // Erase to top of screen
				cursorPos := t.screen().CursorPos()
				// Erase all lines above current line
				if cursorPos.Y > 0 {
					t.screen().eraseRegion(Region{
						X:  0,
						Y:  0,
						X2: t.screen().Size().X,
						Y2: cursorPos.Y,
					}, CRClear)
				}
				// Erase from beginning of current line to cursor (inclusive)
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  cursorPos.Y,
					X2: cursorPos.X + 1,
					Y2: cursorPos.Y + 1,
				}, CRClear)
			case params[0] == 2: // Erase screen and home cursor
				t.screen().eraseRegion(Region{
					X:  0,
					Y:  0,
					X2: t.screen().Size().X,
					Y2: t.screen().Size().Y,
				}, CRClear)
				t.screen().setCursorPos(0, 0)
			case params[0] == 3: // Erase scrollback (xterm ED extension).
				// Clears only the scrollback buffer; the visible screen
				// and cursor are left untouched. Lock is already held
				// here, so use the locked helper (Scrollback.Clear
				// would deadlock).
				t.clearScrollbackLocked()
			default:
				// debugPrintln(debugTodo, "TODO: Unhandled J params: ", params)
				return false
			}

		case 'L': // Insert lines, scroll down
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().scroll(t.screen().CursorPos().Y, t.screen().BottomMargin(), params[0])

		case 'M': // Delete lines, scroll up
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().scroll(t.screen().CursorPos().Y, t.screen().BottomMargin(), -params[0])

		case 'S': // Scroll up
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().scroll(t.screen().TopMargin(), t.screen().BottomMargin(), -params[0])

		case 'T': // Scroll down
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().scroll(t.screen().TopMargin(), t.screen().BottomMargin(), params[0])

		case 'P': // Delete n characters
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().deleteChars(t.screen().CursorPos().X, t.screen().CursorPos().Y, params[0], CRClear)

		case 'X': // Erase from cursor pos to the right
			if paramCount == 0 {
				paramStore[0] = 1
				paramCount = 1
				params = paramStore[:paramCount]
			}
			t.screen().eraseRegion(Region{
				X:  t.screen().CursorPos().X,
				Y:  t.screen().CursorPos().Y,
				X2: t.screen().CursorPos().X + params[0],
				Y2: t.screen().CursorPos().Y + 1,
			}, CRClear)

		case 'r': // Set Scroll margins
			top := 1
			bottom := t.screen().Size().Y
			if len(params) >= 1 {
				top = params[0]
			}
			if len(params) >= 2 {
				bottom = params[1]
			}
			// debugPrintf("cursor home: %v, %v\n", x, y)
			t.screen().setScrollMarginTopBottom(top-1, bottom-1)

		case 'n': // Device Status Report
			if paramCount == 0 {
				paramStore[0] = 0
				paramCount = 1
				params = paramStore[:paramCount]
			}
			switch params[0] {
			case 5:
				_ = t.writeReply([]byte("\033[0n"))
			case 6:
				row := t.screen().CursorPos().Y + 1
				col := t.screen().CursorPos().X + 1
				_ = t.writeReply([]byte(fmt.Sprintf("\033[%d;%dR", row, col)))
			default:
				debugPrintln(debugTodo, "TODO: Unhandled DSR params: ", append([]int(nil), params...))
			}

		case '<': // SGR mouse or other private mode (ignored)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI < params: %v\n", append([]int(nil), params...))
			}

		default:
			debugPrintf(debugTodo, "TODO: Unhandled CSI Command: %v %#v\n", append([]int(nil), params...), string(b))
			return true
		}
	} else if string(prefix) == "?" {
		switch b {
		case 'u': // Query keyboard mode
			flags := t.keyboardFlags()
			_ = t.writeReply([]byte(fmt.Sprintf("\033[?%du", flags)))
			return true
		case 'm': // Private SGR (ignored)
			if *debugCmd {
				debugPrintf(debugCmd, "CSI ? m params: %v\n", append([]int(nil), params...))
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
					t.screen().SetAutoWrap(value)

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

				case 47, 1047: // Alternate screen without clear or cursor save/restore
					t.setAltScreen(value)

				case 1048: // Save/restore cursor only, no screen switch
					if value {
						t.screen().saveCursorPos()
					} else {
						t.screen().restoreCursorPos()
					}

				case 1049: // Save cursor, clear alt screen, switch; restore on reset
					if value {
						if !t.onAltScreen {
							t.mainScreen.saveCursorPos()
							t.enterAltScreen(true)
						}
					} else {
						if t.onAltScreen {
							t.exitAltScreen()
							t.mainScreen.restoreCursorPos()
						}
					}

				case 2004: // Bracketed paste
					t.setViewFlag(VFBracketedPaste, value)

				case 2026: // Synchronized output: expose only, no internal batching
					t.setViewFlag(VFSynchronizedOutput, value)

				default:
					debugPrintf(debugTodo, "TODO: Unhandled flag: %#v %v, %v %#v\n", string(prefix), append([]int(nil), params...), p, string(b))
				}
			}

		default:
			debugPrintf(debugTodo, "TODO: Unhandled ? command: %#v %v, %#v\n", string(prefix), append([]int(nil), params...), string(b))
		}
	} else if string(prefix) == ">" {
		switch b {
		case 'c': // Send Device Attributes
			attrs := "\x1b[>1;4402;0c"
			if err := t.writeReply([]byte(attrs)); err != nil {
				debugPrintln(debugErrors, "Error sending device attrs:", err)
			}

		case 'q': // XTVERSION: DCS > | name(version) ST
			if len(params) == 0 || (len(params) == 1 && params[0] == 0) {
				_ = t.writeReply([]byte(fmt.Sprintf("\x1bP>|termemu(%s)\x1b\\", Version)))
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
				debugPrintf(debugCmd, "CSI > m params: %v mode=%d\n", append([]int(nil), params...), mode)
			}

		case 'u': // key encoding mode (ignored)
			flags := 0
			if len(params) > 0 {
				flags = params[0]
			}
			t.pushKeyboardFlags(flags)

		default:
			debugPrintf(debugTodo, "TODO: Unhandled > command: %#v %v, %#v\n", string(prefix), append([]int(nil), params...), string(b))
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
				debugPrintf(debugCmd, "CSI < mouse params: %v %c\n", append([]int(nil), params...), b)
			}
			return true
		default:
			debugPrintf(debugTodo, "TODO: Unhandled < command: %#v %v, %#v\n", string(prefix), append([]int(nil), params...), string(b))
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
			debugPrintf(debugTodo, "TODO: Unhandled = command: %#v %v, %#v\n", string(prefix), append([]int(nil), params...), string(b))
			return true
		}
	} else {
		debugPrintf(debugTodo, "TODO: Unhandled prefix: %#v %v, %#v\n", string(prefix), append([]int(nil), params...), string(b))
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
		var scanner stringTermScanner
		for {
			b, err = r.ReadByte()
			if err != nil {
				if err != io.EOF {
					debugPrintln(debugErrors, "ERR ReadByte9:", err)
				}
				return false
			}
			if b == 7 { // BEL
				break
			}
			if scanner.isST(b) {
				// ESC \ is ST; drop the ESC we appended as payload.
				if len(param2) > 0 && b == '\\' && param2[len(param2)-1] == 27 {
					param2 = param2[:len(param2)-1]
				}
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
		// Legacy nonstandard working-directory announcement; OSC 7 is the
		// standard form. Both map to VSCurrentDirectory.
		t.setViewString(VSCurrentDirectory, string(param2))

	case 7:
		// Standard working-directory URI advertised by shells (e.g.
		// "file://host/path"). This previously (incorrectly) set
		// VSCurrentFile; an OSC 7 payload alone never means a file, so it
		// now sets VSCurrentDirectory. No OSC sequence sets VSCurrentFile.
		t.setViewString(VSCurrentDirectory, string(param2))

	case 8:
		t.handleOSC8(param2)

	case 52:
		t.handleOSC52(param2)

	case 10:
		debugPrintln(debugTodo, "TODO: OSC foreground color: ", string(param2))

	case 11:
		debugPrintln(debugTodo, "TODO: OSC background color: ", string(param2))

	case 104:
		debugPrintln(debugTodo, "TODO: Reset Color Palette", string(param2))

	case 112:
		debugPrintln(debugTodo, "TODO: Reset Cursor Color", string(param2))

	case 7501:
		// Program Status Protocol feature detection.
		// https://www.superlogical.com/rex/docs/build/program-status
		// Only the query is supported; status reports are ignored for now.
		if strings.TrimSpace(string(param2)) == "?" {
			_ = t.writeReply([]byte("\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\"))
		} else {
			debugPrintln(debugTodo, "TODO: ignoring OSC 7501 report: ", string(param2))
		}

	default:
		debugPrintln(debugTodo, "TODO: Unhandled OSC Command: ", param, string(b))
		return true
	}

	if *debugCmd {
		debugPrintf(debugCmd, "OSC %d: %q\n", param, string(param2))
	}

	return true
}

// handleOSC8 implements OSC 8 hyperlinks. The payload is "params;URI": a
// non-empty URI starts (or replaces) the current link, an empty URI ends
// it. params (e.g. "id=...") is accepted and ignored; only the URI is
// tracked. Content written while a link is active carries it in Span.Link.
// Caller must hold t.Lock.
func (t *terminal) handleOSC8(payload []byte) {
	s := string(payload)
	if s == "" {
		t.setHyperlink("")
		return
	}
	uri := s
	if i := strings.IndexByte(s, ';'); i >= 0 {
		uri = s[i+1:]
	} else {
		// Malformed: no params separator. Ignore without changing state.
		debugPrintln(debugTodo, "TODO: malformed OSC 8 (missing params separator): ", s)
		return
	}
	t.setHyperlink(uri)
}

// handleOSC52 implements the OSC 52 clipboard write path. The payload is
// "<selection>;<base64-data>" (selection is usually "c"). If the frontend
// implements ClipboardSink, the decoded bytes are forwarded to it; called
// with the terminal lock held, so the sink must not call back into the
// terminal. Clipboard queries (data == "?") are never answered or
// forwarded, and malformed base64 is ignored.
// Caller must hold t.Lock.
func (t *terminal) handleOSC52(payload []byte) {
	sel, data, ok := strings.Cut(string(payload), ";")
	if !ok {
		debugPrintln(debugTodo, "TODO: malformed OSC 52 (missing selection separator)")
		return
	}
	if data == "?" {
		// Clipboard query: do not answer or forward (security).
		debugPrintln(debugTodo, "TODO: ignoring OSC 52 clipboard query")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		debugPrintln(debugTodo, "TODO: ignoring OSC 52 with bad base64: ", err)
		return
	}
	if sink, ok := t.frontend.(ClipboardSink); ok {
		sink.Clipboard(LockedView{t: t}, sel, raw)
	}
}

// stringTermScanner detects a string terminator while respecting UTF-8
// continuation bytes: 0x9C is a valid continuation byte, so it only acts as
// an 8-bit ST when it is not consumed by a preceding UTF-8 lead byte.
type stringTermScanner struct {
	prev byte
	cont int
}

// isST reports whether b terminates the string. Feed it one payload byte at a
// time; it must see the ESC of an ESC \ terminator before the backslash.
func (s *stringTermScanner) isST(b byte) bool {
	if s.prev == 0x1b && b == '\\' { // 7-bit ST
		s.prev = b
		return true
	}
	if s.cont > 0 {
		s.cont--
		s.prev = b
		return false
	}
	if b == 0x9c { // 8-bit ST, not part of a UTF-8 sequence
		s.prev = b
		return true
	}
	switch {
	case b >= 0xc2 && b <= 0xdf:
		s.cont = 1
	case b >= 0xe0 && b <= 0xef:
		s.cont = 2
	case b >= 0xf0 && b <= 0xf4:
		s.cont = 3
	}
	s.prev = b
	return false
}

// consumeString discards an ESC-initiated string (APC, PM, SOS) up to its
// terminator: ST (ESC \ or 0x9C) or BEL. Payload bytes are never printed.
func (t *terminal) consumeString(r escapeReader) bool {
	var scanner stringTermScanner
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteString:", err)
			}
			return false
		}
		if b == 0x07 { // BEL
			return true
		}
		if scanner.isST(b) {
			return true
		}
	}
}

func (t *terminal) handleDCS(r escapeReader) bool {
	var scanner stringTermScanner
	var payload []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteDCS:", err)
			}
			return false
		}
		if scanner.isST(b) {
			if *debugCmd {
				if len(payload) > 0 && b == '\\' && payload[len(payload)-1] == 27 {
					payload = payload[:len(payload)-1]
				}
				debugPrintf(debugCmd, "DCS payload: %q\n", string(payload))
			}
			return true
		}
		if *debugCmd {
			payload = append(payload, b)
		}
	}
}
