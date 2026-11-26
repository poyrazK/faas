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

// An unclaimed account's app always carries the account's suffix, fits the
// slug rules and keeps its name on a redeploy.
func TestAnonymousAppSlug(t *testing.T) {
	const acct = "8b0c9f5e-1d2a-4f61-9a3b-2c7d5e6f7a80"
	suffix := AnonymousAppSlugSuffix(acct)
	if len(suffix) != 1+appSlugSuffixLen || suffix == AnonymousAppSlugSuffix("other-account") {
		t.Fatalf("suffix = %q, want six characters that differ per account", suffix)
	}
	for _, base := range []string{"app", "my-api", strings.Repeat("a", 40), strings.Repeat("b", 33) + "-c"} {
		got := AnonymousAppSlug(acct, base)
		if !strings.HasSuffix(got, suffix) || !ValidAppSlug(got) || strings.Contains(got, "--") {
			t.Errorf("AnonymousAppSlug(%q) = %q, want a valid name ending %s", base, got, suffix)
		}
		if again := AnonymousAppSlug(acct, got); again != got {
			t.Errorf("redeploy of %q renamed it to %q", got, again)
		}
	}
}
