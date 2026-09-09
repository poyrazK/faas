package main

import "testing"

func TestPrewarmEnabledIsExactOptIn(t *testing.T) {
	for value, want := range map[string]bool{"1": true, " 1 ": true, "": false, "0": false, "true": false} {
		if got := prewarmEnabled(value); got != want {
			t.Errorf("prewarmEnabled(%q) = %v, want %v", value, got, want)
		}
	}
}
