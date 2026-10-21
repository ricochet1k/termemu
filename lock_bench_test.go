package termemu

import (
	"testing"
)

func BenchmarkPlainRead(b *testing.B) {
	backend := NewNoPTYBackend(nil, nil)
	term := New(&EmptyFrontend{}, backend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = term.Size()
	}
}

func BenchmarkView(b *testing.B) {
	backend := NewNoPTYBackend(nil, nil)
	term := New(&EmptyFrontend{}, backend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.View(func(v LockedView) {
			_, _ = v.Size()
		})
	}
}
