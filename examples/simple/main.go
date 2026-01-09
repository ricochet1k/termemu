package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ricochet1k/termemu"
)

func main() {
	termemu.DebugFlags()
	delay := flag.Int("delay", 0, "wait for n milliseconds instead of waiting for cmd to exit")
	input := flag.String("input", "", "bytes to write to stdin after starting the command")
	size := flag.String("size", "", "terminal size as WxH (example: 80x24)")
	flag.Parse()

	// Simple example: create a terminal, run printf, and print the screen
	mf := &termemu.EmptyFrontend{}
	t := termemu.New(mf)
	if t == nil {
		fmt.Println("failed to create terminal")
		return
	}
	if *size != "" {
		parts := strings.SplitN(*size, "x", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "size must be in WxH format")
			return
		}
		w, errW := strconv.Atoi(parts[0])
		h, errH := strconv.Atoi(parts[1])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			fmt.Fprintln(os.Stderr, "size must be in WxH format with positive integers")
			return
		}
		if err := t.Resize(w, h); err != nil {
			fmt.Fprintln(os.Stderr, "Resize error:", err)
			return
		}
	}

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"sh", "-c", "printf 'Hello World'"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	if err := t.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error; falling back:", err)
		return
	}
	if *input != "" {
		if _, err := t.Write([]byte(*input)); err != nil {
			fmt.Fprintln(os.Stderr, "Write input error:", err)
			return
		}
	}
	if *delay > 0 {
		<-time.After(time.Duration(*delay) * time.Millisecond)
		cmd.Process.Kill()
	} else {
		cmd.Wait()
	}
	// Print the terminal screen to stdout
	t.PrintTerminal()
}
