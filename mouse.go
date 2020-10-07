package termemu

import ()

type mouseBtn byte
type mouseFlag byte

const (
	// low 2 bits
	mBtn1     mouseBtn = 0
	mBtn2     mouseBtn = 1
	mBtn3     mouseBtn = 2
	mRelease  mouseBtn = 3
	mWhichBtn byte     = 3

	// flags
	mShift   mouseFlag = 4
	mMeta    mouseFlag = 8
	mControl mouseFlag = 16
	mMotion  mouseFlag = 32
	mWheel   mouseFlag = 64
	mExtra   mouseFlag = 128
)

type MouseBtn byte

const (
	BtnNone MouseBtn = 0
	// 1, 2, 3
	BtnWheelUp   MouseBtn = 4
	BtnWheelDown MouseBtn = 5
	// 6, 7
	// 8, 9, 10, 11
)

type MouseMods byte

const (
	MShift   MouseMods = 4
	MMeta    MouseMods = 8
	MControl MouseMods = 16

	MMotion MouseMods = 32
)

type EventMouse struct {
	Btn  MouseBtn
	Mods MouseMods
	X    int
	Y    int
}

// NewEventMouse makes a new EventMouse from a VT100/xterm-encoded mouse byte and
// x, y coordinates. TODO: x, y relative to 0 or 1?
func NewEventMouse(encMouse byte, x, y int) *EventMouse {
	btn := encMouse & mWhichBtn
	if encMouse&byte(mWheel) != 0 {
		btn += 4
	} else if encMouse&byte(mExtra) != 0 {
		btn += 8
	} else {
		// lowest 2 bits are button, where 0 is btn1, 1 is btn2, 2 is btn3, and 3 is release
		btn = (btn + 1) & mWhichBtn
	}

	mods := MouseMods(0)
	if encMouse&byte(mShift) != 0 {
		mods &= MShift
	}
	if encMouse&byte(mMeta) != 0 {
		mods &= MMeta
	}
	if encMouse&byte(mControl) != 0 {
		mods &= MControl
	}
	if encMouse&byte(mMotion) != 0 {
		mods &= MMotion
	}

	return &EventMouse{
		Btn:  MouseBtn(btn),
		Mods: mods,
		X:    x,
		Y:    y,
	}
}

func (e *EventMouse) IsMotion() bool {
	return e.Mods&MMotion != 0
}

func (e *EventMouse) Encode() (btn byte, press bool, x, y int) {
	btn = byte(e.Btn) & mWhichBtn
	if e.Btn >= 4 && e.Btn <= 7 {
		btn &= byte(mWheel)
	} else if e.Btn >= 8 && e.Btn <= 11 {
		btn &= byte(mExtra)
	} else {
		// lowest 2 bits are button, where 0 is btn1, 1 is btn2, 2 is btn3, and 3 is release
		btn = (btn - 1) & mWhichBtn
	}

	if e.Mods&MShift != 0 {
		btn &= byte(mShift)
	}
	if e.Mods&MMeta != 0 {
		btn &= byte(mMeta)
	}
	if e.Mods&MControl != 0 {
		btn &= byte(mControl)
	}
	if e.Mods&MMotion != 0 {
		btn &= byte(mMotion)
	}

	return btn, e.Btn != BtnNone, e.X, e.Y
}
