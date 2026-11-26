package api

import (
	"crypto/sha256"
	"encoding/base32"
	"slices"
	"strconv"
	"strings"
)

// appSlugSuffixLen is the length of the suffix SuffixedAppSlugs adds.
const appSlugSuffixLen = 5

var appSlugSuffixEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// SuffixedAppSlugs returns up to n alternative names for base, for a deploy
// whose name another owner holds: base (shortened to fit) + "-" + five
// characters derived from owner and base. The same owner asking for the same
// name always gets the same alternatives, so a fresh CI checkout with no link
// finds the app the previous run created instead of creating another one.
// Invalid and reserved alternatives are left out.
func SuffixedAppSlugs(owner, base string, n int) []string {
	stem := base
	if limit := 40 - 1 - appSlugSuffixLen; len(stem) > limit {
		stem = stem[:limit]
	}
	stem = strings.TrimRight(stem, "-")
	var out []string
	for i := 0; len(out) < n && i < n+4; i++ {
		sum := sha256.Sum256([]byte("gregale-app-slug-suffix\x00" + owner + "\x00" + base + "\x00" + strconv.Itoa(i)))
		candidate := stem + "-" + appSlugSuffixEncoding.EncodeToString(sum[:])[:appSlugSuffixLen]
		if ValidAppSlug(candidate) && !IsReservedAppSlug(candidate) && !slices.Contains(out, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}
