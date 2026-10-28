// Command gioui runs a command under a PTY and displays it in a Gio window.
//
// It uses Gio for the window, input, and text entry (so IME input and
// system-provided clipboard handling work), and blits a CPU-rendered frame from
// examples/internal/raster. That keeps the example small: Gio's own text stack
// is used only for input, not for drawing the grid.
//
//	go run . -- sh
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"os/exec"
	"sync"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/ricochet1k/termemu"
	"github.com/ricochet1k/termemu/examples/internal/raster"
)

func main() {
	fontSize := flag.Float64("font-size", 16, "font size in pixels")
	cols := flag.Int("cols", 80, "initial terminal width in cells")
	rows := flag.Int("rows", 24, "initial terminal height in cells")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"sh"}
	}
	if err := run(*fontSize, *cols, *rows, args); err != nil {
		log.Fatal(err)
	}
}

func run(fontSize float64, cols, rows int, args []string) error {
	renderer, err := raster.New(fontSize)
	if err != nil {
		return err
	}
	defer func() { _ = renderer.Close() }()
	cellW, cellH := renderer.CellSize()

	cmd := exec.Command(args[0], args[1:]...)
	backend := &termemu.PTYBackend{}
	if err := backend.Attach(cmd); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	var window app.Window
	window.Option(app.Title("termemu (gio)"))

	frontend := newGioFrontend(renderer, cols*cellW, rows*cellH)
	frontend.wake = window.Invalidate

	term := termemu.NewWithMode(frontend, backend, termemu.TextReadModeGrapheme)
	if err := term.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize: %w", err)
	}

	// Close the terminal when the child exits: PTYBackend waits for the next
	// command after EOF/EIO, so the read loop only stops when the backend is
	// closed.
	go func() {
		_ = cmd.Wait()
		_ = term.Close()
	}()
	// Leave the app when the terminal stops (the child exited or the backend
	// failed). Perform is safe to call from another goroutine.
	go func() {
		<-term.Done()
		window.Perform(system.ActionClose)
	}()

	tag := new(bool) // unique focus tag
	curCols, curRows := 0, 0
	var ops op.Ops

	for {
		switch ev := window.Event().(type) {
		case app.DestroyEvent:
			_ = term.Close()
			return ev.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, ev)

			if c, r := ev.Size.X/cellW, ev.Size.Y/cellH; c > 0 && r > 0 && (c != curCols || r != curRows) {
				curCols, curRows = c, r
				frontend.setSize(c*cellW, r*cellH)
				if err := term.Resize(c, r); err != nil {
					log.Printf("resize: %v", err)
				}
			}

			handleInput(gtx, tag, term)

			paint.Fill(gtx.Ops, color.NRGBA{A: 0xff})
			img := frontend.image()
			size := img.Bounds().Size()
			rect := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
			paint.NewImageOp(img).Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			rect.Pop()

			ev.Frame(gtx.Ops)
		}
	}
}

// handleInput drains this frame's events and forwards them to the terminal.
//
// Text arrives as key.EditEvent (so the platform input method handles dead
// keys, compose sequences, and IME); key.Event is used for functional keys and
// for modifier combinations, which are not text.
func handleInput(gtx layout.Context, tag event.Tag, term termemu.Terminal) {
	event.Op(gtx.Ops, tag)
	key.InputHintOp{Tag: tag, Hint: key.HintAny}.Add(gtx.Ops)
	if !gtx.Focused(tag) {
		gtx.Execute(key.FocusCmd{Tag: tag})
	}
	filters := []event.Filter{
		key.FocusFilter{Target: tag},
		key.Filter{Focus: tag, Optional: key.ModShift | key.ModCtrl | key.ModCommand | key.ModAlt | key.ModSuper},
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		switch ev := ev.(type) {
		case key.EditEvent:
			_, _ = term.Write([]byte(ev.Text))
		case key.Event:
			if ev.State == key.Release {
				continue
			}
			if ke, ok := keyEvent(ev); ok {
				_, _ = term.SendKey(ke)
			}
		}
	}
}

