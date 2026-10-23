package termemu

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrHandoffInvalid is returned by NewFromHandoff when the handoff is missing
// required state (a backend, a positive size, or matching snapshot
// dimensions).
var ErrHandoffInvalid = errors.New("termemu: invalid handoff")

// ReaderState is the read-loop state that lives in user space: bytes already
// read from the backend but not applied, plus any partial escape command.
type ReaderState struct {
	Pending        []byte // GraphemeReader buffered bytes (data[start:end])
	PendingCommand []byte // partial ESC-initiated command (ESC already consumed)
	State          int    // GraphemeReader UTF-8/grapheme state
	// Initial marks a reader that has not processed any bytes yet; State is
	// then ignored and the reader starts from the tokenizer's initial state.
	Initial        bool
	ForceMergeNext bool
	LastWasRI      bool
	Mode           TextReadMode
}

// Handoff is everything needed to resume a terminal in a new process.
// Backend fds are transferred separately (see PTYBackend.Master/Slave).
type Handoff struct {
	Snapshot   Snapshot
	Scrollback ScrollbackSnapshot
	Reader     ReaderState
	Size       Pos
	TextMode   TextReadMode
}

// Pause stops the read loop and captures everything needed to resume the
// terminal, either in this process (Resume) or another one (NewFromHandoff).
// It leaves the terminal paused. If the backend supports SetReadDeadline the
// blocked read is interrupted immediately; otherwise Pause may wait for the
// next byte (or ctx cancellation).
//
// For a cross-process handoff, call Pause, transfer the backend file
// descriptors (PTYBackend.Master/Slave) plus the Handoff value, and then close
// this terminal. The receiving process adopts the fds and calls
// NewFromHandoff.
func (t *terminal) Pause(ctx context.Context) (Handoff, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	t.Lock()
	if t.closed {
		t.Unlock()
		return Handoff{}, errors.New("termemu: terminal is closed")
	}
	if t.paused {
		t.Unlock()
		return Handoff{}, errors.New("termemu: terminal is already paused")
	}
	t.paused = true
	ready := make(chan ReaderState, 1)
	resume := make(chan bool, 1)
	t.pauseReady = ready
	t.pauseResume = resume
	started := t.readLoopStarted
	backend := t.backend
	mode := t.textReadMode
	t.Unlock()

	if !started {
		return t.captureHandoff(ReaderState{Mode: mode, Initial: true})
	}

	if setter, ok := backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = setter.SetReadDeadline(time.Now())
	}
	// Also wake a loop sleeping in the reuse backoff (child exited); a read
	// deadline alone does not interrupt that timer.
	if waker, ok := backend.(interface{ Wake() }); ok {
		waker.Wake()
	}

	var reader ReaderState
	select {
	case reader = <-ready:
		// The loop is parked on pauseResume.
	case <-ctx.Done():
		t.abortPause()
		return Handoff{}, ctx.Err()
	case <-t.Done():
		t.abortPause()
		return Handoff{}, errors.New("termemu: read loop stopped before pause")
	}

	if reader.Mode != mode {
		reader.Mode = mode
	}
	return t.captureHandoff(reader)
}

// abortPause undoes a paused state and wakes a loop that may be about to park.
// The resume signal is sent after the deadline is cleared so the loop cannot
// immediately observe a stale expired deadline.
func (t *terminal) abortPause() {
	t.Lock()
	t.paused = false
	resume := t.pauseResume
	t.pauseReady = nil
	t.pauseResume = nil
	t.Unlock()
	t.clearReadDeadline()
	if resume != nil {
		select {
		case resume <- true:
		default:
		}
	}
}

// captureHandoff takes the screen, scrollback, and size snapshots. The read
// loop must not be applying output while this runs.
func (t *terminal) captureHandoff(reader ReaderState) (Handoff, error) {
	t.Lock()
	snap := t.snapshotLocked()
	size := t.screen().Size()
	sb := (&Scrollback{t: t}).scrollbackSnapshotLocked()
	mode := t.textReadMode
	t.Unlock()

	return Handoff{
		Snapshot:   snap,
		Scrollback: sb,
		Reader:     reader,
		Size:       size,
		TextMode:   mode,
	}, nil
}

