package termemu

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/xo/terminfo"
)

// const (
// 	sgrPrefix    = "\x1b[<"
// 	sgrPrefixNum = 0xFFF00 // something higher than all the numbers in terminfo.*
// )

type parser struct {
	states []map[byte]int
}

func newParser() *parser {
	return &parser{
		states: []map[byte]int{
			map[byte]int{},
		},
	}
}

func (p *parser) insert(byts []byte, result int) {
	state := 0

	for _, b := range byts[:len(byts)-1] {
		if next, ok := p.states[state][b]; ok {
			if next < 0 {
				panic(fmt.Sprintf("parser string must not be prefix of other string: %v, %v, %v", p.states, byts, result))
			}
			state = next
		} else {
			next := len(p.states)
			p.states[state][b] = next
			p.states = append(p.states, map[byte]int{})
			state = next
		}
	}

	lastb := byts[len(byts)-1]
	if _, ok := p.states[state][lastb]; ok {
		panic(fmt.Sprintf("parser string must not be prefix of other string: %v, %v, %v", p.states, byts, result))
	} else {
		p.states[state][lastb] = -result
	}
}

type TerminalInputHandler struct {
	mouseEncoding MouseEncoding
}

func (t *TerminalInputHandler) SetMouseEncoding(me MouseEncoding) {
	atomic.StoreInt32((*int32)(&t.mouseEncoding), int32(me))
}

func ParseTerminalInput(input io.Reader, ti *terminfo.Terminfo, events chan<- interface{}) *TerminalInputHandler {

	p := newParser()

	for num, bytes := range ti.Strings {
		name := terminfo.StringCapName(num)
		if len(bytes) == 0 {
			// fmt.Fprintf(os.Stderr, "Empty key entry in terminfo? %v %q\n", name, bytes)
			continue
		}
		if strings.HasPrefix(name, "key_") {
			// fmt.Fprintf(os.Stderr, "%v %q\n", name, bytes)
			p.insert(bytes, num)
		}
	}

	// p.insert([]byte(sgrPrefix), sgrPrefixNum)

	t := &TerminalInputHandler{
		mouseEncoding: MEX10,
	}
	go t.inputLoop(input, p, events)
	return t
}

func (t *TerminalInputHandler) inputLoop(input io.Reader, p *parser, events chan<- interface{}) {
	reader := bufio.NewReader(input)

	for {
		if err := t.inputOne(reader, p, events); err != nil {
			debugPrintln(debugErrors, err)
			return
		}
	}
}

