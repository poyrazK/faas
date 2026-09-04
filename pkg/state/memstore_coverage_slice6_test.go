package state

import (
	"errors"
	"testing"
	"time"
)

func TestMemStoreCoverageWebhookDedupe(t *testing.T) {
	m, ctx, _, _, _ := memCoverageFixture(t)
	now := time.Now().UTC()

	// Fresh store → no replay, no deliveries, empty sweep.
	if replay, err := m.CheckWebhookReplay(ctx, "stripe", "del-1", now.Add(-5*time.Minute)); err != nil || replay {
		t.Fatalf("fresh replay check = %v, %v", replay, err)
	}
	if n, err := m.SweepExpiredWebhookDeliveries(ctx, now); err != nil || n != 0 {
		t.Fatalf("fresh sweep = %d, %v", n, err)
	}
	claimed, err := m.ClaimWebhookDelivery(ctx, "polar", "del-atomic", now.Add(-5*time.Minute), now.Add(5*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("first atomic claim = %v, %v", claimed, err)
	}
	claimed, err = m.ClaimWebhookDelivery(ctx, "polar", "del-atomic", now.Add(-5*time.Minute), now.Add(5*time.Minute))
	if err != nil || claimed {
		t.Fatalf("duplicate atomic claim = %v, %v", claimed, err)
	}
	// Record → replay within TTL is true.
	if err := m.RecordWebhookDelivery(ctx, "stripe", "del-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if replay, err := m.CheckWebhookReplay(ctx, "stripe", "del-1", now.Add(-5*time.Minute)); err != nil || !replay {
		t.Fatalf("replay hit = %v, %v", replay, err)
	}
	// Different provider / delivery → miss.
	if replay, err := m.CheckWebhookReplay(ctx, "paddle", "del-1", now.Add(-5*time.Minute)); err != nil || replay {
		t.Fatalf("replay wrong provider = %v, %v", replay, err)
	}
	// Expired row (expires_at before cutoff) → treated as miss.
	if err := m.RecordWebhookDelivery(ctx, "github", "del-old", now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if replay, err := m.CheckWebhookReplay(ctx, "github", "del-old", now.Add(-5*time.Minute)); err != nil || replay {
		t.Fatalf("replay expired = %v, %v", replay, err)
	}
	// Sweep removes the expired row, keeps the fresh one.
	if n, err := m.SweepExpiredWebhookDeliveries(ctx, now); err != nil || n != 1 {
		t.Fatalf("sweep count = %d, %v", n, err)
	}
	if replay, _ := m.CheckWebhookReplay(ctx, "stripe", "del-1", now.Add(-5*time.Minute)); !replay {
		t.Fatal("fresh row should survive sweep")
	}
	// Refresh (ON CONFLICT DO UPDATE) extends the expiry.
	if err := m.RecordWebhookDelivery(ctx, "stripe", "del-1", now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if replay, _ := m.CheckWebhookReplay(ctx, "stripe", "del-1", now.Add(5*time.Minute)); !replay {
		t.Fatal("refreshed row should be replayable")
	}
}

func TestMemStoreCoverageAppSecretsAccountScoped(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	if err := m.UpsertAppSecret(ctx, account.ID, app.ID, "DB_URL", []byte("cipher-a")); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertAppSecret(ctx, account.ID, app.ID, "API_KEY", []byte("cipher-b")); err != nil {
		t.Fatal(err)
	}
	// ListAppSecretsForAccount — default limit clamp + ordering (slug, key).
	if got, err := m.ListAppSecretsForAccount(ctx, account.ID, 0, ""); err != nil || len(got) != 2 {
		t.Fatalf("secrets account = %d, %v", len(got), err)
	}
	if got, err := m.ListAppSecretsForAccount(ctx, account.ID, 1, ""); err != nil || len(got) != 1 || got[0].Key != "API_KEY" {
		t.Fatalf("secrets limit 1 = %+v, %v", got, err)
	}
	// Cursor "<slug>|<key>" skips rows ≤ the anchor.
	if got, err := m.ListAppSecretsForAccount(ctx, account.ID, 10, app.Slug+"|API_KEY"); err != nil || len(got) != 1 || got[0].Key != "DB_URL" {
		t.Fatalf("secrets cursor = %+v, %v", got, err)
	}
	// Wrong account → empty.
	if got, err := m.ListAppSecretsForAccount(ctx, "missing", 10, ""); err != nil || len(got) != 0 {
		t.Fatalf("secrets missing account = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageRegistryCredentialQuotaCheck(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	if err := m.UpsertAppRegistryCredential(ctx, account.ID, app.ID, "registry.example.com", "robot", []byte("sealed-pass")); err != nil {
		t.Fatal(err)
	}
	// count=1, exists=true for the seeded host; exists=false for a new host.
	if n, exists, err := m.RegistryCredentialQuotaCheck(ctx, account.ID, app.ID, "registry.example.com"); err != nil || n != 1 || !exists {
		t.Fatalf("quota check existing = %d/%v, %v", n, exists, err)
	}
	if n, exists, err := m.RegistryCredentialQuotaCheck(ctx, account.ID, app.ID, "other.example.com"); err != nil || n != 1 || exists {
		t.Fatalf("quota check new host = %d/%v, %v", n, exists, err)
	}
	// Cross-account → count 0.
	if n, _, err := m.RegistryCredentialQuotaCheck(ctx, "missing", app.ID, "registry.example.com"); err != nil || n != 0 {
		t.Fatalf("quota check missing = %d, %v", n, err)
	}
}

func TestMemStoreCoverageAlertRuleLastEvaluated(t *testing.T) {
	m, ctx, account, _, _ := memCoverageFixture(t)
	rule, err := m.CreateAlertRule(ctx, AlertRule{AccountID: account.ID, Name: "cpu-high", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := m.SetAlertRuleLastEvaluated(ctx, rule.ID, at); err != nil {
		t.Fatal(err)
	}
	got, err := m.AlertRuleByID(ctx, rule.ID)
	if err != nil || !got.LastEvaluatedAt.Equal(at) {
		t.Fatalf("last evaluated = %+v, %v", got, err)
	}
	if err := m.SetAlertRuleLastEvaluated(ctx, "missing", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("last evaluated missing = %v", err)
	}
}

func TestMemStoreCoverageOrgs(t *testing.T) {
	m, ctx, account, _, _ := memCoverageFixture(t)
	owner := account.ID
	inviter := account.ID
	member := "member-account"
	org, err := m.CreateOrg(ctx, Org{Slug: "acme-org", Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	// CreateOrg duplicate slug → ErrConflict.
	if _, err := m.CreateOrg(ctx, Org{Slug: "ACME-ORG", Name: "dup"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate org slug = %v", err)
	}
	// Personal org missing owner pointer → ErrConflict.
	if _, err := m.CreateOrg(ctx, Org{Slug: "pers", Personal: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("personal without owner = %v", err)
	}
	// AddOrgMember — owner + member.
	if err := m.AddOrgMember(ctx, org.ID, owner, OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.AddOrgMember(ctx, org.ID, member, OrgRoleDeveloper, &inviter); err != nil {
		t.Fatal(err)
	}
	// Duplicate membership → ErrConflict.
	if err := m.AddOrgMember(ctx, org.ID, member, OrgRoleDeveloper, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate member = %v", err)
	}
	// Second owner → ErrOrgLastOwner.
	if err := m.AddOrgMember(ctx, org.ID, "third-account", OrgRoleOwner, nil); !errors.Is(err, ErrOrgLastOwner) {
		t.Fatalf("second owner = %v", err)
	}
	// Missing org → ErrNotFound.
	if err := m.AddOrgMember(ctx, "missing", member, OrgRoleDeveloper, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member missing org = %v", err)
	}
	// ListOrgMembers — both rows, ordered by JoinedAt.
	if got, err := m.ListOrgMembers(ctx, org.ID); err != nil || len(got) != 2 {
		t.Fatalf("list org members = %+v, %v", got, err)
	}
	// OrgMemberByAccount — hit + miss.
	if got, err := m.OrgMemberByAccount(ctx, org.ID, member); err != nil || got.AccountID != member {
		t.Fatalf("org member by account = %+v, %v", got, err)
	}
	if _, err := m.OrgMemberByAccount(ctx, org.ID, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org member missing = %v", err)
	}
	// ListOrgsForAccount — the owner sees the org via membership.
	if got, err := m.ListOrgsForAccount(ctx, owner); err != nil || len(got) != 1 {
		t.Fatalf("list orgs for account = %+v, %v", got, err)
	}
	// OrgByPersonalAccount — no personal org → ErrNotFound.
	if _, err := m.OrgByPersonalAccount(ctx, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("personal org missing = %v", err)
	}
	// CreateOrgInvitation + ListOrgInvitationsForOrg.
	inv, err := m.CreateOrgInvitation(ctx, OrgInvitation{OrgID: org.ID, Email: "invitee@example.com", Role: OrgRoleDeveloper, TokenHash: []byte("tok-hash"), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateOrgInvitation(ctx, OrgInvitation{OrgID: org.ID, Email: "x@example.com", TokenHash: []byte("tok-hash")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate invitation = %v", err)
	}
	if got, err := m.ListOrgInvitationsForOrg(ctx, org.ID); err != nil || len(got) != 1 || got[0].ID != inv.ID {
		t.Fatalf("list invitations = %+v, %v", got, err)
	}
	if got, err := m.ListOrgInvitationsForOrg(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("list invitations missing = %+v, %v", got, err)
	}
}
