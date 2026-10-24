package termemu

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"strings"
)

// charset identifies a 94-character set that can be designated into one of the
// four designation slots G0-G3 with ESC ( ) * +.
//
// Only the ASCII and DEC Special Graphics sets are mapped. The national
// replacement sets (ESC ( A and friends) are accepted but render as ASCII:
// they exist to make up for terminals without a UTF-8 mode, and mapping them
// would change the meaning of ordinary text streams. Any designation byte that
// is not recognized falls back to ASCII.
type charset uint8

const (
	// charsetASCII is the default set and the fallback for unsupported
	// designations.
	charsetASCII charset = iota
	// charsetDECGraphics is the DEC Special Graphics set (ESC ( 0): the
	// 0x5F-0x7E range maps to box-drawing, block, and math characters.
	charsetDECGraphics
)

// charsetSlotCount is the number of designation slots (G0-G3).
const charsetSlotCount = 4

// charsetState is the character set state shared by the escapes and the
// screen: the four G0-G3 designations plus the slot currently invoked as GL
// (SO/SI and LS2/LS3). The zero value is the reset state: every slot ASCII
// with G0 invoked.
type charsetState struct {
	designations [charsetSlotCount]charset
	// glSlot is the designation slot invoked as GL. It is stored (rather than
	// the effective charset) so a later SO/SI still works after a snapshot
	// round-trip.
	glSlot uint8
}

// gl returns the designation currently invoked as GL.
func (c charsetState) gl() charset {
	if int(c.glSlot) >= charsetSlotCount {
		return charsetASCII
	}
	return c.designations[c.glSlot]
}

// designate designates cs into slot (G0-G3). Out-of-range slots are ignored.
func (c *charsetState) designate(slot int, cs charset) {
	if slot < 0 || slot >= charsetSlotCount {
		return
	}
	if cs != charsetDECGraphics {
		cs = charsetASCII
	}
	c.designations[slot] = cs
}

// invoke selects slot (G0-G3) as the GL set. Out-of-range slots are ignored.
func (c *charsetState) invoke(slot int) {
	if slot < 0 || slot >= charsetSlotCount {
		return
	}
	c.glSlot = uint8(slot)
}

// reset restores the default state (all ASCII, G0 invoked).
func (c *charsetState) reset() {
	*c = charsetState{}
}

// charsetFromDesignationByte maps the final byte of an ESC ( ) * + command to
// the designated charset. National replacement sets and unknown bytes fall
// back to ASCII.
func charsetFromDesignationByte(b byte) charset {
	if b == '0' {
		return charsetDECGraphics
	}
	return charsetASCII
}

// decSpecialGraphics maps bytes 0x5F-0x7E of the DEC Special Graphics set to
// their Unicode equivalents (xterm's dec2ucs table).
var decSpecialGraphics = [32]rune{
	0x00A0, // _ blank
	0x25C6, // ` diamond
	0x2592, // a checkerboard
	0x2409, // b HT
	0x240C, // c FF
	0x240D, // d CR
	0x240A, // e LF
	0x00B0, // f degree
	0x00B1, // g plus/minus
	0x2424, // h NL
	0x240B, // i VT
	0x2518, // j lower-right corner
	0x2510, // k upper-right corner
	0x250C, // l upper-left corner
	0x2514, // m lower-left corner
	0x253C, // n crossing lines
	0x23BA, // o horizontal line scan 1
	0x23BB, // p horizontal line scan 3
	0x2500, // q horizontal line
	0x23BC, // r horizontal line scan 7
	0x23BD, // s horizontal line scan 9
	0x251C, // t left tee
	0x2524, // u right tee
	0x2534, // v bottom tee
	0x252C, // w top tee
	0x2502, // x vertical line
	0x2264, // y less than or equal
	0x2265, // z greater than or equal
	0x03C0, // { pi
	0x2260, // | not equal
	0x00A3, // } UK pound
	0x00B7, // ~ middle dot
}

// mapDECGraphics translates text written while the DEC Special Graphics set is
// invoked, returning text unchanged when it contains no character in the
// mapped range. Every mapped rune is a single cell wide and the byte ranges of
// UTF-8 continuation bytes cannot fall inside 0x5F-0x7E, so the caller's cell
// count stays valid and multi-byte text is passed through untouched.
func mapDECGraphics(text string) string {
	i := 0
	for ; i < len(text); i++ {
		if text[i] >= 0x5f && text[i] <= 0x7e {
			break
		}
	}
	if i == len(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + 2*len(text)/4)
	b.WriteString(text[:i])
	for ; i < len(text); i++ {
		ch := text[i]
		if ch >= 0x5f && ch <= 0x7e {
			b.WriteRune(decSpecialGraphics[ch-0x5f])
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// charsetStateJSON is the exported shadow type used for JSON and gob encoding
// of charsetState. Designations holds the G0-G3 set values and GL the invoked
// slot.
type charsetStateJSON struct {
	Designations [charsetSlotCount]uint8 `json:"designations"`
	GL           uint8                   `json:"gl"`
}

func (c charsetState) shadow() charsetStateJSON {
	var sh charsetStateJSON
	for i, d := range c.designations {
		if d == charsetDECGraphics {
			sh.Designations[i] = uint8(charsetDECGraphics)
		}
	}
	sh.GL = c.glSlot
	return sh
}

func (c *charsetState) setFromShadow(sh charsetStateJSON) {
	var next charsetState
	for i, d := range sh.Designations {
		next.designations[i] = charsetASCII
		if charset(d) == charsetDECGraphics {
			next.designations[i] = charsetDECGraphics
		}
	}
	if sh.GL < charsetSlotCount {
		next.glSlot = sh.GL
	}
	*c = next
}

// MarshalJSON encodes the charset state as its designations plus the GL slot.
func (c charsetState) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.shadow())
}

// UnmarshalJSON decodes a charset state previously produced by MarshalJSON.
// A JSON null leaves the state unchanged. Unknown designations decode to
// ASCII.
func (c *charsetState) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var sh charsetStateJSON
	if err := json.Unmarshal(data, &sh); err != nil {
		return err
	}
	c.setFromShadow(sh)
	return nil
}

// GobEncode encodes the charset state as its designations plus the GL slot.
func (c charsetState) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(c.shadow()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GobDecode decodes a charset state previously produced by GobEncode.
func (c *charsetState) GobDecode(data []byte) error {
	var sh charsetStateJSON
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&sh); err != nil {
		return err
	}
	c.setFromShadow(sh)
	return nil
}