// keyCodes maps Gio's functional key names to termemu key codes.
var keyCodes = map[key.Name]termemu.KeyCode{
	key.NameEnter:          termemu.KeyEnter,
	key.NameReturn:         termemu.KeyEnter,
	key.NameTab:            termemu.KeyTab,
	key.NameDeleteBackward: termemu.KeyBackspace,
	key.NameDeleteForward:  termemu.KeyDelete,
	key.NameEscape:         termemu.KeyEscape,
	key.NameUpArrow:        termemu.KeyUp,
	key.NameDownArrow:      termemu.KeyDown,
	key.NameLeftArrow:      termemu.KeyLeft,
	key.NameRightArrow:     termemu.KeyRight,
	key.NameHome:           termemu.KeyHome,
	key.NameEnd:            termemu.KeyEnd,
	key.NamePageUp:         termemu.KeyPageUp,
	key.NamePageDown:       termemu.KeyPageDown,
	key.NameF1:             termemu.KeyF1,
	key.NameF2:             termemu.KeyF2,
	key.NameF3:             termemu.KeyF3,
	key.NameF4:             termemu.KeyF4,
	key.NameF5:             termemu.KeyF5,
	key.NameF6:             termemu.KeyF6,
	key.NameF7:             termemu.KeyF7,
	key.NameF8:             termemu.KeyF8,
	key.NameF9:             termemu.KeyF9,
	key.NameF10:            termemu.KeyF10,
	key.NameF11:            termemu.KeyF11,
	key.NameF12:            termemu.KeyF12,
}

// keyEvent converts a Gio key event to a termemu key event. Text-producing
// keys without a modifier are reported as false: the platform sends those as
// key.EditEvent, and handling both would duplicate every character.
func keyEvent(ev key.Event) (termemu.KeyEvent, bool) {
	mod := keyMod(ev.Modifiers)
	if code, ok := keyCodes[ev.Name]; ok {
		return termemu.KeyEvent{Code: code, Mod: mod}, true
	}
	runes := []rune(string(ev.Name))
	if len(runes) != 1 {
		return termemu.KeyEvent{}, false
	}
	r := runes[0]
	if r >= 'A' && r <= 'Z' {
		r = r - 'A' + 'a'
	}
	if mod&(termemu.ModCtrl|termemu.ModAlt|termemu.ModSuper) == 0 {
		return termemu.KeyEvent{}, false
	}
	return termemu.KeyEvent{Code: termemu.KeyRune, Rune: r, Mod: mod}, true
}

func keyMod(m key.Modifiers) termemu.KeyMod {
	var out termemu.KeyMod
	if m.Contain(key.ModShift) {
		out |= termemu.ModShift
	}
	if m.Contain(key.ModAlt) {
		out |= termemu.ModAlt
	}
	if m.Contain(key.ModCtrl) {
		out |= termemu.ModCtrl
	}
	if m.Contain(key.ModCommand) || m.Contain(key.ModSuper) {
		out |= termemu.ModSuper
	}
	return out
}

// gioFrontend implements termemu.BatchFrontend by rendering each batch into a
// back buffer and swapping it with the front buffer the UI thread reads. The
// swap happens in WriteBatch, which runs without the terminal lock.
type gioFrontend struct {
	termemu.EmptyFrontend

	renderer *raster.Renderer
	wake     func()

	mu            sync.Mutex
	front, back   *image.RGBA
	width, height int
}

func newGioFrontend(renderer *raster.Renderer, width, height int) *gioFrontend {
	f := &gioFrontend{renderer: renderer}
	f.setSize(width, height)
	return f
}

// setSize reallocates the buffers when the window size changes. It must not be
// called while a frame is using the front buffer.
func (f *gioFrontend) setSize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if width == f.width && height == f.height {
		return
	}
	f.width, f.height = width, height
	f.front = image.NewRGBA(image.Rect(0, 0, width, height))
	f.back = image.NewRGBA(image.Rect(0, 0, width, height))
}

// PrepareBatch runs with the terminal lock held.
func (f *gioFrontend) PrepareBatch(v termemu.LockedView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renderer.Draw(f.back, v)
}

// WriteBatch runs without the terminal lock: swap buffers and ask the UI
// thread for a frame.
func (f *gioFrontend) WriteBatch() {
	f.mu.Lock()
	f.front, f.back = f.back, f.front
	f.mu.Unlock()
	if f.wake != nil {
		f.wake()
	}
}

// image returns the most recently completed frame.
func (f *gioFrontend) image() *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.front
}
