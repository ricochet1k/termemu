package termemu

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// snapshotFrontend snapshots both the screen and the scrollback from inside
// callbacks. All it does is call the no-lock view methods, so a regression to
// the locking entry points would deadlock the feed.
type snapshotFrontend struct {
	*MockFrontend

	mu          sync.Mutex
	regionSnaps []Snapshot
	scrollSnaps []Snapshot
	scrollback  []ScrollbackSnapshot
}

func newSnapshotFrontend() *snapshotFrontend {
	return &snapshotFrontend{MockFrontend: NewMockFrontend()}
}

func (f *snapshotFrontend) record(v LockedView) {
	screen := v.Snapshot()
	scroll := v.Scrollback().Snapshot()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.regionSnaps = append(f.regionSnaps, screen)
	f.scrollback = append(f.scrollback, scroll)
}

func (f *snapshotFrontend) RegionChanged(v LockedView, r Region, c ChangeReason) {
	f.record(v)
}

func (f *snapshotFrontend) ScrollLines(v LockedView, y int) {
	f.record(v)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scrollSnaps = append(f.scrollSnaps, v.Snapshot())
}

func (f *snapshotFrontend) counts() (regions, scrolls, scrollbacks int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.regionSnaps), len(f.scrollSnaps), len(f.scrollback)
}

func TestLockedViewSnapshotInCallbacks(t *testing.T) {
	f := newSnapshotFrontend()
	term := newTerminal(f, NewNoPTYBackend(nil, nil), TextReadModeRune)

	data := []byte(strings.Repeat("callback line\n", 40))
	done := make(chan error, 1)
	go func() {
		done <- term.testFeedTerminalInputFromBackend(data, TextReadModeRune)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot from a frontend callback deadlocked")
	}

	regions, _, scrollbacks := f.counts()
	if regions == 0 {
		t.Fatal("RegionChanged never called")
	}
	if scrollbacks != regions {
		t.Fatalf("got %d scrollback snapshots for %d region callbacks", scrollbacks, regions)
	}

	// ScrollLines is a legacy callback the core does not currently emit, so
	// invoke it from a View (same lock-held contract) to cover that path too.
	var fromView Snapshot
	term.View(func(v LockedView) {
		f.ScrollLines(v, 0)
		fromView = v.Snapshot()
	})
	if _, scrolls, _ := f.counts(); scrolls != 1 {
		t.Fatalf("ScrollLines snapshot count = %d, want 1", scrolls)
	}

	// Every usable callback snapshot must describe the live screen. The
	// initial screen-construction callbacks run before the terminal view is
	// wired, so they legitimately see a zero LockedView.
	f.mu.Lock()
	usable := 0
	for i, snap := range f.regionSnaps {
		if snap.Main.Width == 0 && snap.Main.Height == 0 {
			continue
		}
		usable++
		if snap.Main.Width != 80 || snap.Main.Height != 14 {
			f.mu.Unlock()
			t.Fatalf("region snapshot %d size = %dx%d", i, snap.Main.Width, snap.Main.Height)
		}
	}
	f.mu.Unlock()
	if usable == 0 {
		t.Fatal("no usable snapshots captured from RegionChanged")
	}

	// The view snapshot taken after the feed matches the locking entry point.
	if want := term.Snapshot(); !reflect.DeepEqual(fromView, want) {
		t.Fatal("LockedView.Snapshot does not match Terminal.Snapshot")
	}

	var scrollFromView ScrollbackSnapshot
	term.View(func(v LockedView) {
		scrollFromView = v.Scrollback().Snapshot()
	})
	if want := term.Scrollback().Snapshot(); !reflect.DeepEqual(scrollFromView, want) {
		t.Fatal("LockedScrollback.Snapshot does not match Scrollback.Snapshot")
	}
	if len(scrollFromView.Rows) == 0 {
		t.Fatal("expected scrollback rows after scrolling")
	}
}

func TestLockedViewSnapshotZeroValue(t *testing.T) {
	if got := (LockedView{}).Snapshot(); !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("zero LockedView snapshot = %#v, want empty", got)
	}
	if got := (LockedScrollback{}).Snapshot(); !reflect.DeepEqual(got, ScrollbackSnapshot{}) {
		t.Fatalf("zero LockedScrollback snapshot = %#v, want empty", got)
	}
}
