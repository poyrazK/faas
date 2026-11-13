package appevents_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

type producerHost struct {
	appevents.Host
	store *state.MemStore
}

func (h producerHost) Store() state.Store { return h.store }

// A Data API change event spoke for no one: it carried no producer, so the
// company reach filter let it through to every subscription in the account,
// including a departed creator's personal apps. It now names its app as the
// producer, like an in-guest publish.
//
// adr: 20261025211616
func TestAppProducerEventKeepsCompanyBoundary(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAccount(ctx, "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "data-api", Name: "Data API", Plan: api.PlanPro})
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
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: orgID, Slug: slug,
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
	if err = store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}

	s := appevents.Service{Host: producerHost{store: store}}
	result := s.PublishAppProducerEvent(ctx, creator, company.ID, appevents.AppProducerEvent{Key: api.DataAPIChangeEventKeyPrefix + "1", Type: "row.inserted", Data: json.RawMessage(`{"id":1}`)})
	if result.Status != "accepted" {
		t.Fatalf("publish %+v", result)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour))
	if err != nil || work == nil {
		t.Fatalf("claim %+v %v", work, err)
	}
	var envelope eventcontract.Envelope
	if err = json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Producer != eventcontract.AppProducer(uuid.MustParse(company.ID).String()) {
		t.Fatalf("producer %q, want the company app", envelope.Producer)
	}
	var companyRecipient bool
	for _, recipient := range work.RecipientSnapshot {
		switch recipient.AppID {
		case personal.ID:
			t.Fatal("a departed creator's personal app received the company app's Data API event")
		case company.ID:
			companyRecipient = true
		}
	}
	if !companyRecipient {
		t.Fatal("the company app's own subscription missed its Data API event")
	}
}
