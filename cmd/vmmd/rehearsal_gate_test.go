package main

import "testing"

func TestRehearsalContainmentEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"true", false},
		{"1", true},
	} {
		got := rehearsalContainmentEnabled(func(key string) string {
			if key != "FAAS_REHEARSAL_NETWORK_CONTAINMENT" {
				t.Fatalf("unexpected key %q", key)
			}
			return tc.value
		})
		if got != tc.want {
			t.Errorf("FAAS_REHEARSAL_NETWORK_CONTAINMENT=%q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}
