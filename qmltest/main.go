package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/limetext/qml-go"

	"github.com/ricochet1k/termemu"
)

var dupTo = flag.String("dup", "", "Duplicate all output to this file")

func main() {
	flag.Parse()

	absFile, _ := filepath.Abs(os.Args[0])
	os.Chdir(filepath.Dir(absFile))
	err := qml.Run(qmlLoop)
	fmt.Println(err)
}

var ctrl Ctrl

type Ctrl struct {
	termObj  qml.Object
	terminal termemu.Terminal
	frontend *qmlfrontend
}

func qmlLoop() error {
	engine := qml.NewEngine()
	engine.On("quit", func() { os.Exit(0) })
	// goctrl.engine = engine

	// engine.ClearImportPaths()
	engine.AddImportPath("qrc:///qml")
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	engine.AddImportPath(filepath.Join(dir, "qml"))
	engine.AddPluginPath(filepath.Join(dir, "plugins"))
	qml.AddLibraryPath(dir)
	qml.AddLibraryPath(filepath.Join(dir, "plugins"))

	controls, err := engine.LoadFile("qrc:///qml/main.qml")
	if err != nil {
		fmt.Println(err)
		var err2 error
		controls, err2 = engine.LoadFile("qml/main.qml")
		if err2 != nil {
			fmt.Println(err2)
			if strings.HasSuffix(err.Error(), "File not found") {
				return err2
			}
			return err
		}
	}

	context := engine.Context()
	context.SetVar("ctrl", &ctrl)

	window := controls.CreateWindow(nil)

	// goctrl.Window = window
	// goctrl.Root = window.Root()
	fmt.Fprintln(os.Stderr, "Yay", window, window.Root())
	window.Root().Call("initialize")

	rand.Seed(time.Now().Unix())

	window.Show()
	window.Wait()

	return nil
}

func (c *Ctrl) SetView(o qml.Object) {

	f := &qmlfrontend{}
	c.terminal = termemu.New(f)
	c.terminal.SetFrontend(f)
	f.t = c.terminal
	ctrl.terminal = c.terminal
	ctrl.frontend = f
	ctrl.termObj = o

	if *dupTo != "" {
		f, err := os.OpenFile(*dupTo, os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			fmt.Println(err)
		} else {
			// f.WriteString("\033[c")
			f.WriteString("\033[2J")
			f.WriteString("\033[;H")
			c.terminal.DupTo(f)
			fmt.Println("Duping to ", *dupTo, f)
		}
	}

	c.terminal.Resize(90, 25)

	go func() {
		cmd := exec.Command("bash", "-c", "echo Hi $TERM")
		c.terminal.StartCommand(cmd)

		err := cmd.Wait()
		if err != nil {
			fmt.Println(err)
		}

		args := flag.Args()

		cmd = exec.Command(args[0], args[1:]...)
		c.terminal.StartCommand(cmd)
	}()
}

func (c *Ctrl) KeyPressed(key string) {
	c.terminal.Write([]byte(key))
}

var specialKeyMap = map[string]string{
	"backspace":     "\x7f",    // \x08 = ASCII BS, \x7f = ASCII DEL
	"delete":        "\033[3~", // \x08 = ASCII BS, \x7f = ASCII DEL
	"up":            "\033[A",
	"down":          "\033[B",
	"left":          "\033[D",
	"right":         "\033[C",
	"F1":            "\033OP",
	"F2":            "\033OQ",
	"F3":            "\033OR",
	"F4":            "\033OS",
	"F5":            "\033[15~", // what is [16~ ?
	"F6":            "\033[17~",
	"F7":            "\033[18~",
	"F8":            "\033[19~",
	"F9":            "\033[20~",
	"F10":           "\033[21~", // where is [22~ ?
	"F11":           "\033[23~",
	"F12":           "\033[24~",
	"home":          "\033[H",
	"end":           "\033[F",
	"pgup":          "\033[5~",
	"pgdown":        "\033[6~",
	"numpad1":       "\033Oq",
	"numpad2":       "\033Or",
	"numpad3":       "\033Os",
	"numpad4":       "\033Ot",
	"numpad5":       "\033Ou",
	"numpad6":       "\033Ov",
	"numpad7":       "\033Ow",
	"numpad8":       "\033Ox",
	"numpad9":       "\033Oy",
	"numpad0":       "\033Op",
	"numpad.":       "\033On",
	"numpad/":       "\033OQ",
	"numpad*":       "\033OR",
	"numpad+":       "\033Ol",
	"numpad-":       "\033OS",
	"numpad<Enter>": "\033OM",
}

func (c *Ctrl) SpecialKeyPressed(key string) {
	seq, ok := specialKeyMap[key]
	if !ok {
		fmt.Println("Unhandled special key: ", key)
	}
	c.terminal.Write([]byte(seq))
}

func (c *Ctrl) RedrawAll() {
	if c.terminal == nil {
		return
	}
	w, h := c.terminal.Size()
	c.frontend.RegionChanged(termemu.Region{X: 0, Y: 0, X2: w, Y2: h}, termemu.CRRedraw)
}

type qmlfrontend struct {
	termemu.EmptyFrontend
	t termemu.Terminal
}

func (f *qmlfrontend) Bell() {
	fmt.Println("bell")
}

func (f *qmlfrontend) CursorMoved(x, y int) {
	ctrl.termObj.Call("cursorMoved", x, y)
}

func (f *qmlfrontend) RegionChanged(r termemu.Region, cr termemu.ChangeReason) {
	for y := r.Y; y < r.Y2; y++ {
		line := ctrl.terminal.Line(y)
		fgColors, bgColors := ctrl.terminal.LineColors(y)
		x := r.X
		for x < r.X2 {

			fg := fgColors[x]
			bg := bgColors[x]

			x2 := x + 1
			for x2 < r.X2 && fg == fgColors[x2] && bg == bgColors[x2] {
				x2++
			}

			// if len(line[x:x2]) != len([]byte(string(line[x:x2]))) {
			// 	fmt.Println("Region: ", x, y, x2-x, string(line[x:x2]), line[x:x2], len(line[x:x2]), len(string(line[x:x2])), len([]byte(string(line[x:x2]))))
			// }

			ctrl.termObj.Call("regionChanged", x, y, x2-x, string(line[x:x2]), int64(fg), int64(bg))
			x = x2
		}
	}
}

func (f *qmlfrontend) ColorsChanged(fg termemu.Color, bg termemu.Color) {
	if ctrl.termObj == nil {
		return
	}
	ctrl.termObj.Call("colorsChanged", int64(fg), int64(bg))
}
