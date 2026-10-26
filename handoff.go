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

// ErrPauseUnsupported is returned by Pause when the backend cannot interrupt a
// blocked read (it implements neither SetReadDeadline nor Wake). Waiting for
// the next byte would make Pause unbounded, so it fails instead.
var ErrPauseUnsupported = errors.New("termemu: Pause requires a backend that can interrupt a blocked read")

// ReaderState is the read-loop state that lives in user space: bytes already
// read from the backend but not applied, plus any partial escape command.
type ReaderState struct {
	Pending        []byte // GraphemeReader buffered bytes (data[start:end])
	PendingCommand []byte // partial ESC-initiated command (ESC already consumed)
	State          int    // GraphemeReader UTF-8/grapheme state
	// Initial marks a reader that has not processed any bytes yet; State is
	// then ignored and the reader starts from the tokenizer's initial state.
	Initial          bool
	ForceMergeNext   bool
	LastWasRI        bool
	RunHasBase       bool
	LastWasHanging   bool
	LastWasSynthetic bool
	Mode             TextReadMode
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
// It leaves the terminal paused. The backend must be able to interrupt a
// blocked read, either through SetReadDeadline or Wake; otherwise Pause
// returns ErrPauseUnsupported instead of waiting for the next byte.
//
// For a cross-process handoff, call Pause, transfer the backend file
// descriptors (PTYBackend.Master/Slave) plus the Handoff value, and then close
// this terminal. The receiving process adopts the fds and calls
// NewFromHandoff.
func (t *terminal) Pause(ctx context.Context) (Handoff, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	t.lifecycleMu.Lock()
	if t.closed {
		t.lifecycleMu.Unlock()
		return Handoff{}, errors.New("termemu: terminal is closed")
	}
	if t.paused {
		t.lifecycleMu.Unlock()
		return Handoff{}, errors.New("termemu: terminal is already paused")
	}
	started := t.readLoopStarted
	backend := t.backend
	_, hasDeadline := backend.(interface{ SetReadDeadline(time.Time) error })
	_, hasWake := backend.(interface{ Wake() })
	if started && !hasDeadline && !hasWake {
		t.lifecycleMu.Unlock()
		return Handoff{}, ErrPauseUnsupported
	}
	t.paused = true
	t.pausedFlag.Store(true)
	ready := make(chan ReaderState, 1)
	resume := make(chan bool, 1)
	t.pauseReady = ready
	t.pauseResume = resume
	t.lifecycleMu.Unlock()
	mode := t.textReadMode

	if !started {
		return t.captureHandoff(ReaderState{Mode: mode, Initial: true})
	}

	if setter, ok := backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		if err := setter.SetReadDeadline(time.Now()); err != nil {
			if !errors.Is(err, errors.ErrUnsupported) {
				t.abortPause()
				return Handoff{}, fmt.Errorf("termemu: Pause: set read deadline: %w", err)
			}
			if !hasWake {
				t.abortPause()
				return Handoff{}, ErrPauseUnsupported
			}
		}
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
	t.lifecycleMu.Lock()
	t.paused = false
	t.pausedFlag.Store(false)
	resume := t.pauseResume
	t.pauseReady = nil
	t.pauseResume = nil
	t.lifecycleMu.Unlock()
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
	sb := t.scrollback.scrollbackSnapshotLocked()
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
	t.lifecycleMu.Lock()
	if !t.paused {
		t.lifecycleMu.Unlock()
		return nil
	}
	resume := t.pauseResume
	t.paused = false
	t.pausedFlag.Store(false)
	t.pauseReady = nil
	t.pauseResume = nil
	t.lifecycleMu.Unlock()

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
	if !t.pausedFlag.Load() {
		return false
	}
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	return t.paused && t.pauseReady != nil && t.pauseResume != nil
}

// isPaused reports whether a pause was requested (used to distinguish a
// deadline wake from a genuine read error).
func (t *terminal) isPaused() bool {
	if !t.pausedFlag.Load() {
		return false
	}
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	return t.paused
}

// pauseLoop sends the loop's state to Pause and waits for Resume (true) or an
// abort/exit (false). It returns false when the loop should exit.
func (t *terminal) pauseLoop(state ReaderState) bool {
	t.lifecycleMu.Lock()
	if !t.paused || t.pauseReady == nil || t.pauseResume == nil {
		t.lifecycleMu.Unlock()
		return true
	}
	ready, resume := t.pauseReady, t.pauseResume
	t.lifecycleMu.Unlock()

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
	backend := t.backend
	if setter, ok := backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = setter.SetReadDeadline(time.Time{})
	}
}

// clearReadDeadlineIfIdle clears the deadline unless a new Pause has already
// installed one. This avoids a resumed loop clobbering a fresh pause request.
func (t *terminal) clearReadDeadlineIfIdle() {
	t.lifecycleMu.Lock()
	paused := t.paused
	t.lifecycleMu.Unlock()
	if paused {
		return
	}
	t.clearReadDeadline()
}

// resizeSnapshotScreen retargets a screen snapshot to a new size, applying
// the same vertical-margin adjustment as spanScreen.setSize: the bottom margin
// keeps its distance from the bottom edge, so a full-screen region stays
// full-screen after growing. The capture dimensions are updated as well.
func resizeSnapshotScreen(st *ScreenState, w, h int) {
	if st.Width != w || st.Height != h {
		oldH := st.Height
		st.BottomMargin = h - (oldH - st.BottomMargin)
		st.TopMargin = clamp(st.TopMargin, 0, h-1)
		st.BottomMargin = clamp(st.BottomMargin, st.TopMargin, h-1)
	}
	st.Width, st.Height = w, h
}

// NewFromHandoff creates a terminal from a Handoff and starts its read loop
// with the captured reader state. The terminal is created paused (no reads
// happen) until the snapshot and scrollback have been restored. Handoff.Size
// is the target size; if the snapshot was captured at a different size its
// content is resized to fit. The caller owns backend and must arrange any
// cross-process fd transfer beforehand (PTYBackend.Adopt/AdoptMaster). On a
// restore error the backend is closed.
func NewFromHandoff(f Frontend, backend Backend, h Handoff, opts ...TerminalOption) (Terminal, error) {
	if backend == nil {
		return nil, fmt.Errorf("%w: nil backend", ErrHandoffInvalid)
	}
	if h.Size.X <= 0 || h.Size.Y <= 0 {
		return nil, fmt.Errorf("%w: invalid size %dx%d", ErrHandoffInvalid, h.Size.X, h.Size.Y)
	}
	if h.Snapshot.Main.Width <= 0 || h.Snapshot.Main.Height <= 0 ||
		h.Snapshot.Alt.Width <= 0 || h.Snapshot.Alt.Height <= 0 {
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

	// The receiving process may have a different window size. Resize the
	// screen and pty to Handoff.Size, then restore the captured content into
	// that size (lines are truncated/padded, cursor and margins clamp).
	t.mainScreen.setSize(h.Size.X, h.Size.Y)
	t.altScreen.setSize(h.Size.X, h.Size.Y)
	if err := backend.SetSize(h.Size.X, h.Size.Y); err != nil {
		_ = backend.Close()
		return nil, err
	}
	snap := h.Snapshot
	resizeSnapshotScreen(&snap.Main, h.Size.X, h.Size.Y)
	resizeSnapshotScreen(&snap.Alt, h.Size.X, h.Size.Y)
	if err := t.Restore(snap); err != nil {
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
