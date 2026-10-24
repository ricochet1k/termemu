package termemu

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Version is the emulator version reported by XTVERSION (CSI > q).
const Version = "0.1"

// maxEscapeCommandBytes bounds the user-space buffer for a single ESC-initiated
// command (CSI/OSC/DCS/APC/etc.). Longer sequences are consumed and discarded
// rather than growing memory without bound.
const maxEscapeCommandBytes = 64 << 10

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
// io.EOF to nil (clean shutdown). It starts from a previously captured reader
// state so a paused loop (or a new process) can resume mid-command.
func (t *terminal) ptyReadLoop(initial ReaderState) error {
	state := newReadLoopState(t.backend, initial)

	for {
		if t.shouldPause() {
			// Never leave a prepared frame pending while parking.
			t.flushFrontend()
			if !t.pauseLoop(state.snapshot()) {
				return nil
			}
		}

		var err error
		if len(state.pendingCmd) > 0 {
			err = t.continueEscapeCommand(state)
		} else {
			err = t.ptyReadOne(state)
		}
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if t.isPaused() {
					// A deadline fired to wake the loop for Pause. The
					// partial state is already recorded; park at the top.
					continue
				}
				// A cancelled Pause may have cleared the pause flag
				// just before the blocked read observed the deadline.
				// Clearing the deadline and retrying keeps the loop
				// alive instead of exiting on our own wakeup.
				t.clearReadDeadline()
				continue
			}
			t.flushFrontend()
			return err
		}
		// About to block on the backend: this is the batch boundary for
		// frontends that coalesce damage. When more input is already
		// buffered, keep accumulating and paint once for the whole burst.
		if state.gr.Buffered() == 0 {
			t.flushFrontend()
		}
	}
}

// readLoopState is the user-space read-loop state that survives a pause.
type readLoopState struct {
	gr         *GraphemeReader
	pendingCmd []byte
	// cmdBuf and cmdReader are reused across escape commands so a stream of
	// SGR/CSI sequences does not allocate a buffer and a bytes.Reader each time.
	cmdBuf    []byte
	cmdReader bytes.Reader
}

func newReadLoopState(src io.Reader, st ReaderState) *readLoopState {
	gs := GraphemeState{
		Pending:        st.Pending,
		State:          st.State,
		Initial:        st.Initial,
		ForceMergeNext: st.ForceMergeNext,
		LastWasRI:      st.LastWasRI,
		Mode:           st.Mode,
	}
	return &readLoopState{
		gr:         NewGraphemeReaderFromState(src, gs),
		pendingCmd: append([]byte(nil), st.PendingCommand...),
		cmdBuf:     make([]byte, 0, 64),
	}
}

func (s *readLoopState) snapshot() ReaderState {
	gs := s.gr.SnapshotState()
	return ReaderState{
		Pending:        gs.Pending,
		PendingCommand: append([]byte(nil), s.pendingCmd...),
		State:          gs.State,
		Initial:        gs.Initial,
		ForceMergeNext: gs.ForceMergeNext,
		LastWasRI:      gs.LastWasRI,
		Mode:           gs.Mode,
	}
}

