package state

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8KeepsRuneBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"abcdef", 3, "abc"},
		{"aé", 2, "a"},
		{"éé", 3, "é"},
		{"日本", 4, "日"},
		{"é", 0, ""},
	} {
		got := truncateUTF8(tc.in, tc.n)
		if got != tc.want || len(got) > tc.n || !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
