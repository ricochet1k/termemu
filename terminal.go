package termemu

import (
	"errors"
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
}

func New() Terminal {

	f := &dummyFrontend{}

	t := &terminal{
		frontend: f,
		screen:   newScreen(f),
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