func (t *terminal) ptyReadOne(state *readLoopState) error {
	gr := state.gr
	maxWidth := 0
	t.withLock(func() {
		if t.screen().AutoWrap() {
			maxWidth = t.screen().Size().X - t.screen().CursorPos().X
			if maxWidth < 1 {
				maxWidth = 1
			}
		}
	})
	data, width, merge, meta, err := gr.readPrintableRun(maxWidth)
	if err != nil {
		if err != io.EOF {
			debugPrintln(debugErrors, "ERR ReadPrintableBytes:", err)
		}
		return err
	}
	if len(data) > 0 {
		t.withLock(func() {
			t.screen().writeStringMeta(data, width, merge, t.textReadMode, meta)
		})
		if *debugTxt {
			debugPrintf(debugTxt, "\033[32mtxt: %#v\033[0m %v\n", data, len(data))
		}
		return nil
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
			cursorPos.X = t.screen().nextTabStop(cursorPos.X, 1)
			t.screen().setCursorPos(cursorPos.X, cursorPos.Y)
		})

	case 10: // LF ^J Linefeed (newline)
		// A line feed moves down without changing the column (VT100/xterm);
		// the tty driver normally sends CRLF. LNM (CSI 20 h) makes LF do a
		// carriage return as well.
		t.withLock(func() {
			t.screen().moveCursor(0, 1, false, true)
			if t.newlineMode {
				t.screen().moveCursor(-t.screen().CursorPos().X, 0, true, true)
			}
		})

	case 11: // VT ^K Vertical TAB
		t.withLock(func() {
			t.screen().moveCursor(0, 1, false, true)
		})

	case 12: // FF ^L Formfeed (also: New page NP)
		t.withLock(func() {
			t.screen().moveCursor(0, 1, false, true)
		})

	case 13: // CR ^M Carriage Return
		t.withLock(func() {
			t.screen().moveCursor(-t.screen().CursorPos().X, 0, true, true)
		})

	case 14: // SO ^N Shift Out: invoke G1 as GL
		t.withLock(func() {
			t.screen().invokeCharset(1)
		})

	case 15: // SI ^O Shift In: invoke G0 as GL
		t.withLock(func() {
			t.screen().invokeCharset(0)
		})

	case 27: // ESC ^[ Escape Character
		// Read the whole command before taking the lock: handleCommand
		// parses from memory, so a child that stops mid-sequence cannot
		// block Snapshot/Restore (which need the lock) behind a pending
		// ReadByte on the backend.
		cmd, err := readEscapeCommand(state.cmdBuf[:0], gr, nil)
		state.cmdBuf = cmd
		if err != nil {
			if len(cmd) == 0 {
				// The ESC itself was already consumed; remember it so a
				// resumed loop can continue the command.
				cmd = []byte{0x1b}
			}
			state.pendingCmd = append([]byte(nil), cmd...)
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR readEscapeCommand:", err)
			}
			return err
		}

		t.dispatchEscapeCommand(state, cmd)
		// The escape sequence ends the run of graphic characters, so REP
		// cannot repeat across it (xterm/tmux behavior). It is cleared after
		// dispatch so REP itself still sees the character it repeats.
		t.withLock(func() {
			t.screen().clearLastChar()
		})

	case 127: // DEL  Delete Character (treat as backspace)
		t.withLock(func() {
			t.screen().moveCursor(-1, 0, false, false)
		})
	default:
		debugPrintf(debugTodo, "TODO: unhandled char %v %#v\n", b, string(b))
	}

	// Control characters end the run of graphic characters (REP must not
	// repeat across them).
	t.withLock(func() {
		t.screen().clearLastChar()
	})
	return nil
}

// continueEscapeCommand resumes an ESC-initiated command from state.pendingCmd.
// It is called at the top of ptyReadLoop before normal reads.
func (t *terminal) continueEscapeCommand(state *readLoopState) error {
	cmd, err := readEscapeCommand(state.cmdBuf[:0], state.gr, state.pendingCmd)
	state.cmdBuf = cmd
	if err != nil {
		state.pendingCmd = append([]byte(nil), cmd...)
		if len(state.pendingCmd) == 0 {
			state.pendingCmd = []byte{0x1b}
		}
		return err
	}
	state.pendingCmd = nil
	t.dispatchEscapeCommand(state, cmd)
	return nil
}

// dispatchEscapeCommand applies a complete ESC-initiated command under the
// terminal lock.
func (t *terminal) dispatchEscapeCommand(state *readLoopState, cmd []byte) {
	t.withLock(func() {
		// Hot CSI commands are parsed from the completed command bytes into a
		// fixed parameter array. Everything else, including private markers,
		// intermediates and malformed forms, falls back to the general parser.
		var success bool
		fastOK := false
		if len(cmd) > 1 && cmd[0] == '[' {
			success, fastOK = t.tryFastCSI(cmd)
		}
		if !fastOK {
			state.cmdReader.Reset(cmd)
			success = t.handleCommand(&state.cmdReader)
		}
		if *debugCmd || *debugTodo {
			if success {
				debugPrintf(debugCmd, "%v cmd: %#v\n", t.screen().CursorPos(), string(cmd))
			} else {
				debugPrintf(debugTodo, "TODO: Unhandled command: %#v\n", string(cmd))
			}
		}
	})
}

// tryFastCSI parses and dispatches the hot, plain CSI commands in one pass over
// the completed command bytes (starting just after ESC). ok reports whether the
// command had one of those forms; when it is false the caller must use the
// general parser. The parameter rules match handleCmdCSI exactly: an omitted
// parameter list means "no parameters", empty items between separators are 0,
// a trailing separator adds one more 0, and parameters beyond the fixed store
// are dropped. Any prefix, intermediate, sub-parameter, malformed byte, or
// unexpected final byte is rejected so semantics cannot drift.
func (t *terminal) tryFastCSI(cmd []byte) (success, ok bool) {
	if len(cmd) < 2 || cmd[0] != '[' {
		return false, false
	}
	final := cmd[len(cmd)-1]
	switch final {
	case 'H', 'f', 'm', 'A', 'B', 'C', 'D', 'J', 'K':
	default:
		return false, false
	}

	var store [8]int
	count := 0
	param := 0
	paramSet := false
	sawSeparator := false
	for _, b := range cmd[1 : len(cmd)-1] {
		if b >= '0' && b <= '9' {
			param = param*10 + int(b-'0')
			paramSet = true
			sawSeparator = false
			continue
		}
		if b != ';' {
			return false, false
		}
		value := 0
		if paramSet || sawSeparator || count > 0 {
			value = param
		}
		if count < len(store) {
			store[count] = value
			count++
		}
		param = 0
		paramSet = false
		sawSeparator = true
	}
	if paramSet || sawSeparator {
		value := 0
		if paramSet {
			value = param
		}
		if count < len(store) {
			store[count] = value
			count++
		}
	}
	params := store[:count]

	// Dispatch from the parsed array. The J/K/SGR/H helpers are shared with the
	// general parser, so both paths apply identical semantics; A-D are inlined
	// because their commands are short enough that an extra call is measurable.
	switch final {
	case 'A', 'B', 'C', 'D':
		n := 1
		if len(params) > 0 {
			n = params[0]
		}
		switch final {
		case 'A':
			t.screen().moveCursor(0, -n, false, false)
		case 'B':
			t.screen().moveCursor(0, n, false, false)
		case 'C':
			t.screen().moveCursor(n, 0, false, false)
		case 'D':
			t.screen().moveCursor(-n, 0, false, false)
		}
	case 'H', 'f':
		t.csiCursorPosition(params)
	case 'm':
		t.csiSetGraphicRendition(params)
	case 'J':
		return t.csiEraseInDisplay(params), true
	case 'K':
		return t.csiEraseInLine(params), true
	}
	return true, true
}

