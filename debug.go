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

	debugCursor  = new(bool)
	debugCharSet = new(bool)
	debugErase   = new(bool)
	debugScroll  = new(bool)
	debugTxt     = new(bool)
	debugCmd     = new(bool)
	debugTodo    = new(bool)
	debugErrors  = new(bool)

	debugWait = new(bool)
)

func DebugFlags() {
	flag.BoolVar(debugCursor, "debugCursor", false, "Print cursor debugging")
	flag.BoolVar(debugCharSet, "debugCharSet", false, "Print character set debugging")
	flag.BoolVar(debugErase, "debugErase", false, "Print erase debugging")
	flag.BoolVar(debugScroll, "debugScroll", false, "Print scroll debugging")
	flag.BoolVar(debugTxt, "debugTxt", false, "Print all text written to screen")
	flag.BoolVar(debugCmd, "debugCmd", false, "Print all commands")
	flag.BoolVar(debugTodo, "debugTodo", true, "Print TODO commands")
	flag.BoolVar(debugErrors, "debugErrors", true, "Print Errors")

	flag.BoolVar(debugWait, "debugWait", false, "Pause on every debug command")
}

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
