package termemu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/kr/pty"
)

type Terminal interface {
	SetFrontend(f Frontend)

	StartCommand(c *exec.Cmd) error
	Write(b []byte) (int, error)
	Size() (int, int)
	Resize(int, int) error
	Line(int) []rune
	ANSILine(y int) string
	LineColors(int) ([]Color, []Color)
	DupTo(*os.File)

	PrintTerminal() // for debugging
}

type terminal struct {
	frontend Frontend
	screen   *screen

	pty, tty *os.File
	dup      *os.File

	viewFlags   []bool
	viewInts    []int
	viewStrings []string
}

func New(f Frontend) Terminal {

	t := &terminal{
		frontend:    f,
		screen:      newScreen(f),
		viewFlags:   make([]bool, viewFlagCount),
		viewInts:    make([]int, viewIntCount),
		viewStrings: make([]string, viewStringCount),
	}

	var err error

	t.pty, t.tty, err = pty.Open()
	if err != nil {
		return nil
	}

	t.Resize(t.Size())

	go t.ptyReadLoop()

	return t
}

func (t *terminal) SetFrontend(f Frontend) {
	t.frontend = f
	t.screen.frontend = f
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
	if t.pty == nil {
		return errors.New("pty not initialized")
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

	c.Stdout = t.tty
	c.Stdin = t.tty
	c.Stderr = t.tty
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setctty = true
	c.SysProcAttr.Setsid = true
	c.SysProcAttr.Ctty = int(t.tty.Fd())
	err := c.Start()
	if err != nil {
		return err
	}

	return nil
}

func (t *terminal) Write(b []byte) (int, error) {
	return t.pty.Write(b)
}

func (t *terminal) Size() (w, h int) {
	return t.screen.size.X, t.screen.size.Y
}

type winsize struct {
	ws_row    uint16
	ws_col    uint16
	ws_xpixel uint16
	ws_ypixel uint16
}

func (t *terminal) Resize(w, h int) error {

	ws := winsize{
		ws_row:    uint16(h),
		ws_col:    uint16(w),
		ws_xpixel: uint16(w * 8),
		ws_ypixel: uint16(h * 16),
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		t.pty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return syscall.Errno(errno)
	}

	t.screen.setSize(w, h)

	return nil
}

func (t *terminal) Line(y int) []rune {
	if y >= t.screen.size.Y {
		return nil
	}
	return t.screen.getLine(y)
}

func (t *terminal) ANSILine(y int) string {
	if y >= t.screen.size.Y {
		return ""
	}
	return t.screen.renderLineANSI(y)
}

func (t *terminal) LineColors(y int) (fg []Color, bg []Color) {
	if y >= t.screen.size.Y {
		return nil, nil
	}
	return t.screen.getLineColors(y)
}

func (t *terminal) DupTo(f *os.File) {
	t.dup = f
}

func (t *terminal) PrintTerminal() {
	t.screen.printScreen()
}

const (
	// low 2 bits
	M_btn1     = 0
	M_btn2     = 1
	M_btn3     = 2
	M_release  = 3
	M_whichBtn = 3

	// flags
	M_shift   = 4
	M_meta    = 8
	M_control = 16
	M_motion  = 32
	M_wheel   = 64
)

// x and y should start at 1
// wheel events should use btn1 for wheel up, btn2 for wheel down, true for press, and M_wheel for mods
func (t *terminal) SendMouseRaw(btn byte, press bool, mods byte, x, y int) {
	switch t.viewInts[VI_MouseMode] {
	case MM_None:
		return
	case MM_Press:
		if !press {
			return
		}
	case MM_PressRelease:
		if mods&M_motion != 0 {
			return
		}
	case MM_PressReleaseMove:
		if mods&M_whichBtn == M_release {
			return
		}
	case MM_PressReleaseMoveAll:
	}

	switch t.viewInts[VI_MouseEncoding] {
	case ME_X10:
		btnByte := (btn & M_whichBtn) | mods
		if !press {
			btnByte |= M_release
		}

		if 32+x > 255 {
			x = 255 - 32
		}
		if 32+y > 255 {
			y = 255 - 32
		}

		t.Write([]byte("\033[M" + string(32+btnByte) + string(byte(32+x)) + string(byte(32+y))))

	case ME_UTF8:
		btnByte := (btn & M_whichBtn) | mods
		if !press {
			btnByte |= M_release
		}

		t.Write([]byte("\033[M" + string(32+btnByte) + string(rune(32+x)) + string(rune(32+y))))

	case ME_SGR:
		btnByte := (btn & M_whichBtn) | mods
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
