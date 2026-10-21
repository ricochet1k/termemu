package termemu

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
)

const keyboardStackMax = 32

type keyboardMode struct {
	flags int
	stack []int
}

func (t *terminal) keyboardMode() *keyboardMode {
	if t.onAltScreen {
		return &t.keyboardAlt
	}
	return &t.keyboardMain
}

func (t *terminal) keyboardFlags() int {
	return t.keyboardMode().flags
}

func (t *terminal) updateKeyboardFlags(flags, mode int) {
	if flags < 0 {
		flags = 0
	}
	if mode <= 0 {
		mode = 1
	}
	switch mode {
	case 1:
		t.keyboardMode().flags = flags
	case 2:
		t.keyboardMode().flags |= flags
	case 3:
		t.keyboardMode().flags &^= flags
	}
}

func (t *terminal) pushKeyboardFlags(flags int) {
	km := t.keyboardMode()
	if len(km.stack) >= keyboardStackMax {
		copy(km.stack, km.stack[1:])
		km.stack = km.stack[:len(km.stack)-1]
	}
	km.stack = append(km.stack, km.flags)
	km.flags = flags
}

func (t *terminal) popKeyboardFlags(n int) {
	if n <= 0 {
		n = 1
	}
	km := t.keyboardMode()
	for i := 0; i < n; i++ {
		if len(km.stack) == 0 {
			km.flags = 0
			return
		}
		km.flags = km.stack[len(km.stack)-1]
		km.stack = km.stack[:len(km.stack)-1]
	}
}

// keyboardModeJSON is the exported shadow type used for JSON and gob encoding
// of keyboardMode. Flags is the current Kitty keyboard flags value and Stack
// holds the saved flag values pushed by CSI > u / CSI < u.
type keyboardModeJSON struct {
	Flags int   `json:"flags"`
	Stack []int `json:"stack,omitempty"`
}

// MarshalJSON encodes the keyboard mode as its flags plus the saved stack.
func (k keyboardMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(keyboardModeJSON{Flags: k.flags, Stack: k.stack})
}

// UnmarshalJSON decodes a keyboard mode previously produced by MarshalJSON.
// A JSON null leaves the mode unchanged.
func (k *keyboardMode) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var sh keyboardModeJSON
	if err := json.Unmarshal(data, &sh); err != nil {
		return err
	}
	k.flags = sh.Flags
	k.stack = sh.Stack
	return nil
}

// GobEncode encodes the keyboard mode as its flags plus the saved stack.
func (k keyboardMode) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(keyboardModeJSON{Flags: k.flags, Stack: k.stack}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GobDecode decodes a keyboard mode previously produced by GobEncode.
func (k *keyboardMode) GobDecode(data []byte) error {
	var sh keyboardModeJSON
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&sh); err != nil {
		return err
	}
	k.flags = sh.Flags
	k.stack = sh.Stack
	return nil
}
