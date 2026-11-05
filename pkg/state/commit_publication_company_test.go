package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Commit publication spoke for no one: it carried no producer, so the
// company reach filter let it through to every subscription in the account,
// including a departed creator's personal apps. It now names its app as the
// producer, like an in-guest publish.
//
// adr: 20261025211616
func TestPgCommitPublicationKeepsCompanyBoundary(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	owner, err := store.CreateAccount(ctx, "commit-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAccount(ctx, "commit-creator-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "commit-" + uuid.NewString()[:8], Name: "Commit", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, &owner.ID); err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, orgID string) state.App {
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: orgID, Slug: slug + "-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = store.UpsertEventSubscription(ctx, creator.ID, app.ID, "*", "*", nil); err != nil {
			t.Fatal(err)
		}
		return app
	}
	company, personal := newApp("company", org.ID), newApp("personal", "")
	source, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: creator.ID, AppID: company.ID, Name: "orders", Destination: "event"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}

	receipt, err := store.AcceptCommitEvent(ctx, creator.ID, source.ID, uuid.NewString(), "order.paid", json.RawMessage(`{"order_id":1}`), state.Invocation{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var envelope eventcontract.Envelope
	if err = json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Source != receipt.Publication.Source || envelope.Producer != eventcontract.AppProducer(company.ID) {
		t.Fatalf("publication source %q producer %q, want the company app as producer", envelope.Source, envelope.Producer)
	}
	var companyRecipient bool
	for _, recipient := range work.RecipientSnapshot {
		switch recipient.AppID {
		case personal.ID:
			t.Fatal("a departed creator's personal app received the company app's Commit publication")
		case company.ID:
			companyRecipient = true
		}
	}
	if !companyRecipient {
		t.Fatal("the company app's own subscription missed its Commit publication")
	}
}
