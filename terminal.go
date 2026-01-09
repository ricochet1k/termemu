package termemu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Terminal is a wrapper around a pty that sends rendering commands
// to a provided Frontend.
type Terminal interface {
	SetFrontend(f Frontend)

	Lock()
	Unlock()
	WithLock(func())

	StartCommand(c *exec.Cmd) error
	Write(b []byte) (int, error)
	Size() (int, int)
	Resize(int, int) error
	Line(int) []rune
	ANSILine(y int) string
	LineColors(int) ([]Color, []Color)
	StyledLine(x, w, y int) *Line
	StyledLines(r Region) []*Line
	DupTo(*os.File)

	PrintTerminal() // for debugging
}

type terminal struct {
	sync.Mutex

	frontend    Frontend
	onAltScreen bool
	mainScreen  *screen
	altScreen   *screen

	backend Backend

	pty, tty *os.File
	dup      *os.File

	viewFlags   []bool
	viewInts    []int
	viewStrings []string

	readLoopStarted bool
}

// New makes a new terminal using the provided Frontend
func New(f Frontend) Terminal {
	return NewWithBackend(f, PTYBackend{})
}

// NewWithBackend makes a new terminal using the provided Frontend and Backend.
func NewWithBackend(f Frontend, backend Backend) Terminal {
	if f == nil {
		f = &EmptyFrontend{}
	}
	if backend == nil {
		backend = PTYBackend{}
	}

	return &terminal{
		frontend:    f,
		mainScreen:  newScreen(f),
		altScreen:   newScreen(f),
		backend:     backend,
		viewFlags:   make([]bool, viewFlagCount),
		viewInts:    make([]int, viewIntCount),
		viewStrings: make([]string, viewStringCount),
	}
}

func (t *terminal) SetFrontend(f Frontend) {
	t.frontend = f
	t.mainScreen.frontend = f
	t.altScreen.frontend = f
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

func (t *terminal) StartCommand(c *exec.Cmd) error {
	if t.backend == nil {
		t.backend = PTYBackend{}
	}
	if t.pty != nil {
		return errors.New("pty already initialized; start command before writing")
	}

	if c.Env == nil {
		c.Env = os.Environ()
	}

	found := false
	for i, v := range c.Env {
		if strings.HasPrefix(v, "TERM=") {
			found = true
			c.Env[i] = termStr
			break
		}
	}
	if !found {
		c.Env = append(c.Env, termStr)
	}

	var err error
	t.pty, t.tty, err = t.backend.Start(c)
	if err != nil {
		return err
	}

	t.startReadLoop()
	w, h := t.Size()
	_ = t.backend.Setsize(t.pty, w, h)

	return nil
}

func (t *terminal) Write(b []byte) (int, error) {
	if err := t.ensurePTYOpen(); err != nil {
		return 0, err
	}
	return t.pty.Write(b)
}

func (t *terminal) Size() (w, h int) {
	return t.screen().size.X, t.screen().size.Y
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

	if t.pty == nil {
		return nil
	}

	return t.backend.Setsize(t.pty, w, h)
}

func (t *terminal) ensurePTYOpen() error {
	if t.pty != nil {
		return nil
	}
	if t.backend == nil {
		t.backend = PTYBackend{}
	}

	var err error
	t.pty, t.tty, err = t.backend.Open()
	if err != nil {
		return err
	}

	t.startReadLoop()
	w, h := t.Size()
	_ = t.backend.Setsize(t.pty, w, h)

	return nil
}

func (t *terminal) startReadLoop() {
	if t.readLoopStarted {
		return
	}
	t.readLoopStarted = true
	go t.ptyReadLoop()
}

func (t *terminal) Line(y int) []rune {
	if y >= t.screen().size.Y {
		return nil
	}
	return t.screen().getLine(y)
}

func (t *terminal) ANSILine(y int) string {
	if y >= t.screen().size.Y {
		return ""
	}
	return t.screen().renderLineANSI(y)
}

func (t *terminal) LineColors(y int) (fg []Color, bg []Color) {
	if y >= t.screen().size.Y {
		return nil, nil
	}
	return t.screen().getLineColors(y)
}

func (t *terminal) StyledLine(x, w, y int) *Line {
	return t.screen().StyledLine(x, w, y)
}

func (t *terminal) StyledLines(r Region) []*Line {
	return t.screen().StyledLines(r)
}

func (t *terminal) DupTo(f *os.File) {
	t.dup = f
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
func (t *terminal) SendMouseRaw(btn MouseBtn, press bool, mods MouseFlag, x, y int) {
	switch t.viewInts[VIMouseMode] {
	case MMNone:
		return
	case MMPress:
		if !press {
			return
		}
	case MMPressRelease:
		if mods&MMotion != 0 {
			return
		}
	case MMPressReleaseMove:
		if byte(mods)&mWhichBtn == byte(MRelease) {
			return
		}
	case MMPressReleaseMoveAll:
	}

	switch t.viewInts[VIMouseEncoding] {
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

		t.Write([]byte("\033[M" + string(32+btnByte) + string(byte(32+x)) + string(byte(32+y))))

	case MEUTF8:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}

		t.Write([]byte("\033[M" + string(32+btnByte) + string(rune(32+x)) + string(rune(32+y))))

	case MESGR:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		pressByte := 'M'
		if !press {
			pressByte = 'm'
		}
		t.Write([]byte(fmt.Sprintf("\033[<%v;%v;%v%c", btnByte, x, y, pressByte)))
	}
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
func (t *terminal) screen() *screen {
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
	t.frontend.RegionChanged(Region{X: 0, Y: 0, X2: t.screen().size.X, Y2: t.screen().size.Y}, CRScreenSwitch)
}
