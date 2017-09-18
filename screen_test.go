package termemu

import "testing"

func makeScreen(chars []string) *screen {
	s := newScreen(&dummyFrontend{})
	s.setSize(len(chars[0]), len(chars))
	for i := range chars {
		copy(s.chars[i], []rune(chars[i]))
	}
	return s
}

func testScreen(s *screen, chars []string) bool {
	for i := range chars {
		for j := range chars[i] {
			if s.chars[i][j] != rune(chars[i][j]) {
				return false
			}
		}
	}
	return true
}

var ninePatch = []string{
	"112233",
	"112233",
	"445566",
	"445566",
	"778899",
	"778899",
}

func TestMe(t *testing.T) {
	s := makeScreen(ninePatch)
	s.rawWriteRunes(1, 1, []rune("asdf"))
	if testScreen(s, ninePatch) {
		s.printScreen()
		t.Errorf("Expected string to change and testScreen to report false")
	}
}

func TestErase(t *testing.T) {
	var s *screen

	s = makeScreen(ninePatch)
	// non-inclusive
	s.eraseRegion(Region{X: 1, Y: 1, X2: 5, Y2: 5})
	if !testScreen(s, []string{
		"112233",
		"1    3",
		"4    6",
		"4    6",
		"7    9",
		"778899",
	}) {
		s.printScreen()
		t.Errorf("Expected middle of screen to be erased")
	}
}

func TestScroll(t *testing.T) {
	var s *screen

	s = makeScreen(ninePatch)
	s.scroll(1, 4, -1)
	if !testScreen(s, []string{
		"112233",
		"445566",
		"445566",
		"778899",
		"      ",
		"778899",
	}) {
		s.printScreen()
		t.Errorf("Expected screen to scroll up")
	}

	s = makeScreen(ninePatch)
	s.scroll(1, 4, 1)
	if !testScreen(s, []string{
		"112233",
		"      ",
		"112233",
		"445566",
		"445566",
		"778899",
	}) {
		s.printScreen()
		t.Errorf("Expected screen to scroll down")
	}
}