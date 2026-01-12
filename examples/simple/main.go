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
	inputDelay := flag.Int("input_delay", 0, "wait for n milliseconds before sending input")
	inputInterval := flag.Int("input_interval", 0, "wait for n milliseconds between input bytes")
	input := flag.String("input", "", "bytes to write to stdin after starting the command")
	size := flag.String("size", "", "terminal size as WxH (example: 80x24)")
	textMode := flag.String("text_mode", "rune", "text read mode: rune or grapheme")
	flag.Parse()

	fmt.Printf("input %q\n", *input)

	// Simple example: create a terminal, run printf, and print the screen
	mf := &termemu.EmptyFrontend{}
	mode := termemu.TextReadModeRune
	switch strings.ToLower(*textMode) {
	case "rune":
		mode = termemu.TextReadModeRune
	case "grapheme":
		mode = termemu.TextReadModeGrapheme
	default:
		fmt.Fprintln(os.Stderr, "text_mode must be rune or grapheme")
		return
	}
	var width, height int
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
		width, height = w, h
	}

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"sh", "-c", "printf 'Hello World'"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	backend := &termemu.PTYBackend{}
	if err := backend.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error; falling back:", err)
		return
	}
	t := termemu.NewWithMode(mf, backend, mode)
	if t == nil {
		fmt.Println("failed to create terminal")
		return
	}
	if width > 0 && height > 0 {
		if err := t.Resize(width, height); err != nil {
			fmt.Fprintln(os.Stderr, "Resize error:", err)
			return
		}
	}
	if *input != "" {
		if *inputDelay > 0 {
			<-time.After(time.Duration(*inputDelay) * time.Millisecond)
		}
		if *inputInterval > 0 {
			for i := 0; i < len(*input); i++ {
				if _, err := t.Write([]byte{(*input)[i]}); err != nil {
					fmt.Fprintln(os.Stderr, "Write input error:", err)
					return
				}
				time.Sleep(time.Duration(*inputInterval) * time.Millisecond)
			}
		} else {
			if _, err := t.Write([]byte(*input)); err != nil {
				fmt.Fprintln(os.Stderr, "Write input error:", err)
				return
			}
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
