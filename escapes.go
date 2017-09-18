package termemu

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"strconv"
	"strings"
)

var debugTxt = flag.Bool("debugTxt", false, "Print all text written to screen")
var debugCmd = flag.Bool("debugCmd", false, "Print all commands")

type dupReader struct {
	reader *bufio.Reader
	t      *terminal

	unread int
}

// func (r *dupReader) Read(b []byte) (n int, err error) {
// 	n, err = r.reader.Read(b)
// 	if n > 0 && r.t.dup != nil {
// 		_, err2 := r.t.dup.Write(b[:n])
// 		if err2 != nil {
// 			fmt.Println(err2)
// 			r.t.dup = nil
// 		}
// 	}
//
// 	return n, err
// }

func (r *dupReader) ReadByte() (byte, error) {
	b, err := r.reader.ReadByte()
	if err == nil && r.t.dup != nil {
		r.t.dup.Write([]byte{b})
	}
	if r.unread > 0 {
		r.unread -= 1
	}
	return b, err
}
func (r *dupReader) UnreadByte() error {
	err := r.reader.UnreadByte()
	r.unread += 1
	return err
}
func (r *dupReader) ReadRune() (rune, int, error) {
	b, n, err := r.reader.ReadRune()
	if err == nil && r.t.dup != nil {
		r.t.dup.Write([]byte(string(b))[r.unread:])
	}
	r.unread -= n
	if r.unread < 0 {
		r.unread = 0
	}
	return b, n, err
}
func (r *dupReader) Buffered() int {
	return r.reader.Buffered()
}

type recordingReader struct {
	r   *dupReader
	buf *bytes.Buffer
}

func (r *recordingReader) ReadByte() (b byte, err error) {
	b, err = r.r.ReadByte()
	if err == nil && r.buf != nil {
		_, _ = r.buf.Write([]byte{b})
	}

	return b, err
}

func (r *recordingReader) StartRecording() {
	r.buf = &bytes.Buffer{}
}

func (r *recordingReader) StopRecording() []byte {
	bs := r.buf.Bytes()
	r.buf = nil
	return bs
}

// type ptyReader struct {
// 	t        *terminal
// 	buffer   []byte
// 	curBytes []byte
// }
//
// func (r *ptyReader) read() ([]byte, error) {
// 	if len(r.curBytes) == 0 {
// 		n, err := r.t.pty.Read(r.buffer)
// 		if err != nil {
// 			fmt.Println(err)
// 			return nil, err
// 		}
//
// 		r.curBytes = r.buffer[:n]
// 	}
//
// 	return r.curBytes, nil
// }
//
// func (r *ptyReader) readAll(b func(byte) bool) (matched []byte, rest []byte, err error) {
// 	bs, err := r.read()
// 	if err != nil {
// 		return nil, nil, err
// 	}
//
// 	i := 0
//
// 	prefix := []byte{}
// 	for {
// 		if i >= len(bs) {
// 			prefix = append(prefix, bs...)
// 			bs, err = r.advance(i)
// 			if err != nil {
// 				return nil, nil, err
// 			}
// 			i = 0
// 		}
// 		if !b(bs[i]) {
// 			break
// 		}
// 		i++
// 	}
//
// 	ret := append(prefix, bs[:i]...)
//
// 	rest, err = r.advance(i)
//
// 	return ret, rest, err
// }
//
// func (r *ptyReader) advance(l int) (rest []byte, err error) {
//
// 	if r.t.dup != nil && l > 0 {
// 		n, err := r.t.dup.Write(r.curBytes[:l])
// 		if err != nil || n != l {
// 			fmt.Println("dup: ", err)
// 			r.t.dup = nil
// 		}
// 	}
//
// 	r.curBytes = r.curBytes[l:]
// 	rest, err = r.read()
// 	return rest, err
// }

func isPrintable(c byte) bool {
	return (32 <= c && c <= 126) // || (128 <= c && c <= 255)
}

func waitPrompt() {
	fmt.Print(":")
	fmt.Scanln()
	fmt.Print("\033[A\033[2K")
}

