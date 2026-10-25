package api

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// workerAppHosts is deploy/cloudflare/public-timeout-worker/app-hosts.json:
// the public-timeout Worker's copy of which hosts are apps (ADR-1343). The
// Worker sends every app host to the compute-node app edge in its "*" mode,
// so a name reserved here but missing there would move a Gregale-owned host
// off the control plane.
type workerAppHosts struct {
	SlugPattern     string   `json:"slug_pattern"`
	InvalidPrefixes []string `json:"invalid_prefixes"`
	ReservedSlugs   []string `json:"reserved_slugs"`
	PlatformLabels  []string `json:"platform_labels"`
}

func loadWorkerAppHosts(t *testing.T) workerAppHosts {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/cloudflare/public-timeout-worker/app-hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	var hosts workerAppHosts
	if err := json.Unmarshal(raw, &hosts); err != nil {
		t.Fatal(err)
	}
	return hosts
}

func TestWorkerAppHostsMatchAppSlugRules(t *testing.T) {
	hosts := loadWorkerAppHosts(t)

	if hosts.SlugPattern != appSlugRE.String() {
		t.Errorf("app-hosts.json slug_pattern = %q, want appSlugRE %q", hosts.SlugPattern, appSlugRE.String())
	}

	want := make([]string, 0, len(reservedAppSlugs))
	for slug := range reservedAppSlugs {
		want = append(want, slug)
	}
	slices.Sort(want)
	got := slices.Sorted(slices.Values(hosts.ReservedSlugs))
	if !slices.Equal(got, want) {
		t.Errorf("app-hosts.json reserved_slugs = %v, want reservedAppSlugs %v", got, want)
	}

	// The Worker's rule (pattern, then the invalid prefixes) must agree with
	// ValidAppSlug on names either side of every boundary.
	workerValid := func(label string) bool {
		if !appSlugRE.MatchString(label) {
			return false
		}
		for _, prefix := range hosts.InvalidPrefixes {
			if strings.HasPrefix(label, prefix) {
				return false
			}
		}
		return true
	}
	for _, label := range []string{
		"abc", "ab", "a", "s3", "tag-shop", "tags-shop", "tag", "pr-7-shop",
		"-abc", "abc-", "a--b", "shop1", "Shop", "shop_1",
		strings.Repeat("a", 40), strings.Repeat("a", 41),
	} {
		if got, want := workerValid(label), ValidAppSlug(label); got != want {
			t.Errorf("label %q: Worker says app slug %v, ValidAppSlug %v", label, got, want)
		}
	}

	// Platform labels must never be allocatable as apps.
	for _, label := range hosts.PlatformLabels {
		if ValidAppSlug(label) && !IsReservedAppSlug(label) {
			t.Errorf("platform label %q is a valid, unreserved app slug; reserve it", label)
		}
	}
}
