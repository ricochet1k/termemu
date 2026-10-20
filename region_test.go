package termemu

import "testing"

func TestRegionClamp(t *testing.T) {
	bounds := Region{X: 0, Y: 0, X2: 80, Y2: 24}

	tests := []struct {
		name string
		in   Region
		want Region
	}{
		{"inside", Region{X: 1, Y: 2, X2: 3, Y2: 4}, Region{X: 1, Y: 2, X2: 3, Y2: 4}},
		{"negative", Region{X: -5, Y: -5, X2: 10, Y2: 10}, Region{X: 0, Y: 0, X2: 10, Y2: 10}},
		{"past far edge", Region{X: 70, Y: 20, X2: 200, Y2: 200}, Region{X: 70, Y: 20, X2: 80, Y2: 24}},
		{"inverted", Region{X: 50, Y: 20, X2: 10, Y2: 5}, Region{X: 50, Y: 20, X2: 50, Y2: 20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Clamp(bounds); got != tt.want {
				t.Errorf("Clamp(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