func (t *terminal) ptyReadLoop() {

	// r := ptyReader{
	// 	t:        t,
	// 	buffer:   make([]byte, 1024),
	// 	curBytes: []byte{},
	// }
	r := &dupReader{
		reader: bufio.NewReader(t.pty),
		t:      t,
	}
	rr := &recordingReader{r: r}
	// r :=

	// i := 0

	for {
		// bs, err := r.advance(i)
		// if err != nil {
		// 	return
		// }

		b, err := r.ReadByte()
		if err != nil {
			fmt.Println(err)
			return
		}

		// printables
		runes := []rune{}
		for {
			// fmt.Println("byte:", b, string(b))
			if isPrintable(b) {
				runes = append(runes, rune(b))
			} else if b >= 128 && b <= 255 {
				r.UnreadByte()
				rn, _, err := r.ReadRune()
				if err != nil {
					fmt.Println(err)
					return
				}
				// fmt.Println("rune:", rn, string(rn))
				runes = append(runes, rn)
			} else {
				break
			}

			if r.Buffered() == 0 {
				break
			}

			// fmt.Println("r.Buffered():", r.Buffered())

			b, err = r.ReadByte()
			if err != nil {
				fmt.Println(err)
				return
			}
		}
		if len(runes) > 0 {
			t.screen.writeRunes(runes)
			if *debugTxt {
				fmt.Printf("\033[32mtxt: %#v\033[0m %v\n", string(runes), len(runes))
				waitPrompt()
			}

			if r.Buffered() == 0 {
				continue
			}
		}

		// ABCDEFGHIJKLMNOPQRSTUVWXYZ

		switch b {
		case 0: // NUL Null byte, ignore

		case 5: // ENQ ^E Return Terminal Status
			fmt.Println("ENQ")
		case 7: // BEL ^G Bell
			// fmt.Println("bell")
			t.frontend.Bell()

		case 8: // BS ^H Backspace
			t.screen.moveCursor(-1, 0, false, false)

		case 9: // HT ^I Horizontal TAB
			fmt.Println("tab")

		case 10: // LF ^J Linefeed (newline)
			// fmt.Println("linefeed")
			t.screen.moveCursor(0, 1, true, true)

		case 11: // VT ^K Vertical TAB
			fmt.Println("vtab")

		case 12: // FF ^L Formfeed (also: New page NP)
			// fmt.Println("formfeed")
			t.screen.moveCursor(0, 1, false, true)

		case 13: // CR ^M Carriage Return
			// fmt.Println("carriage return")
			t.screen.moveCursor(-t.screen.cursorPos.X, 0, true, true)

		case 27: // ESC ^[ Escape Character

			rr.StartRecording()
			success := t.handleCommand(rr)
			cmd := rr.StopRecording()

			if !success {
				fmt.Printf("Unhandled command: %#v\n", string(cmd))
			} else if *debugCmd {
				fmt.Printf("cmd: %#v\n", string(cmd))
				waitPrompt()
			}

			// b, err = r.ReadByte() // skip escape char
			// if err != nil {
			// 	fmt.Println(err)
			// 	return
			// }
			// var cmdBytes []byte
			//
			// if b == '[' || b == ']' {
			// 	cmdBytes = append(cmdBytes, b)
			// 	b, err = r.ReadByte()
			// 	if err != nil {
			// 		fmt.Println(err)
			// 		return
			// 	}
			// 	if b == '?' {
			// 		cmdBytes = append(cmdBytes, b)
			// 		b, err = r.ReadByte()
			// 		if err != nil {
			// 			fmt.Println(err)
			// 			return
			// 		}
			// 	}
			// 	for b == ';' || (b >= '0' && b <= '9') {
			// 		cmdBytes = append(cmdBytes, b)
			// 		b, err = r.ReadByte()
			// 		if err != nil {
			// 			fmt.Println(err)
			// 			return
			// 		}
			// 	}
			// 	cmdBytes = append(cmdBytes, b)
			// } else {
			// 	cmdBytes = append(cmdBytes, b)
			// }
			//
			// cmd := string(cmdBytes)
			// handled := t.handleCommand(cmd)
			// if !handled {
			// 	fmt.Printf("unhandled command: %#v\n", cmd)
			// } else if *debugCmd {
			// 	fmt.Printf("cmd: %#v\n", cmd)
			// }
			continue

		case 127: // DEL  Delete Character
			fmt.Println("delete character")
		default:
			fmt.Printf("unhandled char %v %#v\n", b, string(b))
			continue
		}

		fmt.Printf("byte: %v %#v\n", b, string(b))
		waitPrompt()
	}
}

