package state

import "testing"

func TestManualTrafficExplicit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		prior, next   int
		current, want bool
	}{
		{"split member", 0, 50, false, true},
		{"split shrinks", 100, 50, false, true},
		{"split returns to stable", 50, 100, true, false},
		{"split member lowered to zero", 50, 0, true, false},
		{"unchanged keeps flag", 30, 30, true, true},
		{"stable promote", 0, 100, false, false},
		{"stable retained at zero", 100, 0, false, false},
		// A runtime upgrade candidate rolled back and reactivated stays marked.
		{"upgrade candidate rolled back", 100, 0, true, true},
		{"upgrade candidate reactivated", 0, 100, true, true},
	} {
		if got := manualTrafficExplicit(tc.prior, tc.next, tc.current); got != tc.want {
			t.Errorf("%s: manualTrafficExplicit(%d, %d, %v) = %v, want %v", tc.name, tc.prior, tc.next, tc.current, got, tc.want)
		}
	}
}
