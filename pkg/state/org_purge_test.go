package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Prod hunt r73: a deleted organization kept its slug forever. After the
// window its slug is released, and only then.
func TestMemStorePurgeDeletedOrgsReleasesTheSlug(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	owner, err := m.CreateAccount(ctx, "purge-owner@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	org, err := m.CreateSharedOrg(ctx, owner.ID, "audit-org", "Audit")
	if err != nil {
		t.Fatalf("CreateSharedOrg: %v", err)
	}
	if err := m.SoftDeleteOrg(ctx, org.ID); err != nil {
		t.Fatalf("SoftDeleteOrg: %v", err)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if got, _ := m.PurgeDeletedOrgs(ctx, cutoff, 10); len(got) != 0 {
		t.Fatalf("an org deleted just now was purged: %v", got)
	}
	m.mu.Lock()
	o := m.orgs[org.ID]
	old := time.Now().Add(-31 * 24 * time.Hour)
	o.DeletionRequestedAt = &old
	m.orgs[org.ID] = o
	m.mu.Unlock()

	got, err := m.PurgeDeletedOrgs(ctx, cutoff, 10)
	if err != nil || len(got) != 1 || got[0].ID != org.ID || got[0].Slug != "audit-org" {
		t.Fatalf("PurgeDeletedOrgs = %v, %v", got, err)
	}
	if _, err := m.OrgBySlug(ctx, "audit-org"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old slug still resolves: %v", err)
	}
	purged, err := m.OrgByID(ctx, org.ID)
	if err != nil || purged.Slug != PurgedOrgSlug(org.ID) || !purged.DeletedPending {
		t.Fatalf("purged org = %+v, %v", purged, err)
	}
	if got, _ := m.PurgeDeletedOrgs(ctx, cutoff, 10); len(got) != 0 {
		t.Fatalf("a purged org was purged again: %v", got)
	}
}

func TestPurgedOrgSlugFitsTheSlugShape(t *testing.T) {
	slug := PurgedOrgSlug("2426a324-2384-4550-a6a8-686034d8a669")
	if len(slug) > 32 || !IsPurgedOrgSlug(slug) || slug != "purged-2426a32423844550a6a86860" {
		t.Fatalf("slug = %q", slug)
	}
}