// Resume continues a same-process paused read loop. It clears any read
// deadline before waking the loop so the first resumed read cannot observe an
// expired deadline. Resume on a non-paused terminal is a no-op.
func (t *terminal) Resume() error {
	t.Lock()
	if !t.paused {
		t.Unlock()
		return nil
	}
	resume := t.pauseResume
	t.paused = false
	t.pauseReady = nil
	t.pauseResume = nil
	t.Unlock()

	// Clear before waking the loop (see pauseLoop/abortPause ordering).
	t.clearReadDeadline()
	if resume != nil {
		select {
		case resume <- true:
		default:
		}
	}
	return nil
}

// shouldPause reports whether the read loop should park before its next read.
func (t *terminal) shouldPause() bool {
	t.Lock()
	defer t.Unlock()
	return t.paused && t.pauseReady != nil && t.pauseResume != nil
}

// isPaused reports whether a pause was requested (used to distinguish a
// deadline wake from a genuine read error).
func (t *terminal) isPaused() bool {
	t.Lock()
	defer t.Unlock()
	return t.paused
}

// pauseLoop sends the loop's state to Pause and waits for Resume (true) or an
// abort/exit (false). It returns false when the loop should exit.
func (t *terminal) pauseLoop(state ReaderState) bool {
	t.Lock()
	if !t.paused || t.pauseReady == nil || t.pauseResume == nil {
		t.Unlock()
		return true
	}
	ready, resume := t.pauseReady, t.pauseResume
	t.Unlock()

	select {
	case ready <- state:
	default:
	}

	if !<-resume {
		return false
	}
	t.clearReadDeadlineIfIdle()
	return true
}

// clearReadDeadline clears the backend read deadline, if supported.
func (t *terminal) clearReadDeadline() {
	t.Lock()
	backend := t.backend
	t.Unlock()
	if setter, ok := backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = setter.SetReadDeadline(time.Time{})
	}
}

// clearReadDeadlineIfIdle clears the deadline unless a new Pause has already
// installed one. This avoids a resumed loop clobbering a fresh pause request.
func (t *terminal) clearReadDeadlineIfIdle() {
	t.Lock()
	paused := t.paused
	backend := t.backend
	t.Unlock()
	if paused {
		return
	}
	if setter, ok := backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = setter.SetReadDeadline(time.Time{})
	}
}

// NewFromHandoff creates a terminal from a Handoff and starts its read loop
// with the captured reader state. The terminal is created paused (no reads
// happen) until the snapshot and scrollback have been restored. The caller
// owns backend and must arrange any cross-process fd transfer beforehand
// (PTYBackend.Adopt/AdoptMaster). On a restore error the backend is closed.
func NewFromHandoff(f Frontend, backend Backend, h Handoff, opts ...TerminalOption) (Terminal, error) {
	if backend == nil {
		return nil, fmt.Errorf("%w: nil backend", ErrHandoffInvalid)
	}
	if h.Size.X <= 0 || h.Size.Y <= 0 {
		return nil, fmt.Errorf("%w: invalid size %dx%d", ErrHandoffInvalid, h.Size.X, h.Size.Y)
	}
	if h.Snapshot.Main.Width != h.Size.X || h.Snapshot.Main.Height != h.Size.Y ||
		h.Snapshot.Alt.Width != h.Size.X || h.Snapshot.Alt.Height != h.Size.Y {
		return nil, ErrSnapshotSizeMismatch
	}

	mode := h.Reader.Mode
	if h.TextMode == TextReadModeGrapheme {
		mode = TextReadModeGrapheme
	}
	h.Reader.Mode = mode

	t := newTerminal(f, backend, mode)
	for _, opt := range opts {
		opt(t)
	}

	t.mainScreen.setSize(h.Size.X, h.Size.Y)
	t.altScreen.setSize(h.Size.X, h.Size.Y)
	if err := backend.SetSize(h.Size.X, h.Size.Y); err != nil {
		_ = backend.Close()
		return nil, err
	}
	if err := t.Restore(h.Snapshot); err != nil {
		_ = backend.Close()
		return nil, err
	}
	// A zero snapshot is a valid empty scrollback buffer (the raw snapshot
	// type uses 0 for both Base and Next).
	if h.Scrollback.Next != 0 || len(h.Scrollback.Rows) != 0 {
		if err := t.Scrollback().Restore(h.Scrollback); err != nil {
			_ = backend.Close()
			return nil, err
		}
	}

	if setter, ok := f.(interface{ SetTerminal(Terminal) }); ok {
		setter.SetTerminal(t)
	}
	t.startReadLoopFromState(h.Reader)
	return t, nil
}
