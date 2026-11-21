package state

import (
	"github.com/google/uuid"
)

// ADR-20261031193635 stable branch URLs (amended 2026-11-12). A branch's
// workload URL is built from its slot instead of its environment ID: the slot
// is derived from the project and the branch's slug, so it stays the same
// when the branch is reset (deleted and made again under the same name),
// while an environment ID never repeats. The gateway resolves a slot to the
// environment of the branch that holds it (ProjectEnvironmentByID). Slots are
// SHA-1 name-based UUIDs (version 5); environment IDs are random (version 4),
// so the two never collide.

// environmentBranchSlotNamespace is the fixed namespace slots are derived in.
var environmentBranchSlotNamespace = uuid.MustParse("6c3b7f0e-2f4d-5a8e-9b1c-0d2e4f6a8b10")

// EnvironmentBranchSlotID is the slot of the branch named slug in projectID.
// A project ID in MemStore's compact form derives the same slot as its
// canonical form.
func EnvironmentBranchSlotID(projectID, slug string) string {
	if parsed, err := uuid.Parse(projectID); err == nil {
		projectID = parsed.String()
	}
	return uuid.NewSHA1(environmentBranchSlotNamespace, []byte(projectID+"\x00"+slug)).String()
}

// canonicalUUID returns id's canonical form, or id when it is not a UUID.
func canonicalUUID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}
