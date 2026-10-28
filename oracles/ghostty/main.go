package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ghostty {diff|replay} [flags]")
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "diff":
		err = runDiff(os.Args[2:])
	case "replay":
		err = runReplay(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	castFile := fs.String("cast", "", "path to asciicast v2/v3 file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *castFile == "" {
		return errors.New("diff requires -cast FILE")
	}

	hdr, output, err := loadCastFile(*castFile)
	if err != nil {
		return err
	}

	termemuOracle := newTermemuOracle(hdr.Width, hdr.Height)
	if err := termemuOracle.Feed(output); err != nil {
		return fmt.Errorf("feed termemu: %w", err)
	}
	termemuDump, err := termemuOracle.Dump()
	if err != nil {
		return fmt.Errorf("dump termemu: %w", err)
	}
	_ = termemuOracle.Close()

	ghosttyOracle, err := newGhosttyOracle(hdr.Width, hdr.Height)
	if err != nil {
		return err
	}
	defer ghosttyOracle.Close()
	if err := ghosttyOracle.Feed(output); err != nil {
		return fmt.Errorf("feed ghostty: %w", err)
	}
	ghosttyDump, err := ghosttyOracle.Dump()
	if err != nil {
		return fmt.Errorf("dump ghostty: %w", err)
	}

	divs := Diff(ghosttyDump, termemuDump)
	if len(divs) == 0 {
		fmt.Println("no divergences")
		return nil
	}

	fmt.Printf("%d divergence(s):\n", len(divs))
	for _, d := range divs {
		fmt.Println(d.String())
		for _, line := range d.Context {
			fmt.Println("  " + line)
		}
	}
	return errors.New("divergences found")
}

func runReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	castFile := fs.String("cast", "", "path to asciicast v2/v3 file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *castFile == "" {
		return errors.New("replay requires -cast FILE")
	}

	hdr, output, err := loadCastFile(*castFile)
	if err != nil {
		return err
	}

	termemuOracle := newTermemuOracle(hdr.Width, hdr.Height)
	if err := termemuOracle.Feed(output); err != nil {
		return fmt.Errorf("feed termemu: %w", err)
	}
	termemuDump, err := termemuOracle.Dump()
	if err != nil {
		return fmt.Errorf("dump termemu: %w", err)
	}
	_ = termemuOracle.Close()

	fmt.Print(dumpString(termemuDump))
	return nil
}

func loadCastFile(path string) (CastHeader, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return CastHeader{}, nil, fmt.Errorf("open cast: %w", err)
	}
	defer f.Close()
	return LoadCast(f)
}

// dumpString renders a Dump as human-readable text.
func dumpString(d Dump) string {
	var b strings.Builder
	fmt.Fprintf(&b, "size: %dx%d\n", d.Width, d.Height)
	fmt.Fprintf(&b, "cursor: (%d,%d) visible=%v\n", d.Cursor.X, d.Cursor.Y, d.CursorVisible)
	fmt.Fprintf(&b, "alt-screen: %v\n", d.AltScreen)
	for y, row := range d.Rows {
		fmt.Fprintf(&b, "%3d | ", y)
		for _, c := range row {
			if c.Text == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Text)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
