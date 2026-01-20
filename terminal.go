package termemu

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

type Terminal interface {
	SetFrontend(f Frontend)

	Lock()
	Unlock()
	WithLock(func())

	Write(b []byte) (int, error)
	SendKey(KeyEvent) (int, error)
	Size() (int, int)
	Resize(int, int) error
	Line(int) string
	ANSILine(y int) string
	StyledLine(x, w, y int) Line
	StyledLines(r Region) []Line

	PrintTerminal() // for debugging
}

type terminal struct {
	sync.Mutex

	frontend    Frontend
	onAltScreen bool
	mainScreen  screen
	altScreen   screen

	backend Backend

	viewFlags   []bool
	viewInts    []int
	viewStrings []string

	readLoopStarted bool
	readLoopDone    chan struct{}
	textReadMode    TextReadMode

	keyboardMain keyboardMode
	keyboardAlt  keyboardMode
}

// New makes a new terminal using the provided Frontend, Backend, and default text read mode.
func New(f Frontend, backend Backend) Terminal {
	return NewWithMode(f, backend, TextReadModeRune)
}

// NewWithBackend makes a new terminal using the provided Frontend and Backend.
func NewWithBackend(f Frontend, backend Backend) Terminal {
	return NewWithMode(f, backend, TextReadModeRune)
}

// NewWithMode makes a new terminal using the provided Frontend, Backend, and text read mode.
func NewWithMode(f Frontend, backend Backend, mode TextReadMode) Terminal {
	if f == nil {
		f = &EmptyFrontend{}
	}
	if backend == nil {
		return nil
	}

	t := &terminal{
		frontend:     f,
		mainScreen:   newScreen(f),
		altScreen:    newScreen(f),
		backend:      backend,
		viewFlags:    make([]bool, viewFlagCount),
		viewInts:     make([]int, viewIntCount),
		viewStrings:  make([]string, viewStringCount),
		textReadMode: mode,
	}
	t.startReadLoop()
	return t
}

func (t *terminal) SetFrontend(f Frontend) {
	t.frontend = f
	t.mainScreen.SetFrontend(f)
	t.altScreen.SetFrontend(f)
}

// func NewNoPTY(f Frontend) Terminal {
// 	return &terminal{
// 		frontend: f,
// 		screen: screen{
// 			frontend: f,
// 		},
// 	}
// }

const termStr = "TERM=xterm-256color"

// Write is for the client to write keyboard/mouse input or terminal feedback
func (t *terminal) Write(b []byte) (int, error) {
	if t.backend == nil {
		return 0, errors.New("backend is nil")
	}
	total := 0
	for len(b) > 0 {
		n, err := t.backend.Write(b)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		b = b[n:]
	}
	return total, nil
}

// Size returns the terminal width and height.
// The caller must ensure the terminal is locked before calling this method.
func (t *terminal) Size() (w, h int) {
	size := t.screen().Size()
	return size.X, size.Y
}

type winsize struct {
	wsRow    uint16
	wsCol    uint16
	wsXPixel uint16
	wsYPixel uint16
}

func (t *terminal) Resize(w, h int) error {
	t.mainScreen.setSize(w, h)
	t.altScreen.setSize(w, h)

	if t.backend == nil {
		return nil
	}

	return t.backend.SetSize(w, h)
}

func (t *terminal) startReadLoop() {
	if t.readLoopStarted {
		return
	}
	if t.backend == nil {
		return
	}
	t.readLoopStarted = true
	if t.readLoopDone == nil {
		t.readLoopDone = make(chan struct{})
	}
	go func() {
		defer close(t.readLoopDone)
		t.ptyReadLoop()
	}()
}

// Line returns the plain text content of line y.
// The caller must lock the terminal before calling this method.
func (t *terminal) Line(y int) string {
	size := t.screen().Size()
	if y >= size.Y {
		return ""
	}
	return t.screen().StyledLine(0, size.X, y).PlainTextString()
}

// ANSILine returns the ANSI-escaped content of line y.
// The caller must lock the terminal before calling this method.
func (t *terminal) ANSILine(y int) string {
	if y >= t.screen().Size().Y {
		return ""
	}
	return t.screen().renderLineANSI(y)
}

