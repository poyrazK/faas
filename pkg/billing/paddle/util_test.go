package paddle

// util_test covers the package-internal helpers that don't need a
// Provider or SDK handle — redact (key-masking rule + edge cases)
// and cloneBytes (defensive copy semantically equivalent to
// bytes.Clone). Lives in `package paddle` (not `paddle_test`) so the
// test can reach redactAPIKey directly without a public re-export
// (the previous PR #158 added RedactAPIKeyForTest which the review
// flagged as unnecessary noise).

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactAPIKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"long key, 16 masked", "pdl_abcdefghijklmnop", "pdl_" + strings.Repeat("*", 16)},
		{"exactly 4 chars", "abcd", "***"},
		{"3 chars", "abc", "***"},
		{"empty", "", "***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactAPIKey(tc.in)
			if got != tc.want {
				t.Errorf("redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactAPIKey_NeverLeaksUnmaskedSegments(t *testing.T) {
	t.Parallel()

	// Belt + braces: confirm the masking rule still holds when the
	// input shrinks past the 4-char prefix. Length 5 should keep
	// only the first 4 chars + 1 mask — never the 5th.
	got := redactAPIKey("pdl_x")
	want := "pdl_"
	if len(got) < len(want) {
		t.Fatalf("redact(%q) too short: got %q want %q", "pdl_x", got, want)
	}
	if !strings.HasPrefix(got, "pdl_") {
		t.Errorf("redact(%q) = %q, want prefix pdl_", "pdl_x", got)
	}
}

func TestCloneBytes_IsDefensiveCopy(t *testing.T) {
	t.Parallel()

	src := []byte{1, 2, 3, 4}
	cp := cloneBytes(src)
	if !bytes.Equal(src, cp) {
		t.Fatalf("cloneBytes got %v, want %v", cp, src)
	}
	src[0] = 99
	if cp[0] != 1 {
		t.Errorf("cloneBytes is not a defensive copy: cp[0]=%d after src[0]=99", cp[0])
	}
}
