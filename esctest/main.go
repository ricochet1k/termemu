package main

import (
	"fmt"
	"os"
	"time"

	"github.com/creack/termios/raw"
)

func main() {
	tios, err := raw.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Println("MakeRaw failed:", err)
	}

	irestore := -1

	for i, a := range os.Args[1:] {
		if a == "--" {
			irestore = i
			break
		}
		fmt.Printf("\033%s", a)
	}

	go func() {
		buf := make([]byte, 1024)

		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				fmt.Println(err)
			}

			if n > 0 {
				fmt.Printf("got: %#v\r\n", string(buf[:n]))
			}
		}
	}()

	time.Sleep(2 * time.Second)

	if irestore != -1 {
		for _, a := range os.Args[1+irestore+1:] {
			fmt.Printf("\033%s", a)
		}
	}

	err = raw.TcSetAttr(os.Stdin.Fd(), tios)
	if err != nil {
		fmt.Println("TcSetAttr failed:", err)
	}
}