// StyledLine returns the styled content of a line region.
// The caller must lock the terminal before calling this method.
func (t *terminal) StyledLine(x, w, y int) Line {
	return t.screen().StyledLine(x, w, y)
}

// StyledLines returns the styled content of multiple lines in a region.
// The caller must lock the terminal before calling this method.
func (t *terminal) StyledLines(r Region) []Line {
	return t.screen().StyledLines(r)
}

func (t *terminal) PrintTerminal() {
	t.screen().printScreen()
}

type MouseBtn byte
type MouseFlag byte

const (
	// low 2 bits
	MBtn1     MouseBtn = 0
	MBtn2     MouseBtn = 1
	MBtn3     MouseBtn = 2
	MRelease  MouseBtn = 3
	mWhichBtn byte     = 3

	// flags
	MShift   MouseFlag = 4
	MMeta    MouseFlag = 8
	MControl MouseFlag = 16
	MMotion  MouseFlag = 32
	MWheel   MouseFlag = 64
)

// x and y should start at 1
// wheel events should use btn1 for wheel up, btn2 for wheel down, true for press, and M_wheel for mods
func (t *terminal) SendMouseRaw(btn MouseBtn, press bool, mods MouseFlag, x, y int) error {
	switch t.viewInts[VIMouseMode] {
	case MMNone:
		return nil
	case MMPress:
		if !press {
			return nil
		}
	case MMPressRelease:
		if mods&MMotion != 0 {
			return nil
		}
	case MMPressReleaseMove:
		if byte(mods)&mWhichBtn == byte(MRelease) {
			return nil
		}
	case MMPressReleaseMoveAll:
	}

	mouseEncoding := t.viewInts[VIMouseEncoding]
	switch mouseEncoding {
	case MEX10:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}

		if 32+x > 255 {
			x = 255 - 32
		}
		if 32+y > 255 {
			y = 255 - 32
		}

		mouseCmd := []byte("\033[M" + string(32+btnByte) + string(byte(32+x)) + string(byte(32+y)))
		_, err := t.Write(mouseCmd)
		if err != nil {
			panic(fmt.Sprintf("error %v", err))
		}
		return err

	case MEUTF8:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}

		_, err := t.Write([]byte("\033[M" + string(32+btnByte) + string(rune(32+x)) + string(rune(32+y))))
		return err

	case MESGR:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		pressByte := 'M'
		if !press {
			pressByte = 'm'
		}
		_, err := fmt.Fprintf(t, "\033[<%v;%v;%v%c", btnByte, x, y, pressByte)
		return err
	}
	panic(fmt.Sprintf("Unhandled ViMouseEncoding?? %v", mouseEncoding))
}

func (t *terminal) setViewFlag(flag ViewFlag, value bool) {
	t.viewFlags[flag] = value
	t.frontend.ViewFlagChanged(flag, value)
}
func (t *terminal) GetViewFlag(flag ViewFlag) bool {
	return t.viewFlags[flag]
}
func (t *terminal) setViewInt(flag ViewInt, value int) {
	t.viewInts[flag] = value
	t.frontend.ViewIntChanged(flag, value)
}
func (t *terminal) GetViewInt(flag ViewInt) int {
	return t.viewInts[flag]
}
func (t *terminal) setViewString(flag ViewString, value string) {
	t.viewStrings[flag] = value
	t.frontend.ViewStringChanged(flag, value)
}
func (t *terminal) GetViewString(flag ViewString) string {
	return t.viewStrings[flag]
}

// returns screen and unlock fn, must defer the unlock fn
func (t *terminal) screen() screen {
	if t.onAltScreen {
		return t.altScreen
	}
	return t.mainScreen
}

// calls f with screen locked
func (t *terminal) WithLock(f func()) {
	t.Lock()
	defer t.Unlock()
	f()
}

func (t *terminal) switchScreen() {
	t.onAltScreen = !t.onAltScreen
	size := t.screen().Size()
	t.frontend.RegionChanged(Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRScreenSwitch)
}

// testHandleCommand is only for testing
func (t *terminal) testHandleCommand(te *testing.T, cmd string) {
	if !t.handleCommand(bufio.NewReader(strings.NewReader(cmd))) {
		te.Fatalf("handleCommand %q failed", cmd)
	}
}