func (t *terminal) handleCommand(r *recordingReader) bool {
	b, err := r.ReadByte()
	if err != nil {
		fmt.Println(err)
		return false
	}

	// short commands
	switch b {

	case 'c': // reset
		fmt.Println("cmd: reset") // TODO

	case 'D': // Index, scroll down if necessary
		t.screen.moveCursor(0, 1, false, true)

	case 'M': // Reverse index, scroll up if necessary
		t.screen.moveCursor(0, -1, false, true)

	case '[': // CSI Control Sequence Introducer
		return t.handleCmdCSI(r)

	case ']': // OSC Operating System Commands
		return t.handleCmdOSC(r)

	case '=': // Application Keypad
		fmt.Println("Application Keypad") // TODO

	case '>': // Normal Keypad
		fmt.Println("Normal Keypad") // TODO

	default:
		return false
	}
	return true
}

func (t *terminal) handleCmdCSI(r *recordingReader) bool {

	b, err := r.ReadByte()
	if err != nil {
		fmt.Println(err)
		return false
	}

	var prefix = []byte{}
	if b == '?' || b == '>' {
		prefix = append(prefix, b)

		b, err = r.ReadByte()
		if err != nil {
			fmt.Println(err)
			return false
		}
	}

	paramBytes := []byte{}
	for b == ';' || (b >= '0' && b <= '9') {
		paramBytes = append(paramBytes, b)

		b, err = r.ReadByte()
		if err != nil {
			fmt.Println(err)
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
			t.screen.moveCursor(0, -params[0], false, true)
		case 'B': // Move cursor down
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen.moveCursor(0, params[0], false, true)
		case 'C': // Move cursor forward
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen.moveCursor(params[0], 0, false, false)
		case 'D': // Move cursor backward
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen.moveCursor(-params[0], 0, false, false)

		case 'f', 'H': // Cursor Home
			x := 1
			y := 1
			if len(params) >= 1 {
				y = params[0]
			}
			if len(params) >= 2 {
				x = params[1]
			}
			// fmt.Printf("cursor home: %v, %v\n", x, y)
			t.screen.setCursorPos(x-1, y-1)

		case 'm': // Set color/mode
			if len(params) == 0 {
				params = []int{0}
			}

			fc := t.screen.frontColor
			bc := t.screen.backColor

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
							fmt.Println("unhandled extended color: ", params[i+1])
							continue
						}
					}

				case p >= 90 && p <= 97:
					fc = fc.SetColor(Color(p - 90 + 8))

				case p >= 100 && p <= 107:
					bc = bc.SetColor(Color(p - 100 + 8))

				default:
					fmt.Println("Unhandled set color: ", p)
					return false
				}

				// fmt.Printf("m %v: %x, %x\n", p, fc, bc)

				t.screen.setColors(fc, bc)
			}

		case 'K': // Erase
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// fmt.Println("erase: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to end of line
				t.screen.eraseRegion(Region{
					X:  t.screen.cursorPos.X,
					Y:  t.screen.cursorPos.Y,
					X2: t.screen.size.X,
					Y2: t.screen.cursorPos.Y + 1,
				})
			case params[0] == 1: // Erase to start of line
				t.screen.eraseRegion(Region{
					X:  0,
					Y:  t.screen.cursorPos.Y,
					X2: t.screen.cursorPos.X,
					Y2: t.screen.cursorPos.Y + 1,
				})
			case params[0] == 2: // Erase entire line
				t.screen.eraseRegion(Region{
					X:  0,
					Y:  t.screen.cursorPos.Y,
					X2: t.screen.size.X,
					Y2: t.screen.cursorPos.Y + 1,
				})
			default:
				fmt.Println("Unhandled K params: ", params)
				return false
			}

		case 'J': // Erase Lines
			// eraseRegion clamps the region to the window, so we don't have to be too careful here
			// fmt.Println("erase lines: ", params)

			switch {
			case len(params) == 0 || params[0] == 0: // Erase to bottom of screen
				t.screen.eraseRegion(Region{
					X:  0,
					Y:  t.screen.cursorPos.Y,
					X2: t.screen.size.X,
					Y2: t.screen.size.Y,
				})
			case params[0] == 1: // Erase to top of screen
				t.screen.eraseRegion(Region{
					X:  0,
					Y:  0,
					X2: t.screen.size.X,
					Y2: t.screen.cursorPos.Y,
				})
			case params[0] == 2: // Erase screen and home cursor
				t.screen.eraseRegion(Region{
					X:  0,
					Y:  0,
					X2: t.screen.size.X,
					Y2: t.screen.size.Y,
				})
				// t.screen.setCursorPos(0, 0)
			default:
				fmt.Println("Unhandled J params: ", params)
				return false
			}

		case 'M': // Delete lines, scroll up
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen.scroll(t.screen.topMargin, t.screen.cursorPos.Y, -params[0])

		case 'P': // Delete n characters
			if len(params) == 0 {
				params = []int{1}
			}
			t.screen.eraseRegion(Region{
				X:  t.screen.cursorPos.X,
				Y:  t.screen.cursorPos.Y,
				X2: t.screen.cursorPos.X + params[0],
				Y2: t.screen.cursorPos.Y + 1,
			})
			fmt.Println("Delete", params[0], "chars")

		case 'r': // Set Scroll margins
			top := 1
			bottom := t.screen.size.Y
			if len(params) >= 1 {
				top = params[0]
			}
			if len(params) >= 2 {
				bottom = params[1]
			}
			// fmt.Printf("cursor home: %v, %v\n", x, y)
			t.screen.setScrollMarginTopBottom(top-1, bottom-1)

		case 'n': // Device Status Report

		default:
			fmt.Printf("Unhandled CSI Command: %v %#v\n", params, string(b))
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
					fmt.Println("Application Cursor Keys =", value) // TODO

				case 12: // Blink Cursor
					fmt.Println("Blink Cursor =", value) // TODO

				case 25: // Show Cursor
					fmt.Println("Show Cursor =", value) // TODO

				case 1049: // Save/Restore cursor and alternate screen
					fmt.Println("Save Cursor and alternate screen =", value) // TODO

				case 2004: // Bracketed paste
					fmt.Println("Bracketed paste =", value) // TODO

				default:
					fmt.Printf("Unhandled flag: %#v %v, %v %#v\n", string(prefix), params, p, string(b))
				}
			}

		default:
			fmt.Printf("Unhandled ? command: %#v %v, %#v\n", string(prefix), params, string(b))
		}
	} else if string(prefix) == ">" {
		switch b {
		case 'c': // Send Device Attributes
			attrs := "\x1b[>1;4402;0c"
			n, err := t.pty.WriteString(attrs)
			if err != nil || n != len(attrs) {
				fmt.Println("Error sending device attrs:", err)
			}

		default:
			return false
		}
	} else {
		fmt.Printf("Unhandled prefix: %#v %v, %#v\n", string(prefix), params, string(b))
		return true
	}

	return true
}

func (t *terminal) handleCmdOSC(r *recordingReader) bool {
	paramBytes := []byte{}
	var err error
	var b byte = '9'
	for b >= '0' && b <= '9' {
		paramBytes = append(paramBytes, b)

		b, err = r.ReadByte()
		if err != nil {
			fmt.Println(err)
			return false
		}
	}

	param, _ := strconv.Atoi(string(paramBytes[1:]))

	// skip the ';'
	b, err = r.ReadByte()
	if err != nil {
		fmt.Println(err)
		return false
	}

	param2 := []byte{}
	for b != 7 && b != 0x9c { // BEL , ST
		if len(param2) > 0 && param2[len(param2)-1] == 27 && b == '\\' { // ESC \ is also ST
			break
		}
		param2 = append(param2, b)

		b, err = r.ReadByte()
		if err != nil {
			fmt.Println(err)
			return false
		}
	}

	switch param {
	case 0:
		fmt.Printf("window title: %#v\n", string(param2))

	default:
		fmt.Println("Unhandled OSC Command: ", param, string(b))
		return false
	}

	return true
}
