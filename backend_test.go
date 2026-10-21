package termemu

import (
	"bytes"
	"errors"
	"io"
	"syscall"
	"testing"
	"time"
)

// childExitReader returns childErr a fixed number of times, then data, then
// io.EOF. It fakes a pty master whose child has exited.
type childExitReader struct {
	childErr error
	failures int
	data     []byte
	failed   int
}

func (r *childExitReader) Read(p []byte) (int, error) {
	if r.failed < r.failures {
		r.failed++
		return 0, r.childErr
	}
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, io.EOF
}

// TestReadRetryingReuseChildExit checks that both EIO (Linux) and EOF (Darwin)
// from the master are treated as "child exited, wait for reuse" rather than
// ending the stream.
func TestReadRetryingReuseChildExit(t *testing.T) {
	for _, childErr := range []error{syscall.EIO, io.EOF} {
		t.Run(childErr.Error(), func(t *testing.T) {
			r := &childExitReader{childErr: childErr, failures: 3, data: []byte("hello")}
			var waits []time.Duration
			buf := make([]byte, 8)
			n, err := readRetryingReuse(r, buf,
				func() bool { return false },
				func(d time.Duration) { waits = append(waits, d) })
			if err != nil {
				t.Fatalf("Read failed: %v", err)
			}
			if n != 5 {
				t.Fatalf("n = %d, want 5", n)
			}
			if got := string(buf[:n]); got != "hello" {
				t.Fatalf("data = %q, want %q", got, "hello")
			}
			if len(waits) != 3 {
				t.Fatalf("waited %d times, want 3 (one per child exit)", len(waits))
			}
			if waits[0] != time.Millisecond || waits[1] != 2*time.Millisecond || waits[2] != 4*time.Millisecond {
				t.Fatalf("backoff = %v, want exponential from 1ms", waits)
			}
		})
	}
}

// TestReadRetryingReuseClosed checks Close wins over retry: after closed
// reports true, Read returns io.ErrClosedPipe instead of waiting again.
func TestReadRetryingReuseClosed(t *testing.T) {
	r := &childExitReader{childErr: io.EOF, failures: 1000}
	closed := false
	waits := 0
	n, err := readRetryingReuse(r, make([]byte, 8),
		func() bool { return closed },
		func(time.Duration) { waits++; closed = true })
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err = %v, want io.ErrClosedPipe", err)
	}
	if waits != 1 {
		t.Fatalf("waited %d times, want 1", waits)
	}
}

func TestReadRetryingReuseOtherError(t *testing.T) {
	boom := errors.New("boom")
	r := &childExitReader{childErr: boom, failures: 1}
	n, err := readRetryingReuse(r, make([]byte, 8),
		func() bool { return false },
		func(time.Duration) { t.Fatal("unexpected reuse wait for unrelated error") })
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("Read = (%d, %v), want (0, boom)", n, err)
	}
}

// TestNoPTYBackendEOFIsTerminal makes sure the retry policy stays limited to
// PTYBackend: pipe-based backends still end at EOF.
func TestNoPTYBackendEOFIsTerminal(t *testing.T) {
	b := NewNoPTYBackend(bytes.NewReader(nil), io.Discard)
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestPTYBackendNoCommand(t *testing.T) {
	backend := &PTYBackend{}
	if pid := backend.PID(); pid != 0 {
		t.Fatalf("PID without Attach = %d, want 0", pid)
	}
	if err := backend.Wait(); err == nil {
		t.Fatal("Wait without Attach succeeded, want an error")
	}
}
