package api

import (
	"strings"
	"testing"
)

func TestSuffixedAppSlugs(t *testing.T) {
	a := SuffixedAppSlugs("acct-1", "shop", 3)
	if len(a) != 3 {
		t.Fatalf("got %v, want 3 alternatives", a)
	}
	for _, s := range a {
		if !strings.HasPrefix(s, "shop-") || len(s) != len("shop-")+appSlugSuffixLen || !ValidAppSlug(s) {
			t.Errorf("alternative %q: want shop-<5 chars> and a valid slug", s)
		}
	}
	if again := SuffixedAppSlugs("acct-1", "shop", 3); strings.Join(again, ",") != strings.Join(a, ",") {
		t.Errorf("not deterministic: %v then %v", a, again)
	}
	if other := SuffixedAppSlugs("acct-2", "shop", 3); other[0] == a[0] {
		t.Errorf("two owners share the first alternative %q", a[0])
	}
	long := strings.Repeat("a", 38) + "-b"
	for _, s := range SuffixedAppSlugs("acct-1", long, 3) {
		if !ValidAppSlug(s) {
			t.Errorf("40-char base gave invalid alternative %q", s)
		}
	}
	// The cut lands on a hyphen: the stem must not end in "-" before the suffix.
	dash := strings.Repeat("a", 33) + "-bbbbbb"
	for _, s := range SuffixedAppSlugs("acct-1", dash, 3) {
		if strings.Contains(s, "--") || !ValidAppSlug(s) {
			t.Errorf("alternative %q of %q", s, dash)
		}
	}
}
