package main

import (
	"fmt"
	"os"

	"github.com/creack/termios/raw"
	"github.com/ricochet1k/termemu"
	"github.com/xo/terminfo"
)

func main() {
	ti, err := terminfo.LoadFromEnv()
	if err != nil {
		fmt.Println("terminfo.LoadFromEnv failed:", err)
		return
	}

	events := make(chan interface{}, 1)

	// go func() {
	// 	time.Sleep(10 * time.Second)
	// 	os.Stdin.Close()
	// }()

	_ = termemu.ParseTerminalInput(os.Stdin, ti, events)

	tios, err := raw.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Println("MakeRaw failed:", err)
		return
	}

	defer func() {
		err = raw.TcSetAttr(os.Stdin.Fd(), tios)
		if err != nil {
			fmt.Println("TcSetAttr failed:", err)
		}
	}()

	setupArgs := os.Args[1:]
	var restoreArgs []string

	for i, a := range os.Args[1:] {
		if a == "--" {
			setupArgs = os.Args[1:i]
			restoreArgs = os.Args[i+1:]
			break
		}
	}

	for _, arg := range setupArgs {
		fmt.Printf("\033%s", arg)
	}
	if len(restoreArgs) > 0 {
		defer func() {
			for _, a := range restoreArgs {
				fmt.Printf("\033%s", a)
			}
		}()
	}

	for ev := range events {
		switch ev := ev.(type) {
		case []rune:
			fmt.Printf("%q\r\n", string(ev))
			if len(ev) == 1 && ev[0] == 3 {
				return
			}
		case int:
			fmt.Printf("%v: %v\r\n", ev, terminfo.StringCapName(ev))
		case *termemu.EventKey:
			fmt.Printf("key: %x %q\r\n", ev.Mods, ev.Rune)
			if ev.Rune == 'c' && ev.Mods == termemu.KControl {
				return
			}
		default:
			fmt.Printf("%#v\r\n", ev)
		}
	}
}
