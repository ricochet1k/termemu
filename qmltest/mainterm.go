package main

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/ricochet1k/termemu"
)

func mainTerm() {
	f := &frontend{}
	t := termemu.New(f)
	f.t = t

	t.Resize(90, 25)

	cmd := exec.Command("bash", "-c", "echo Hi $TERM")
	t.StartCommand(cmd)

	err := cmd.Wait()
	if err != nil {
		fmt.Println(err)
	}

	cmd = exec.Command("bash", "-c", "vim")
	t.StartCommand(cmd)

	// err := cmd.Wait()
	// if err != nil {
	// 	fmt.Println(err)
	// }

	time.Sleep(1 * time.Second)
	t.PrintTerminal()
}

type frontend struct {
	termemu.EmptyFrontend
	t termemu.Terminal
}

func (f *frontend) Bell() {
	fmt.Println("bell")
}

func (f *frontend) RegionChanged(r termemu.Region, cr termemu.ChangeReason) {
	// line := f.t.Line(r.Y)
	// fmt.Printf("RegionChanged: %v, %#v\n", r, string(line[r.X:r.X+r.W]))
}
func (f *frontend) CursorMoved(x, y int)                             {}
func (f *frontend) ColorsChanged(fg termemu.Color, bg termemu.Color) {}