type escapeReader interface {
	ReadByte() (byte, error)
}

// readEscapeCommand reads one complete ESC-initiated command, whose leading
// ESC has already been consumed. partial holds bytes that were consumed before
// a pause; they are replayed first through the same CSI/OSC/DCS/two-byte
// logic. On error it returns the bytes consumed so far (including partial) so
// the caller can store them and resume later.
//
// CSI commands end at a final byte in 0x40-0x7E. OSC, DCS, APC, PM and SOS
// strings end at ST (ESC \\ or 0x9C) or BEL, honoring UTF-8 continuation bytes.
// Two-byte commands consume their argument, and everything else is a single
// command byte. The bytes are buffered even when debugging is off; the debug
// capture uses the returned slice directly.
func readEscapeCommand(dst []byte, r escapeReader, partial []byte) ([]byte, error) {
	// A lone ESC is stored as a sentinel when no command byte was read yet.
	if len(partial) > 0 && partial[0] == 0x1b {
		partial = partial[1:]
	}
	buf := dst[:0]
	drop := len(partial) > maxEscapeCommandBytes
	if drop {
		buf = buf[:0]
	} else {
		buf = append(buf, partial...)
	}
	replay := 0
	read := func() (byte, error) {
		if replay < len(partial) {
			b := partial[replay]
			replay++
			return b, nil
		}
		b, err := r.ReadByte()
		if err == nil {
			if drop {
				return b, nil
			}
			if len(buf) >= maxEscapeCommandBytes {
				// Stop buffering, but keep consuming until the sequence
				// terminator. A broken/unterminated sequence must not grow
				// memory without bound.
				drop = true
				buf = nil
				return b, nil
			}
			buf = append(buf, b)
		}
		return b, err
	}

	b, err := read()
	if err != nil {
		return buf, err
	}

	// GraphemeReader exposes its buffered bytes so command scanning can look
	// at a whole chunk at once instead of calling ReadByte per byte.
	br, bulk := r.(bulkEscapeReader)

	switch b {
	case '[': // CSI: parameter/intermediate bytes then a final byte
		for {
			if replay < len(partial) {
				b, err := read()
				if err != nil {
					return buf, err
				}
				if isCSIFinal(b) || isCSIMalformed(b) {
					return commandResult(buf, drop), nil
				}
				continue
			}
			if bulk {
				if p := br.peekBuffered(); len(p) > 0 {
					n := 0
					for n < len(p) {
						c := p[n]
						n++
						if isCSIFinal(c) || isCSIMalformed(c) {
							buf, drop = appendEscapeBytes(buf, p[:n], drop)
							br.discardBuffered(n)
							return commandResult(buf, drop), nil
						}
					}
					buf, drop = appendEscapeBytes(buf, p[:n], drop)
					br.discardBuffered(n)
				}
			}
			b, err := read()
			if err != nil {
				return buf, err
			}
			if isCSIFinal(b) || isCSIMalformed(b) {
				return commandResult(buf, drop), nil
			}
		}

	case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS
		var scanner stringTermScanner
		for {
			if replay < len(partial) {
				b, err := read()
				if err != nil {
					return buf, err
				}
				if b == 0x07 || scanner.isST(b) {
					return commandResult(buf, drop), nil
				}
				continue
			}
			if bulk {
				if p := br.peekBuffered(); len(p) > 0 {
					n := 0
					for n < len(p) {
						c := p[n]
						n++
						if c == 0x07 || scanner.isST(c) {
							buf, drop = appendEscapeBytes(buf, p[:n], drop)
							br.discardBuffered(n)
							return commandResult(buf, drop), nil
						}
					}
					buf, drop = appendEscapeBytes(buf, p[:n], drop)
					br.discardBuffered(n)
				}
			}
			b, err := read()
			if err != nil {
				return buf, err
			}
			if b == 0x07 || scanner.isST(b) {
				return commandResult(buf, drop), nil
			}
		}

	case '%', ' ', '-', '.', '/', '(', ')', '*', '+', '#':
		// Two-byte commands with one argument byte.
		if _, err := read(); err != nil {
			return buf, err
		}
	}

	return commandResult(buf, drop), nil
}

