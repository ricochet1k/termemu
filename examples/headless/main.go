// Command headless runs a command under a PTY and renders its screen to a PNG
// file, with no window and no GUI toolkit.
//
// It is the smallest of the graphical frontend examples: it implements the
// optional BatchFrontend interface, paints each frame into an image.RGBA with
// examples/internal/raster, and writes the result after the terminal lock is
// released.
//
//	go run . -out screen.png -- ls --color=always /tmp
//
// The PNG is rewritten after every rendered frame, so pointing a viewer at it
// gives a slow-motion mirror of the command's output.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ricochet1k/termemu"
	"github.com/ricochet1k/termemu/examples/internal/raster"
)

func main() {
	cols := flag.Int("cols", 80, "terminal width in cells")
	rows := flag.Int("rows", 24, "terminal height in cells")
	fontSize := flag.Float64("font-size", 16, "font size in pixels")
	out := flag.String("out", "screen.png", "PNG file to write (empty disables writing)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] -- command [args...]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*cols, *rows, *fontSize, *out, args); err != nil {
		log.Fatal(err)
	}
}

func run(cols, rows int, fontSize float64, out string, args []string) error {
	renderer, err := raster.New(fontSize)
	if err != nil {
		return err
	}
	defer func() { _ = renderer.Close() }()

	cellW, cellH := renderer.CellSize()
	img := image.NewRGBA(image.Rect(0, 0, cols*cellW, rows*cellH))
	frontend := &pngFrontend{renderer: renderer, img: img, path: out}

	cmd := exec.Command(args[0], args[1:]...)
	backend := &termemu.PTYBackend{}
	if err := backend.Attach(cmd); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	term := termemu.NewWithMode(frontend, backend, termemu.TextReadModeGrapheme)
	if err := term.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize: %w", err)
	}

	waitErr := cmd.Wait()

	// PTYBackend treats the child's exit as "wait for the next command", so
	// the read loop only stops when the backend is closed. Give it a moment to
	// drain what the child wrote before exiting.
	time.Sleep(100 * time.Millisecond)
	if cerr := term.Close(); cerr != nil {
		return fmt.Errorf("close: %w", cerr)
	}
	<-term.Done()

	// Render one last frame from the final state and report write errors that
	// happened during the batch loop.
	term.View(func(v termemu.LockedView) { renderer.Draw(img, v) })
	if err := frontend.write(); err != nil {
		return err
	}
	if frontend.err != nil {
		return frontend.err
	}
	if waitErr != nil {
		fmt.Fprintf(os.Stderr, "command exited with %v\n", waitErr)
	}
	return nil
}

// pngFrontend renders the terminal into an image and writes it to disk after
// the terminal lock is released.
type pngFrontend struct {
	termemu.EmptyFrontend

	renderer *raster.Renderer
	img      *image.RGBA
	path     string

	// mu serializes WriteBatch calls. The terminal releases its lock before
	// calling it, so batches can arrive from more than one goroutine (the read
	// loop, Resize, Restore); the frontend keeps them ordered and keeps err
	// race-free.
	mu  sync.Mutex
	err error
}

// PrepareBatch runs with the terminal lock held, so it only touches frontend
// state and never performs I/O.
func (f *pngFrontend) PrepareBatch(v termemu.LockedView) {
	f.renderer.Draw(f.img, v)
}

// WriteBatch runs after the terminal lock is released, so writing the file
// cannot block the terminal.
func (f *pngFrontend) WriteBatch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.write(); err != nil && f.err == nil {
		f.err = err
	}
}

func (f *pngFrontend) write() error {
	if f.path == "" {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := png.Encode(file, f.img); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
