package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ricochet1k/termemu"
)

func main() {
	// Simple example: create a terminal, run printf, and print the screen
	mf := &termemu.EmptyFrontend{}
	t := termemu.New(mf)
	if t == nil {
		fmt.Println("failed to create terminal")
		return
	}
	args := os.Args[1:]
	if len(args) <= 1 {
		args = []string{"sh", "-c", "printf 'Hello World'"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	if err := t.StartCommand(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "StartCommand error; falling back:", err)
		return
	}
	cmd.Wait()
	// Print the terminal screen to stdout
	t.PrintTerminal()
}
