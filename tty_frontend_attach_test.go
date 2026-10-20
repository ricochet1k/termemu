package termemu

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is an io.Writer safe for concurrent use by the TTYFrontend,
// which may write from the read-loop goroutine (renders under the terminal
// lock) and from Detach (no terminal lock).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// TestTTYFrontendAttachConcurrent churns Attach/Detach while terminal output
// flows through the read loop. Attach used to hold the frontend mutex while
// taking the terminal lock — the opposite order of RegionChanged (terminal
// lock held, takes the frontend mutex) — so this interleaving could
// deadlock. It must complete promptly; run with -race.
func TestTTYFrontendAttachConcurrent(t *testing.T) {
	r, w := io.Pipe()
	var out lockedBuffer
	frontend := NewTTYFrontend(&out)
	term := NewWithMode(frontend, NewNoPTYBackend(r, io.Discard), TextReadModeRune)
	region := Region{X: 0, Y: 0, X2: 40, Y2: 4}
	if err := term.Resize(40, 4); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer w.Close()
		for i := 0; i < 200; i++ {
			if _, err := w.Write([]byte("hello\x1b[31mred\x1b[0m\r\n")); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			frontend.Attach(region)
			frontend.Detach()
		}
	}()
	wg.Wait()

	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	waitDone(t, term, 5*time.Second)
}
