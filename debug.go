package termemu

import (
	"flag"
	"fmt"
	"io"
	"os"
)

var (
	debugOutput     io.Writer = os.Stderr
	debugFile                 = flag.String("debugFile", "", "File to send debug info to")
	debugInitCalled           = false

	debugCursor  = flag.Bool("debugCursor", false, "Print cursor debugging")
	debugCharSet = flag.Bool("debugCharSet", false, "Print character set debugging")
	debugErase   = flag.Bool("debugErase", false, "Print erase debugging")
	debugScroll  = flag.Bool("debugScroll", false, "Print scroll debugging")
	debugTxt     = flag.Bool("debugTxt", false, "Print all text written to screen")
	debugCmd     = flag.Bool("debugCmd", false, "Print all commands")
	debugTodo    = flag.Bool("debugTodo", true, "Print TODO commands")
	debugErrors  = flag.Bool("debugErrors", true, "Print Errors")

	debugWait = flag.Bool("debugWait", false, "Pause on every debug command")
)

func initDebug() {
	debugInitCalled = true
	if *debugFile != "" {
		f, err := os.OpenFile(*debugFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
		debugOutput = f
	}
}

func debugPause(debugFlag *bool) {
	if *debugWait && (debugFlag == debugTxt || debugFlag == debugScroll) {
		b := []byte{0}
		os.Stdin.Read(b)
		if b[0] == 'q' {
			os.Exit(1)
		}
	}
}

func debugPrintln(debugFlag *bool, args ...interface{}) {
	if !debugInitCalled {
		initDebug()
	}
	if *debugFlag {
		fmt.Fprintln(debugOutput, args...)
		debugPause(debugFlag)
	}
}

func debugPrintf(debugFlag *bool, f string, args ...interface{}) {
	if !debugInitCalled {
		initDebug()
	}
	if *debugFlag {
		fmt.Fprintf(debugOutput, f, args...)
		debugPause(debugFlag)
	}
}
