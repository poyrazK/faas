package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// Billing review R10-3: an organization gets its billing email at its first
// checkout, so a Free organization has none. Its quota mail goes to its
// owner instead of nowhere.
func TestQuotaMailForAFreeOrganizationGoesToItsOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := state.NewMemStore()
	owner := makeAccount(t, ctx, s, api.PlanFree)
	org, err := s.CreateOrg(ctx, state.Org{Slug: "free-co", Name: "Free Co", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	subject, err := state.BillingResourceAccount(ctx, s, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if subject.Email != "" || subject.ResourceOrgID == "" {
		t.Fatalf("fixture subject %+v: want an organization without a billing email", subject)
	}
	mailer := &fakeMailer{}
	params := meter.QuotaParams{Store: s, Notif: &fakeNotifier{}, Parker: &fakeParker{}, Mailer: mailer, Log: discardLog()}
	used := float64(api.PlanFree.PlanIncludedGBHours()) * 0.85
	if _, err := params.Enforce(ctx, subject, used, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if mailer.count() != 1 || len(mailer.sent[0].To) != 1 || mailer.sent[0].To[0] != owner.Email {
		t.Fatalf("mails = %+v, want one approaching mail to the owner %s", mailer.sent, owner.Email)
	}
}
