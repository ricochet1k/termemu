package termemu

import (
	"bytes"
	"testing"
	"sync"
)

type dummyBackend struct {
	sync.Mutex
	buf *bytes.Buffer
}

func (d *dummyBackend) SetSize(w, h int) error { return nil }
func (d *dummyBackend) Close() error { return nil }
func (d *dummyBackend) Read(p []byte) (n int, err error) {
	d.Lock()
	defer d.Unlock()
	return d.buf.Read(p)
}
func (d *dummyBackend) Write(p []byte) (n int, err error) {
	d.Lock()
	defer d.Unlock()
	return d.buf.Write(p)
}

func TestDataRace_Frontend(t *testing.T) {
	backend := &dummyBackend{buf: new(bytes.Buffer)}
	for i := 0; i < 1000; i++ {
		_, _ = backend.Write([]byte("Hello, world! "))
	}

	term := New(&EmptyFrontend{}, backend)

	done := make(chan bool)
	go func() {
		for i := 0; i < 1000; i++ {
			term.SetFrontend(&EmptyFrontend{})
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 1000; i++ {
			term.WithLock(func() {
				term.Line(0)
			})
		}
		done <- true
	}()

	<-done
	<-done
}

func TestDataRace_Write(t *testing.T) {
	backend := &dummyBackend{buf: new(bytes.Buffer)}
	for i := 0; i < 1000; i++ {
		_, _ = backend.Write([]byte("Hello, world! "))
	}

	term := New(&EmptyFrontend{}, backend)

	done := make(chan bool)
	go func() {
		for i := 0; i < 1000; i++ {
			_, _ = term.Write([]byte("A"))
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 1000; i++ {
			term.WithLock(func() {
				term.Line(0)
			})
		}
		done <- true
	}()

	<-done
	<-done
}

func TestDataRace_Resize(t *testing.T) {
	backend := &dummyBackend{buf: new(bytes.Buffer)}
	for i := 0; i < 1000; i++ {
		_, _ = backend.Write([]byte("Hello, world! "))
	}

	term := New(&EmptyFrontend{}, backend)

	done := make(chan bool)
	go func() {
		for i := 0; i < 1000; i++ {
			_ = term.Resize(100+i%10, 100)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 1000; i++ {
			term.WithLock(func() {
				term.Line(0)
			})
		}
		done <- true
	}()

	<-done
	<-done
}