func (t *TerminalInputHandler) inputOne(reader *bufio.Reader, p *parser, events chan<- interface{}) error {
	var r rune
	var err error

	// printables
	var runes []rune
	for {
		if r, _, err = reader.ReadRune(); err != nil {
			return err
		}

		if r < 32 || r > 126 && r < 128 {
			if err = reader.UnreadRune(); err != nil {
				return err
			}
			break
		}

		runes = append(runes, r)

		// check at end of loop so that first run can wait for more data
		if reader.Buffered() == 0 {
			break
		}
	}
	if len(runes) > 0 {
		events <- runes

		if reader.Buffered() == 0 {
			return nil
		}
	}

	peek, err := reader.Peek(reader.Buffered())
	if err != nil {
		return err
	}

	i := 0
	state := 0
	for state >= 0 {
		if i >= len(peek) {
			panic(fmt.Sprint("wanted to read more than buffered?!", peek, state))
		}
		if next, ok := p.states[state][peek[i]]; ok {
			i++
			state = next

			// terminal state
			if state < 0 {
				break
			}
		} else {
			break
		}
	}

	if state >= 0 {
		// state >= 0 means we didn't fully match any key specified in terminfo

		if i == 0 { // didn't even match one byte of terminfo
			if r, _, err = reader.ReadRune(); err != nil {
				return err
			}
			if r < 32 { // ctrl+key
				events <- t.parseKey(nil, r)
				return nil
			}

			fmt.Print("fallback0:")
			events <- []rune{r}
			return nil
		}

		if i == 1 && peek[0] == 0x1b && len(peek) > 1 {
			// escape, then a key byte (<32 or printable) means either alt or meta
			if _, err = reader.Discard(1); err != nil {
				return err
			}
			if r, _, err = reader.ReadRune(); err != nil {
				return err
			}

			events <- t.parseKey([]byte{0x1b}, r)
			return nil
		}

		if i >= 2 && peek[0] == 0x1b && peek[1] == '[' && len(peek) > 2 {
			prefix, params, b, err := readCSI(reader)
			if err != nil {
				return err
			}

			if len(prefix) == 1 && prefix[0] == '>' && (b == 'm' || b == 'M') {
				ev, err := parseSGRMouse(params, b)
				if err != nil {
					return err
				}
				events <- ev
			}

			events <- struct {
				Prefix []byte
				Params []int
				B      string
			}{
				prefix, params, string(b),
			}
			return nil
		}

		// parser didn't match whatever this sequence is, just pass it on
		runes = []rune(string(peek[:i]))
		if _, err = reader.Discard(i); err != nil {
			return err
		}
		fmt.Print("fallback:")
		events <- runes
	} else {
		// matched a key sequence in terminfo!
		if _, err = reader.Discard(i); err != nil {
			return err
		}

		key := -state
		// if key == sgrPrefixNum {
		// 	// TODO: check mouse encoding???
		// 	ev, err := parseSGRMouse(reader)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	events <- ev

		// } else
		if key == terminfo.KeyMouse {
			enc := MouseEncoding(atomic.LoadInt32((*int32)(&t.mouseEncoding)))
			fmt.Fprintf(os.Stderr, "MOUSE! %v %v\r\n", key, enc)
			switch enc {
			case MEX10:
				ev, err := parseX10Mouse(reader)
				if err != nil {
					return err
				}
				events <- ev

			case MEUTF8:
				ev, err := parseUTF8Mouse(reader)
				if err != nil {
					return err
				}
				events <- ev

			}
		} else {
			events <- key
		}
	}
	return nil
}

func (t *TerminalInputHandler) parseKey(prefix []byte, r rune) *EventKey {
	mods := KeyMods(0)

	if len(prefix) > 0 {
		if len(prefix) == 1 && prefix[0] == 0x1b {
			mods |= KMeta
		} else {
			fmt.Fprintf(os.Stderr, "Unhandled key prefix: %v", prefix)
		}
	}

	if r < 32 {
		r += 'a' - 1
		mods |= KControl
	}

	return &EventKey{
		Rune: r,
		Mods: mods,
	}
}

func parseX10Mouse(reader *bufio.Reader) (*EventMouse, error) {
	var b byte
	var err error
	if b, err = reader.ReadByte(); err != nil {
		return nil, err
	}
	btn := b
	if b, err = reader.ReadByte(); err != nil {
		return nil, err
	}
	x := b
	if b, err = reader.ReadByte(); err != nil {
		return nil, err
	}
	y := b

	fmt.Fprintln(os.Stderr, btn, x, y)

	return NewEventMouse(btn-32, int(x-32), int(y-32)), nil
}

func parseUTF8Mouse(reader *bufio.Reader) (*EventMouse, error) {
	var r rune
	var err error
	if r, _, err = reader.ReadRune(); err != nil {
		return nil, err
	}
	btn := r
	if r, _, err = reader.ReadRune(); err != nil {
		return nil, err
	}
	x := r
	if r, _, err = reader.ReadRune(); err != nil {
		return nil, err
	}
	y := r
	fmt.Fprintln(os.Stderr, btn, x, y)

	return NewEventMouse(byte(btn-32), int(x-32), int(y-32)), nil
}

func parseSGRMouse(params []int, b rune) (*EventMouse, error) {
	press := b == 'M' // TODO: What to do with this??

	if len(params) != 3 {
		return nil, fmt.Errorf("invalid SGRMouse: %v %s %v\r\n", params, string(b), press)
	}

	return NewEventMouse(byte(params[0]), params[1], params[2]), nil
}
