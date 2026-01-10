package termemu

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
