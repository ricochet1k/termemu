package termemu

import (
	"sync"
	"testing"
)

// MockFrontend implements Frontend for tests and records calls.
type MockFrontend struct {
	mu sync.Mutex

	BellCount int

	Regions []struct {
		R Region
		C ChangeReason
	}

	CursorX          int
	CursorY          int
	CursorMovedCount int

	Colors []struct {
		F Color
		B Color
	}

	ViewFlags   map[ViewFlag]bool
	ViewInts    map[ViewInt]int
	ViewStrings map[ViewString]string
}

func NewMockFrontend() *MockFrontend {
	return &MockFrontend{
		ViewFlags:   make(map[ViewFlag]bool),
		ViewInts:    make(map[ViewInt]int),
		ViewStrings: make(map[ViewString]string),
	}
}

func (m *MockFrontend) Bell() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BellCount++
}

func (m *MockFrontend) RegionChanged(r Region, c ChangeReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Regions = append(m.Regions, struct {
		R Region
		C ChangeReason
	}{r, c})
}

func (m *MockFrontend) ScrollLines(y int) {
	// no-op for now
	_ = y
}

func (m *MockFrontend) CursorMoved(x, y int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CursorX = x
	m.CursorY = y
	m.CursorMovedCount++
}

func (m *MockFrontend) ColorsChanged(f Color, b Color) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Colors = append(m.Colors, struct {
		F Color
		B Color
	}{f, b})
}

func (m *MockFrontend) ViewFlagChanged(vs ViewFlag, value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewFlags[vs] = value
}

func (m *MockFrontend) ViewIntChanged(vs ViewInt, value int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewInts[vs] = value
}

func (m *MockFrontend) ViewStringChanged(vs ViewString, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewStrings[vs] = value
}

// helper to create a screen with a MockFrontend and return both
func MakeScreenWithMock() (screen, *MockFrontend) {
	mf := NewMockFrontend()
	s := newScreen(mf)
	return s, mf
}

// MakeTerminalWithMock constructs a terminal without opening a pty for tests.
func MakeTerminalWithMock() (*terminal, *MockFrontend) {
	mf := NewMockFrontend()
	t := &terminal{
		frontend:    mf,
		mainScreen:  newScreen(mf),
		altScreen:   newScreen(mf),
		viewFlags:   make([]bool, viewFlagCount),
		viewInts:    make([]int, viewIntCount),
		viewStrings: make([]string, viewStringCount),
	}
	return t, mf
}

// small helper to fail test with recorded state for easier debugging
func dumpMock(t *testing.T, m *MockFrontend) {
	t.Logf("MockFrontend: Bell=%d, Cursor=(%d,%d), Regions=%d, Colors=%d", m.BellCount, m.CursorX, m.CursorY, len(m.Regions), len(m.Colors))
}
