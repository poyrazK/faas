package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountOnlyCommands is apid's suspension and non-payment deletion command
// surface as it behaves for an organization: it locks account rows only.
type accountOnlyCommands struct{ calls int }

func (c *accountOnlyCommands) Suspend(context.Context, string, time.Time) (api.InternalMailSuspensionResponse, error) {
	c.calls++
	return api.InternalMailSuspensionResponse{}, errors.New("account unavailable")
}

func (c *accountOnlyCommands) Schedule(context.Context, string, time.Time) (api.InternalMailNonpaymentDeletionResponse, error) {
	c.calls++
	return api.InternalMailNonpaymentDeletionResponse{}, errors.New("account unavailable")
}

func (c *accountOnlyCommands) Complete(context.Context, string, string, string) error {
	return nil
}

// Hunt 17 H17-3: with the durable suspension and non-payment deletion mail
// enabled, an organization's dunning went to apid commands that lock account
// rows only, so an unpaid organization never left past_due. Organizations take
// the direct transition.
func TestDunningOrganizationSkipsAccountOnlyCommands(t *testing.T) {
	ctx := t.Context()
	_, store, parker, notif, mailer, clock := dunningTestFixture(t, ctx)
	// MemStore turns resource ownership on for a Free organization.
	org, err := store.CreateOrg(ctx, state.Org{Slug: "unpaid-co", Name: "Unpaid Co", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if !org.ResourceOwnershipEnabled {
		t.Fatal("fixture organization must be its own billing subject")
	}
	if err := store.MarkDunningStep(ctx, org.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	commands := &accountOnlyCommands{}
	on := func() bool { return true }
	d := meter.NewDunning(meter.DunningParams{Store: store, Parker: parker, Notif: notif, Mailer: mailer, Log: discardLog(),
		Now:                 func() time.Time { return *clock },
		SuspensionRequested: on, SuspensionCommands: commands,
		NonpaymentDeletionRequested: on, NonpaymentDeletionCommands: commands})
	status := func() state.OrgStatus {
		t.Helper()
		got, err := store.OrgByID(ctx, org.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}

	*clock = time.Now().Add(api.InternalMailDunningSuspendAfter + time.Hour)
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != state.OrgStatusSuspended {
		t.Fatalf("organization status after the suspension window = %s, want suspended", got)
	}
	*clock = time.Now().Add(api.InternalMailDunningDeleteAfter + time.Hour)
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != state.OrgStatusDeletedPending {
		t.Fatalf("organization status after the deletion window = %s, want deleted_pending", got)
	}
	if commands.calls != 0 {
		t.Fatalf("organization dunning called the account-only commands %d time(s)", commands.calls)
	}
}
