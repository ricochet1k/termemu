package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"github.com/creack/termios/raw"
	"github.com/ricochet1k/termemu"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"sh"}
	}

	tios, err := raw.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Fprintln(os.Stderr, "MakeRaw failed:", err)
		return
	}
	defer func() {
		_ = raw.TcSetAttr(os.Stdin.Fd(), tios)
	}()

	tty := termemu.NewTTYFrontend(nil, os.Stdout)
	term := termemu.New(tty)
	tty.SetTerminal(term)

	resize := func() {
		ws, err := pty.GetsizeFull(os.Stdout)
		if err == nil && ws.Cols > 0 && ws.Rows > 0 {
			w := int(ws.Cols)
			h := int(ws.Rows)
			_ = term.Resize(w, h)
			tty.Attach(termemu.Region{X: 0, Y: 0, X2: w, Y2: h})
			return
		}
		w, h := term.Size()
		tty.Attach(termemu.Region{X: 0, Y: 0, X2: w, Y2: h})
	}
	resize()

	cmd := exec.Command(args[0], args[1:]...)
	if err := term.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error:", err)
		return
	}

	inputCh := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				inputCh <- chunk
			}
			if err != nil {
				close(inputCh)
				return
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	for {
		select {
		case b, ok := <-inputCh:
			if !ok {
				return
			}
			if len(b) > 0 {
				_, _ = term.Write(b)
			}
		case <-sigCh:
			resize()
		case <-doneCh:
			tty.Detach()
			return
		}
	}
}
