package termemu

import (
	"testing"
)

func BenchmarkManualLock(b *testing.B) {
	backend := NewNoPTYBackend(nil, nil)
	term := New(&EmptyFrontend{}, backend)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.Lock()
		_, _ = term.Size()
		term.Unlock()
	}
}

func BenchmarkWithLock(b *testing.B) {
	backend := NewNoPTYBackend(nil, nil)
	term := New(&EmptyFrontend{}, backend)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.WithLock(func() {
			_, _ = term.Size()
		})
	}
}
