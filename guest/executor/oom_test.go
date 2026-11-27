package executor

import (
	"strings"
	"testing"
)

func TestGuestOOMCounterRequiresValidEvidence(t *testing.T) {
	for _, tc := range []struct {
		text  string
		count uint64
		ok    bool
	}{
		{"nr_free_pages 12\noom_kill 3\n", 3, true},
		{"oom_kill 0\n", 0, true},
		{"oom_kill -1\n", 0, false},
		{"oom_kill invalid\n", 0, false},
		{"nr_free_pages 12\n", 0, false},
	} {
		value, ok := guestOOMKills(strings.NewReader(tc.text))
		if value != tc.count || ok != tc.ok {
			t.Fatalf("%q: %d/%v, want %d/%v", tc.text, value, ok, tc.count, tc.ok)
		}
	}
}
