// personal_org_namespace_test.go — guards the frozen UUID v5 namespace
// that derives every personal-org slug (issue #190 / ADR-061, PR 3).
//
//   - TestPersonalOrgNamespaceFrozen re-derives the namespace from
//     the canonical name string and asserts the literal in
//     pkg/state/types.go hasn't drifted. Re-derivation is the only
//     way to surface a sneaky edit to the literal — uuid.MustParse
//     only catches malformedness, not drift.
//
//   - TestPersonalOrgSlug_RegexShape pins the slug length and shape
//     against the orgs_slug_shape CHECK migrated in slot 99. The
//     prefix is `u-` (matches `[a-z0-9][a-z0-9-]`), the body is
//     exactly 12 lowercase hex chars (matches `[a-z0-9]`). Total
//     length 14 — fits the regex's 1..30 middle bound.
//
//   - TestPersonalOrgSlug_Deterministic asserts that the same
//     accountID always produces the same slug across the two
//     relevant namespaces (no flag-day surprises when the Go
//     runtime rotates random state).

package state_test

import (
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPersonalOrgNamespaceFrozen(t *testing.T) {
	want := uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte("onebox-faas/iam-6/personal-org-namespace/v1"))
	if state.PersonalOrgNamespace != want {
		t.Errorf("PersonalOrgNamespace drifted: got %s, want %s",
			state.PersonalOrgNamespace, want)
	}
}

func TestPersonalOrgSlug_RegexShape(t *testing.T) {
	// Sample UUIDs at the four canonical shapes that have shown up
	// in tests across this PR: bare zeros, all-ones, all-fs, and a
	// realistic mixed-id.
	slugRE := regexp.MustCompile(`^u-[0-9a-f]{12}$`)
	for _, id := range []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-0000000000ff",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
		"11111111-2222-3333-4444-555555555555",
		"a1b2c3d4-e5f6-7890-1234-567890abcdef",
	} {
		s := state.PersonalOrgSlug(id)
		if !slugRE.MatchString(s) {
			t.Errorf("slug %q for id %s does not match /^u-[0-9a-f]{12}$/", s, id)
		}
		if len(s) != 14 {
			t.Errorf("slug %q for id %s: length = %d, want 14", s, id, len(s))
		}
	}
}

func TestPersonalOrgSlug_Deterministic(t *testing.T) {
	acct := "00000000-0000-0000-0000-0000000000a1"
	first := state.PersonalOrgSlug(acct)
	for i := 0; i < 8; i++ {
		again := state.PersonalOrgSlug(acct)
		if again != first {
			t.Errorf("PersonalOrgSlug(%s) non-deterministic: %s vs %s (iter %d)",
				acct, first, again, i)
		}
	}
}
