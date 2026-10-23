//go:build unix

package termemu

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// readWithTimeout runs a single Read and fails if it does not return promptly.
func readWithTimeout(t *testing.T, backend *PTYBackend, buf []byte) (int, error) {
	t.Helper()
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := backend.Read(buf)
		ch <- result{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("PTYBackend.Read blocked")
		return 0, nil
	}
}

// TestPTYBackendAdoptMasterOnly covers adopting a master without a slave:
// reads/writes/resize work, Attach/Open fail, and EOF/EIO is surfaced instead
// of waiting for reuse.
func TestPTYBackendAdoptMasterOnly(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer slave.Close()

	backend := &PTYBackend{}
	if err := backend.AdoptMaster(master); err != nil {
		t.Fatalf("AdoptMaster: %v", err)
	}

	if f, err := backend.Open(); err == nil || f != nil {
		t.Fatalf("Open on master-only backend = (%v, %v), want error", f, err)
	}
	if err := backend.Attach(exec.Command("sh", "-c", "true")); err == nil {
		t.Fatal("Attach on master-only backend should fail")
	}

	if _, err := slave.Write([]byte("adopted_master_only\n")); err != nil {
		t.Fatalf("write slave: %v", err)
	}
	buf := make([]byte, 256)
	n, err := readWithTimeout(t, backend, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "adopted_master_only") {
		t.Fatalf("Read = %q, want adopted_master_only", buf[:n])
	}

	if err := backend.SetSize(100, 40); err != nil {
		t.Fatalf("SetSize: %v", err)
	}
	if sz, err := pty.GetsizeFull(master); err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	} else if sz.Cols != 100 || sz.Rows != 40 {
		t.Fatalf("pty size = %dx%d, want 100x40", sz.Cols, sz.Rows)
	}

	// Closing the slave must make the next Read return EOF/EIO promptly; the
	// master-only path must not enter the child-reuse retry loop. Drain any
	// bytes still queued from the write above.
	if err := slave.Close(); err != nil {
		t.Fatalf("close slave: %v", err)
	}
	deadline := time.After(5 * time.Second)
	errc := make(chan error, 1)
	go func() {
		b := make([]byte, 256)
		for {
			if _, err := backend.Read(b); err != nil {
				errc <- err
				return
			}
		}
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, syscall.EIO) && !errors.Is(err, io.EOF) {
			t.Fatalf("Read after slave close = %v, want EOF or EIO", err)
		}
	case <-deadline:
		t.Fatal("master-only Read did not surface EOF/EIO; it is waiting for reuse")
	}

	if err := backend.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := backend.Read(make([]byte, 8)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read after Close = %v, want io.ErrClosedPipe", err)
	}
}

// TestPTYBackendAdoptPair verifies that a full adopted pair still supports
// Attach, running commands, and reuse.
func TestPTYBackendAdoptPair(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}

	backend := &PTYBackend{}
	if err := backend.Adopt(master, slave); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	term := New(NewMockFrontend(), backend)
	if term == nil {
		t.Fatal("New returned nil")
	}
	defer term.Close()

	run := func(script string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		if err := backend.Attach(cmd); err != nil {
			t.Fatalf("Attach: %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}

	run("printf first_adopted")
	waitForLine(t, term, 0, "first_adopted")

	run(`printf '\nsecond_adopted'`)
	waitForLine(t, term, 1, "second_adopted")
}

func TestPTYBackendAdoptRejects(t *testing.T) {
	t.Run("nil master", func(t *testing.T) {
		backend := &PTYBackend{}
		if err := backend.Adopt(nil, nil); err == nil {
			t.Fatal("Adopt(nil, nil) should fail")
		}
	})

	t.Run("after Open", func(t *testing.T) {
		backend := &PTYBackend{}
		if _, err := backend.Open(); err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer backend.Close()

		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("pty.Open: %v", err)
		}
		defer master.Close()
		defer slave.Close()
		if err := backend.Adopt(master, slave); err == nil {
			t.Fatal("Adopt after Open should fail")
		}
	})

	t.Run("twice", func(t *testing.T) {
		backend := &PTYBackend{}
		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("pty.Open: %v", err)
		}
		if err := backend.Adopt(master, slave); err != nil {
			t.Fatalf("first Adopt: %v", err)
		}
		defer backend.Close()

		master2, slave2, err := pty.Open()
		if err != nil {
			t.Fatalf("second pty.Open: %v", err)
		}
		defer master2.Close()
		defer slave2.Close()
		if err := backend.Adopt(master2, slave2); err == nil {
			t.Fatal("second Adopt should fail")
		}
	})

	t.Run("after Close", func(t *testing.T) {
		backend := &PTYBackend{}
		if err := backend.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("pty.Open: %v", err)
		}
		defer master.Close()
		defer slave.Close()
		if err := backend.Adopt(master, slave); err == nil {
			t.Fatal("Adopt after Close should fail")
		}
	})
}
