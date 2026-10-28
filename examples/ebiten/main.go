// Command ebiten runs a command under a PTY and displays it in an Ebiten
// window.
//
// Ebiten is a game engine, so the frame loop is Update/Draw rather than events:
// the terminal read loop renders into a CPU back buffer, and Draw uploads the
// latest completed frame as a texture. That keeps the interesting part (the
// grid) in examples/internal/raster instead of duplicating it with Ebiten's
// text stack.
//
//	go run . -- sh
package main

import (
	"flag"
	"image"
	"log"
	"os/exec"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

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

	renderer, err := raster.New(*fontSize)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = renderer.Close() }()
	cellW, cellH := renderer.CellSize()

	cmd := exec.Command(args[0], args[1:]...)
	backend := &termemu.PTYBackend{}
	if err := backend.Attach(cmd); err != nil {
		log.Fatalf("attach: %v", err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}

	frontend := newEbitenFrontend(renderer, *cols*cellW, *rows*cellH)
	term := termemu.NewWithMode(frontend, backend, termemu.TextReadModeGrapheme)
	if err := term.Resize(*cols, *rows); err != nil {
		log.Fatalf("resize: %v", err)
	}
	go func() {
		_ = cmd.Wait()
		_ = term.Close()
	}()

	ebiten.SetWindowTitle("termemu (ebiten)")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(*cols*cellW, *rows*cellH)
	if err := ebiten.RunGame(&game{term: term, frontend: frontend, cellW: cellW, cellH: cellH}); err != nil {
		log.Fatal(err)
	}
}

// game is the Ebiten game loop.
type game struct {
	term     termemu.Terminal
	frontend *ebitenFrontend
	cellW    int
	cellH    int

	// tex is the uploaded copy of the last frame; gen tracks which frontend
	// generation it came from. Only Update/Draw touch these fields.
	tex        *ebiten.Image
	texGen     uint64
	cols, rows int
}

// Layout makes the logical screen match the window, so a resize changes the
// terminal size in cells.
func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func (g *game) Update() error {
	select {
	case <-g.term.Done():
		return ebiten.Termination
	default:
	}

	w, h := ebiten.WindowSize()
	if cols, rows := w/g.cellW, h/g.cellH; cols > 0 && rows > 0 && (cols != g.cols || rows != g.rows) {
		g.cols, g.rows = cols, rows
		g.frontend.setSize(cols*g.cellW, rows*g.cellH)
		if err := g.term.Resize(cols, rows); err != nil {
			log.Printf("resize: %v", err)
		}
	}

	mod := ebitenMod()
	for _, k := range inpututil.AppendJustPressedKeys(nil) {
		if code, ok := keyCodes[k]; ok {
			_, _ = g.term.SendKey(termemu.KeyEvent{Code: code, Mod: mod})
			continue
		}
		if r, ok := letter(k); ok && mod&(termemu.ModCtrl|termemu.ModAlt|termemu.ModSuper) != 0 {
			_, _ = g.term.SendKey(termemu.KeyEvent{Code: termemu.KeyRune, Rune: r, Mod: mod})
		}
	}
	if runes := ebiten.AppendInputChars(nil); len(runes) > 0 {
		_, _ = g.term.Write([]byte(string(runes)))
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	front, gen := g.frontend.frame()
	if front == nil {
		return
	}
	if g.tex == nil || gen != g.texGen {
		g.tex = ebiten.NewImageFromImage(front)
		g.texGen = gen
	}
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterNearest}
	screen.DrawImage(g.tex, op)
}

// ebitenMod reports the currently held modifiers.
func ebitenMod() termemu.KeyMod {
	var mod termemu.KeyMod
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		mod |= termemu.ModShift
	}
	if ebiten.IsKeyPressed(ebiten.KeyControl) {
		mod |= termemu.ModCtrl
	}
	if ebiten.IsKeyPressed(ebiten.KeyAlt) {
		mod |= termemu.ModAlt
	}
	if ebiten.IsKeyPressed(ebiten.KeyMeta) {
		mod |= termemu.ModSuper
	}
	return mod
}

// letter returns the lowercase letter for an A-Z key.
func letter(k ebiten.Key) (rune, bool) {
	if k < ebiten.KeyA || k > ebiten.KeyZ {
		return 0, false
	}
	return rune('a' + int(k-ebiten.KeyA)), true
}

// keyCodes maps Ebiten's functional keys to termemu key codes.
var keyCodes = map[ebiten.Key]termemu.KeyCode{
	ebiten.KeyEnter:       termemu.KeyEnter,
	ebiten.KeyNumpadEnter: termemu.KeyEnter,
	ebiten.KeyTab:         termemu.KeyTab,
	ebiten.KeyBackspace:   termemu.KeyBackspace,
	ebiten.KeyDelete:      termemu.KeyDelete,
	ebiten.KeyEscape:      termemu.KeyEscape,
	ebiten.KeyArrowUp:     termemu.KeyUp,
	ebiten.KeyArrowDown:   termemu.KeyDown,
	ebiten.KeyArrowLeft:   termemu.KeyLeft,
	ebiten.KeyArrowRight:  termemu.KeyRight,
	ebiten.KeyHome:        termemu.KeyHome,
	ebiten.KeyEnd:         termemu.KeyEnd,
	ebiten.KeyPageUp:      termemu.KeyPageUp,
	ebiten.KeyPageDown:    termemu.KeyPageDown,
	ebiten.KeyF1:          termemu.KeyF1,
	ebiten.KeyF2:          termemu.KeyF2,
	ebiten.KeyF3:          termemu.KeyF3,
	ebiten.KeyF4:          termemu.KeyF4,
	ebiten.KeyF5:          termemu.KeyF5,
	ebiten.KeyF6:          termemu.KeyF6,
	ebiten.KeyF7:          termemu.KeyF7,
	ebiten.KeyF8:          termemu.KeyF8,
	ebiten.KeyF9:          termemu.KeyF9,
	ebiten.KeyF10:         termemu.KeyF10,
	ebiten.KeyF11:         termemu.KeyF11,
	ebiten.KeyF12:         termemu.KeyF12,
}

// ebitenFrontend implements termemu.BatchFrontend. Batches render into a back
// buffer; WriteBatch swaps it with the front buffer and bumps the generation
// counter the game loop watches.
type ebitenFrontend struct {
	termemu.EmptyFrontend

	renderer *raster.Renderer

	mu            sync.Mutex
	front, back   *image.RGBA
	width, height int
	gen           uint64
}

func newEbitenFrontend(renderer *raster.Renderer, width, height int) *ebitenFrontend {
	f := &ebitenFrontend{renderer: renderer}
	f.setSize(width, height)
	return f
}

func (f *ebitenFrontend) setSize(width, height int) {
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
	f.gen++
}

// PrepareBatch runs with the terminal lock held.
func (f *ebitenFrontend) PrepareBatch(v termemu.LockedView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renderer.Draw(f.back, v)
}

// WriteBatch runs without the terminal lock.
func (f *ebitenFrontend) WriteBatch() {
	f.mu.Lock()
	f.front, f.back = f.back, f.front
	f.gen++
	f.mu.Unlock()
}

// frame returns the most recently completed frame and its generation.
func (f *ebitenFrontend) frame() (*image.RGBA, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.front, f.gen
}