// bulkEscapeReader is implemented by GraphemeReader so escape commands can be
// scanned a chunk at a time. peekBuffered returns the unread bytes without
// consuming them; discardBuffered consumes a prefix of that slice.
type bulkEscapeReader interface {
	peekBuffered() []byte
	discardBuffered(n int)
}

func isCSIFinal(b byte) bool { return b >= 0x40 && b <= 0x7e }

// isCSIMalformed reports bytes that cannot appear in a CSI parameter or
// intermediate position. The handler treats them as the final byte instead of
// swallowing following text.
func isCSIMalformed(b byte) bool { return b < 0x20 || b > 0x3f }

// appendEscapeBytes appends consumed command bytes, enforcing the command size
// limit. It returns a nil buffer once the command is dropped.
func appendEscapeBytes(buf, p []byte, drop bool) ([]byte, bool) {
	if drop {
		return nil, true
	}
	if len(buf)+len(p) > maxEscapeCommandBytes {
		return nil, true
	}
	return append(buf, p...), false
}

// commandResult drops a command that exceeded maxEscapeCommandBytes.
func commandResult(buf []byte, drop bool) []byte {
	if drop {
		return nil
	}
	return buf
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

	case 'E': // NEL: Next Line (line feed plus carriage return)
		cursorPos := t.screen().CursorPos()
		t.screen().setCursorPos(0, cursorPos.Y)
		t.screen().moveCursor(0, 1, false, true)

	case 'H': // HTS: Set a horizontal tab stop at the cursor column
		t.screen().setTabStop(t.screen().CursorPos().X)

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
		slot := 0
		switch b {
		case ')':
			slot = 1
		case '*':
			slot = 2
		case '+':
			slot = 3
		}
		C, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte4:", err)
			}
			return false
		}
		t.screen().designateCharset(slot, charsetFromDesignationByte(C))

	case 'n': // LS2: invoke G2 as GL
		t.screen().invokeCharset(2)

	case 'o': // LS3: invoke G3 as GL
		t.screen().invokeCharset(3)

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
	} else {
		pm = t.ansiModeState(ps)
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
	case 6: // DECOM
		set = t.screen().OriginMode()
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
	case 1046: // Alt-screen switching allowed
		set = t.viewFlags[VFAltScreenAllowed]
	case 1000:
		set = t.viewInts[VIMouseMode] == MMPressRelease
	case 1002:
		set = t.viewInts[VIMouseMode] == MMPressReleaseMove
	case 1003:
		set = t.viewInts[VIMouseMode] == MMPressReleaseMoveAll
	case 1004: // Focus reporting
		set = t.viewFlags[VFReportFocus]
	case 1007: // Alternate scroll mode
		set = t.viewFlags[VFAltScroll]
	case 1016: // SGR-Pixels mouse reporting
		set = t.viewFlags[VFMousePixel]
	case 1005: // UTF-8 mouse encoding
		set = t.viewInts[VIMouseEncoding] == MEUTF8
	case 1006: // SGR mouse encoding
		set = t.viewInts[VIMouseEncoding] == MESGR
	case 1015: // urxvt mouse encoding
		set = t.viewInts[VIMouseEncoding] == MEURXVT
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

// ansiModeState reports the state of an ANSI (non-private) mode for DECRQM:
// 1 set, 2 reset, or 0 when the mode is not recognized.
func (t *terminal) ansiModeState(mode int) int {
	set := false
	switch mode {
	case 4: // IRM
		set = t.screen().InsertMode()
	case 20: // LNM
		set = t.newlineMode
	default:
		return 0
	}
	if set {
		return 1
	}
	return 2
}

// handleDECSTR implements DECSTR (CSI ! p): soft terminal reset. It restores
// default style, full scroll margins, auto-wrap, character sets, insert/
// origin mode, and cursor visibility without touching screen contents or tab
// stops (xterm keeps tab stops across a soft reset).
func (t *terminal) handleDECSTR() {
	for _, s := range []screen{t.mainScreen, t.altScreen} {
		s.setStyle(NewStyle())
		s.setScrollMarginTopBottom(0, s.Size().Y-1)
		s.SetAutoWrap(true)
		s.resetCharsets()
		s.SetInsertMode(false)
		s.SetOriginMode(false)
	}
	t.setViewFlag(VFShowCursor, true)
	t.setViewInt(VICursorShape, CursorShapeDefault)
}

// lineScrollRegion returns the row range affected by IL/DL. Inside the
// scroll margins the region ends at the bottom margin; outside it (above or
// below the margins) xterm/tmux scroll from the cursor row to the bottom of
// the screen.
func lineScrollRegion(t *terminal) (y1, y2 int) {
	y1 = t.screen().CursorPos().Y
	y2 = t.screen().BottomMargin()
	if y1 < t.screen().TopMargin() || y1 > y2 {
		y2 = t.screen().Size().Y - 1
	}
	return y1, y2
}

// csiCursorPosition implements CUP/HVP (CSI Pl;Pc H and CSI Pl;Pc f). An
// omitted component defaults to 1; an explicit 0 keeps its literal value and is
// clamped by the screen when the cursor is set.
func (t *terminal) csiCursorPosition(params []int) {
	x := 1
	y := 1
	if len(params) >= 1 {
		y = params[0]
	}
	if len(params) >= 2 {
		x = params[1]
	}
	// In origin mode the row is relative to the top margin.
	row := y - 1
	if t.screen().OriginMode() {
		row += t.screen().TopMargin()
	}
	t.screen().setCursorPos(x-1, row)
}

// csiCursorMove implements CUU/CUD/CUF/CUB (CSI Ps A/B/C/D). An omitted
// parameter defaults to 1; an explicit 0 moves zero cells.
func (t *terminal) csiCursorMove(final byte, params []int) {
	n := 1
	if len(params) > 0 {
		n = params[0]
	}
	switch final {
	case 'A':
		t.screen().moveCursor(0, -n, false, false)
	case 'B':
		t.screen().moveCursor(0, n, false, false)
	case 'C':
		t.screen().moveCursor(n, 0, false, false)
	case 'D':
		t.screen().moveCursor(-n, 0, false, false)
	}
}

// csiSetGraphicRenditionGroups implements SGR (CSI Ps;... m). params holds
// the primary value of each parameter; groupStore/groupLens hold the
// colon-separated sub-parameter groups aligned with params, so extended
// colors can be written as 38:2::r:g:b as well as 38;2;r;g;b.
func (t *terminal) csiSetGraphicRenditionGroups(params []int, groupStore *[8][8]int, groupLens *[8]int) {
	style := t.screen().Style()

	for i := 0; i < len(params); i++ {
		g := groupStore[i][:groupLens[i]]
		p := g[0]
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

		case p == 38 || p == 48 || p == 58: // extended set color
			component := ComponentFG
			switch p {
			case 48:
				component = ComponentBG
			case 58:
				component = ComponentUnderline
			}
			if len(g) >= 2 {
				// Colon form: mode and components are sub-parameters.
				switch g[1] {
				case 5: // 38:5:n
					if len(g) >= 3 {
						_ = style.SetColor(component, Color256(g[2]&0xff))
					}
				case 2: // 38:2:r:g:b or 38:2:<colorspace>:r:g:b
					if len(g) >= 5 {
						// The colorspace id is optional; the color is
						// always the last three components.
						start := len(g) - 3
						_ = style.SetColor(component, RGB(g[start], g[start+1], g[start+2]))
					}
				}
			} else if i+2 < len(params) {
				// Semicolon form consumes following parameters.
				switch params[i+1] {
				case 5: // 38;5;n
					_ = style.SetColor(component, Color256(params[i+2]&0xff))
					i += 2
				case 2: // 38;2;r;g;b
					if i+4 < len(params) {
						_ = style.SetColor(component, RGB(params[i+2], params[i+3], params[i+4]))
						i += 4
					}
				default:
					debugPrintln(debugTodo, "TODO: unhandled extended color: ", params[i+1])
				}
			}

		case p == 59: // default underline color
			_ = style.SetColor(ComponentUnderline, DefaultColor())

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
}

// csiSetGraphicRendition implements SGR for commands whose parameters are all
// plain (semicolon-separated), as produced by the fast CSI path. An omitted
// parameter list is a single 0 (reset), matching the general parser.
func (t *terminal) csiSetGraphicRendition(params []int) {
	var groups [8][8]int
	var lens [8]int
	var onlyDefault [1]int
	if len(params) == 0 {
		params = onlyDefault[:]
	}
	for i := 0; i < len(params) && i < len(groups); i++ {
		groups[i][0] = params[i]
		lens[i] = 1
	}
	t.csiSetGraphicRenditionGroups(params, &groups, &lens)
}

// csiEraseInLine implements EL (CSI Ps K).
func (t *terminal) csiEraseInLine(params []int) bool {
	// eraseRegion clamps the region to the window, so we don't have to be too careful here
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
	return true
}

// csiEraseInDisplay implements ED (CSI Ps J).
func (t *terminal) csiEraseInDisplay(params []int) bool {
	// eraseRegion clamps the region to the window, so we don't have to be too careful here
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
	case params[0] == 2: // Erase entire screen (cursor does not move)
		t.screen().eraseRegion(Region{
			X:  0,
			Y:  0,
			X2: t.screen().Size().X,
			Y2: t.screen().Size().Y,
		}, CRClear)
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

	// Parameters are separated by ';' and sub-parameters by ':' (ECMA-48).
	// Every handler gets the primary value of each parameter; SGR also gets
	// the colon-separated groups so it can parse 38:2::r:g:b style colors.
	// This cannot escape to the heap: fixed arrays, no slices of slices.
	var paramStore [8]int
	var groupStore [8][8]int
	var groupLens [8]int
	paramCount := 0
	param := 0
	paramSet := false
	clampGroup := func(idx int) {
		if groupLens[idx] > len(groupStore[idx]) {
			groupLens[idx] = len(groupStore[idx])
		}
	}
	// pushSub stores the value being accumulated as the next sub-parameter of
	// the current parameter.
	pushSub := func() {
		if paramCount >= len(paramStore) {
			return
		}
		value := 0
		if paramSet {
			value = param
		}
		if groupLens[paramCount] < len(groupStore[paramCount]) {
			groupStore[paramCount][groupLens[paramCount]] = value
			groupLens[paramCount]++
		}
		if groupLens[paramCount] == 1 {
			paramStore[paramCount] = value
		}
	}
	// pushParam finishes the current parameter (its last sub-parameter
	// included) and starts a new one.
	pushParam := func() {
		pushSub()
		if paramCount < len(paramStore) {
			paramCount++
		}
		param = 0
		paramSet = false
	}
	for b == ';' || b == ':' || (b >= '0' && b <= '9') {
		switch b {
		case ';':
			pushParam()
		case ':':
			pushSub()
			// An empty sub-parameter is a real value (for example the omitted
			// colorspace in 38:2::r:g:b), so move past it.
			param = 0
			paramSet = false
		default:
			param = param*10 + int(b-'0')
			paramSet = true
		}

		b, err = r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByte7:", err)
			}
			return false
		}
	}
	if paramSet || paramCount > 0 || groupLens[paramCount] > 0 {
		pushParam()
	}
	for i := 0; i < paramCount; i++ {
		clampGroup(i)
	}

	params := paramStore[:paramCount]
	// defaultParam supplies the CSI default for a missing first parameter.
	defaultParam := func(v int) {
		if paramCount != 0 {
			return
		}
		paramStore[0] = v
		groupStore[0][0] = v
		groupLens[0] = 1
		paramCount = 1
		params = paramStore[:paramCount]
	}

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
		case len(intermediates) == 1 && intermediates[0] == ' ' && b == 'q':
			// DECSCUSR: set cursor shape. Unknown shapes are ignored.
			ps := 0
			if len(params) > 0 {
				ps = params[0]
			}
			if ps >= 0 && ps <= 6 {
				t.setViewInt(VICursorShape, ps)
			}
		default:
			debugPrintf(debugTodo, "TODO: Unhandled CSI intermediates: %#v %v, %#v\n", string(intermediates), append([]int(nil), params...), string(b))
			return true
		}
		return true
	}

	if prefix == 0 {
		switch b {
		case 'A', 'B', 'C', 'D': // Cursor up/down/forward/backward
			t.csiCursorMove(b, params)

		case 'E': // Cursor Next Line
			defaultParam(1)
			t.screen().moveCursor(0, params[0], false, false)
			pos := t.screen().CursorPos()
			t.screen().setCursorPos(0, pos.Y)
		case 'F': // Cursor Previous Line
			defaultParam(1)
			t.screen().moveCursor(0, -params[0], false, false)
			pos := t.screen().CursorPos()
			t.screen().setCursorPos(0, pos.Y)
		case 'a': // Cursor Forward (HPR)
			defaultParam(1)
			t.screen().moveCursor(params[0], 0, false, false)
		case 'e': // Cursor Down (VPR)
			defaultParam(1)
			t.screen().moveCursor(0, params[0], false, false)
		case 'I': // Cursor Forward Tabulation
			defaultParam(1)
			pos := t.screen().CursorPos()
			t.screen().setCursorPos(t.screen().nextTabStop(pos.X, params[0]), pos.Y)
		case 'Z': // Cursor Backward Tabulation
			defaultParam(1)
			pos := t.screen().CursorPos()
			t.screen().setCursorPos(t.screen().prevTabStop(pos.X, params[0]), pos.Y)

		case 'G': // Cursor Character Absolute
			defaultParam(1)
			t.screen().setCursorPos(params[0]-1, t.screen().CursorPos().Y)

		case 'c': // Send Device Attributes
			defaultParam(0)
			switch params[0] {
			case 0:
				_ = t.writeReply([]byte("\033[?1;2c"))
			}

		case 'd': // Line Position Absolute
			defaultParam(1)
			row := params[0] - 1
			if t.screen().OriginMode() {
				row += t.screen().TopMargin()
			}
			t.screen().setCursorPos(t.screen().CursorPos().X, row)

		case 'f', 'H': // Cursor Home
			t.csiCursorPosition(params)

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
			case 4: // IRM: insert mode
				t.screen().SetInsertMode(value)

			case 20: // LNM: line feed also performs a carriage return
				t.newlineMode = value
			default:
				debugPrintln(debugTodo, "TODO: Unhandled CSI mode param: ", params[0])
				return false
			}

		case 'm': // Set color/mode
			defaultParam(0)
			t.csiSetGraphicRenditionGroups(params, &groupStore, &groupLens)

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
			return t.csiEraseInLine(params)

		case 'J': // Erase Lines
			return t.csiEraseInDisplay(params)

		case 'L': // Insert lines, scroll down
			defaultParam(1)
			y1, y2 := lineScrollRegion(t)
			t.screen().scroll(y1, y2, params[0])

		case 'M': // Delete lines, scroll up
			defaultParam(1)
			y1, y2 := lineScrollRegion(t)
			t.screen().scroll(y1, y2, -params[0])

		case 'S': // Scroll up
			defaultParam(1)
			t.screen().scroll(t.screen().TopMargin(), t.screen().BottomMargin(), -params[0])

		case 'T': // Scroll down
			defaultParam(1)
			t.screen().scroll(t.screen().TopMargin(), t.screen().BottomMargin(), params[0])

		case 'b': // REP: repeat the preceding graphic character
			defaultParam(1)
			count := params[0]
			if count <= 0 {
				count = 1
			}
			t.screen().repeatLastChar(count)

		case 'P': // Delete n characters
			defaultParam(1)
			t.screen().deleteChars(t.screen().CursorPos().X, t.screen().CursorPos().Y, params[0], CRClear)

		case '@': // Insert n characters (ICH)
			defaultParam(1)
			t.screen().insertChars(t.screen().CursorPos().X, t.screen().CursorPos().Y, params[0], CRClear)

		case 'X': // Erase from cursor pos to the right
			defaultParam(1)
			t.screen().eraseRegion(Region{
				X:  t.screen().CursorPos().X,
				Y:  t.screen().CursorPos().Y,
				X2: t.screen().CursorPos().X + params[0],
				Y2: t.screen().CursorPos().Y + 1,
			}, CRClear)

		case 'g': // Clear tab stops (TBC)
			defaultParam(0)
			switch params[0] {
			case 0: // clear the stop at the cursor column
				t.screen().clearTabStop(t.screen().CursorPos().X)
			case 3: // clear all stops
				t.screen().clearAllTabStops()
			default:
				debugPrintln(debugTodo, "TODO: Unhandled TBC param: ", params[0])
			}

		case 'x': // DECREQTPARM: report terminal parameters
			defaultParam(0)
			if params[0] == 0 || params[0] == 1 {
				// Same shape as xterm's VT100/VT102 reply: Ps+2; no parity;
				// 8 bits; 38400 baud both ways; clock multiplier 1; flags 0.
				_ = t.writeReply([]byte(fmt.Sprintf("\033[%d;1;1;128;128;1;0x", params[0]+2)))
			}

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
			// DECSTBM homes the cursor (VT100/xterm behavior); in origin mode
			// home is the top-left of the scroll region.
			t.screen().homeCursor()

		case 'n': // Device Status Report
			defaultParam(0)
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
	} else if prefix == '?' {
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

				case 6: // Origin mode (DECOM)
					t.screen().SetOriginMode(value)
					t.screen().homeCursor()

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

				case 1007: // Alternate scroll mode
					t.setViewFlag(VFAltScroll, value)

				case 1016: // SGR-Pixels mouse reporting
					t.setViewFlag(VFMousePixel, value)

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
						t.setViewInt(VIMouseEncoding, MEURXVT)
					} else {
						t.setViewInt(VIMouseEncoding, MEX10)
					}

				case 1034:
					debugPrintf(debugTodo, "TODO: Interpret Meta key = %v\n", value)

				case 47: // Alternate screen, preserve contents and cursor slot
					t.setAltScreen(value)

				case 1047: // Alternate screen; clearing happens on the way out
					if value {
						t.setAltScreen(true)
					} else if t.onAltScreen {
						size := t.altScreen.Size()
						t.altScreen.eraseRegion(Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRClear)
						t.setAltScreen(false)
					}

				case 1048: // Save/restore cursor only, no screen switch
					if value {
						t.screen().saveCursorPos()
					} else {
						t.screen().restoreCursorPos()
					}

				case 1046: // Allow (set) or forbid (reset) alt-screen switching
					t.setViewFlag(VFAltScreenAllowed, value)

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
	} else if prefix == '>' {
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
	} else if prefix == '<' {
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
	} else if prefix == '=' {
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

	var param2 []byte

	if b == ';' {
		var ok bool
		param2, ok = readStringPayload(r, true)
		if !ok {
			return false
		}
	} else if b != 7 && b != 0x9c { // BEL, ST
		debugPrintln(debugErrors, "OSC command number not followed by ;, BEL, or ST?", b)
		return false
	}

	switch param {
	case 0, 2:
		t.setViewString(VSWindowTitle, string(param2))

	case 4:
		t.handleOSC4(param2)

	case 6, 7:
		// OSC 7 is the standard working-directory URI advertised by shells
		// (e.g. "file://host/path"); OSC 6 is the legacy nonstandard form.
		// An OSC 7 payload alone never means a file, so no OSC sets
		// VSCurrentFile.
		t.setViewString(VSCurrentDirectory, string(param2))

	case 8:
		t.handleOSC8(param2)

	case 52:
		t.handleOSC52(param2)

	case 10, 11, 12: // default foreground, background, and cursor colors
		t.handleOSCDynamicColor(param, param2)

	case 104: // reset color palette
		t.handleOSC104(param2)

	case 110, 111, 112: // reset default fg / bg / cursor color
		t.handleOSCDynamicColor(param, param2)

	case 9: // OSC 9: desktop notification with a body only
		if sink, ok := t.frontend.(NotificationSink); ok {
			sink.Notify(LockedView{t: t}, "", string(param2))
		}

	case 133: // Shell integration: semantic prompt marks
		t.handleOSC133(param2)

	case 777: // OSC 777: desktop notification "notify;title;body"
		t.handleOSC777(param2)

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

// handleOSC133 decodes an OSC 133 semantic prompt mark and forwards it to a
// ShellIntegration frontend. Payloads are "A", "B", "C", "D[;exit]", or
// extension forms such as "P;k=v"; unknown marks are ignored.
func (t *terminal) handleOSC133(payload []byte) {
	sink, ok := t.frontend.(ShellIntegration)
	if !ok || len(payload) == 0 {
		return
	}
	s := string(payload)
	kind := s[0]
	rest := ""
	if len(s) > 1 && s[1] == ';' {
		rest = s[2:]
	} else if len(s) > 1 {
		// Not a mark we understand (for example a bare extension form).
		return
	}
	mark := PromptMark{ExitCode: -1, Raw: rest}
	switch kind {
	case 'A':
		mark.Kind = PromptMarkStart
	case 'B':
		mark.Kind = PromptMarkInput
	case 'C':
		mark.Kind = PromptMarkCommand
	case 'D':
		mark.Kind = PromptMarkDone
		if code, err := strconv.Atoi(strings.SplitN(rest, ";", 2)[0]); err == nil {
			mark.ExitCode = code
		}
	default:
		return
	}
	sink.PromptMark(LockedView{t: t}, mark)
}

// handleOSC777 decodes an OSC 777 notification ("notify;title;body") and
// forwards it to a NotificationSink frontend.
func (t *terminal) handleOSC777(payload []byte) {
	sink, ok := t.frontend.(NotificationSink)
	if !ok {
		return
	}
	parts := strings.SplitN(string(payload), ";", 3)
	if len(parts) < 1 || parts[0] != "notify" {
		return
	}
	title, body := "", ""
	if len(parts) > 1 {
		title = parts[1]
	}
	if len(parts) > 2 {
		body = parts[2]
	}
	sink.Notify(LockedView{t: t}, title, body)
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

// readStringPayload reads a control-string payload up to BEL or ST and
// returns it without the terminator. When collect is false the payload is
// consumed but not buffered. ok is false when a read failed first.
func readStringPayload(r escapeReader, collect bool) ([]byte, bool) {
	var scanner stringTermScanner
	var payload []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err != io.EOF {
				debugPrintln(debugErrors, "ERR ReadByteString:", err)
			}
			return nil, false
		}
		if scanner.isST(b) {
			// A 7-bit ST is ESC \\: drop the ESC that was buffered as payload.
			if b == '\\' && len(payload) > 0 && payload[len(payload)-1] == 0x1b {
				payload = payload[:len(payload)-1]
			}
			return payload, true
		}
		if b == 0x07 {
			return payload, true
		}
		if collect {
			payload = append(payload, b)
		}
	}
}

// consumeString discards an ESC-initiated string (APC, PM, SOS) up to its
// terminator: ST (ESC \ or 0x9C) or BEL. Payload bytes are never printed.
func (t *terminal) consumeString(r escapeReader) bool {
	_, ok := readStringPayload(r, false)
	return ok
}

func (t *terminal) handleDCS(r escapeReader) bool {
	payload, ok := readStringPayload(r, *debugCmd)
	if !ok {
		return false
	}
	if *debugCmd {
		debugPrintf(debugCmd, "DCS payload: %q\n", string(payload))
	}
	return true
}
