package state_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's rows stay under its creator's account, so fanout spanned the
// creator's personal apps and the company's apps whatever the creator's
// standing in the company. Events now cross that boundary only while the
// creator may still deploy to the company.
//
// adr: 20261025211616
func TestEventFanoutKeepsCompanyBoundary(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		owner, err := store.CreateAccount(ctx, "boundary-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		creator, err := store.CreateAccount(ctx, "boundary-creator-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		org, err := store.CreateOrg(ctx, state.Org{Slug: "boundary-" + uuid.NewString()[:8], Name: "Boundary", Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, &owner.ID); err != nil {
			t.Fatal(err)
		}
		newApp := func(slug, orgID string) (state.App, string) {
			app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: orgID, Slug: slug + "-" + uuid.NewString()[:8],
				Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			sub, _, err := store.UpsertEventSubscription(ctx, creator.ID, app.ID, "*", "*", nil)
			if err != nil {
				t.Fatal(err)
			}
			return app, sub.ID
		}
		company, companySub := newApp("company", org.ID)
		personal, personalSub := newApp("personal", "")
		start := time.Now().UTC().Add(-time.Second)
		published := map[string]string{}
		both, onlyCompany, onlyPersonal := []string{company.ID, personal.ID}, []string{company.ID}, []string{personal.ID}
		slices.Sort(both)

		publish := func(producer string) ([]string, error) {
			envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created",
				Data: json.RawMessage(`{"order_id":"o-1"}`), Producer: producer}).Normalize(creator.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(ctx, "apid", "event.published", &creator.ID, payload); err != nil {
				return nil, err
			}
			published[envelope.ID] = producer
			work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			apps := make([]string, 0, len(work.RecipientSnapshot))
			for _, recipient := range work.RecipientSnapshot {
				apps = append(apps, recipient.AppID)
			}
			slices.Sort(apps)
			return apps, nil
		}
		expect := func(stage, producer string, want []string) {
			t.Helper()
			got, err := publish(producer)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("%s: producer %q reached %v (err %v), want %v", stage, producer, got, err, want)
			}
		}

		// A developer may deploy code that bridges the two, so events cross.
		for _, producer := range []string{eventcontract.AppProducer(company.ID), eventcontract.AppProducer(personal.ID), eventcontract.ProducerPersonal, eventcontract.OrgProducer(org.ID), ""} {
			expect("developer", producer, both)
		}

		if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleViewer); err != nil {
			t.Fatal(err)
		}
		expect("viewer", eventcontract.AppProducer(company.ID), onlyCompany)
		expect("viewer", eventcontract.OrgProducer(org.ID), onlyCompany)
		expect("viewer", eventcontract.AppProducer(personal.ID), onlyPersonal)
		expect("viewer", eventcontract.ProducerPersonal, onlyPersonal)
		// N-1 publishers and platform events carry no producer.
		expect("viewer", "", both)

		if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleDeveloper); err != nil {
			t.Fatal(err)
		}
		if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
			t.Fatal(err)
		}
		// The company's own apps keep talking after their creator leaves.
		expect("removed", eventcontract.AppProducer(company.ID), onlyCompany)
		expect("removed", eventcontract.ProducerPersonal, onlyPersonal)
		expect("removed", eventcontract.AppProducer(personal.ID), onlyPersonal)

		if _, err := publish("app:not-a-uuid"); err == nil {
			t.Fatal("an unrecognized producer was routed")
		}

		// Replays offer an app only the retained events fanout would route
		// to it now, whenever they were published.
		for _, c := range []struct {
			app       state.App
			sub       string
			producers []string
		}{
			{personal, personalSub, []string{eventcontract.AppProducer(personal.ID), eventcontract.ProducerPersonal, ""}},
			{company, companySub, []string{eventcontract.AppProducer(company.ID), eventcontract.OrgProducer(org.ID), ""}},
		} {
			var want []string
			for id, producer := range published {
				if slices.Contains(c.producers, producer) {
					want = append(want, id)
				}
			}
			slices.Sort(want)
			preview, err := store.PreviewEventReplay(ctx, creator.ID, state.EventReplayPreviewQuery{AppID: c.app.ID, SubscriptionID: c.sub,
				EventReplayPreviewOptions: api.EventReplayPreviewOptions{From: start, Until: time.Now().UTC().Add(time.Minute), Limit: 50}})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(preview.Matches))
			for _, match := range preview.Matches {
				got = append(got, match.EventID)
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("replay preview for %s offered %v, want %v", c.app.Slug, got, want)
			}
		}

		// The account routes read each event's producer and recipients' apps.
		for id, producer := range published {
			scope, err := store.EventReceiptScope(ctx, creator.ID, "orders", id)
			if err != nil || scope.Producer != producer || len(scope.RecipientApps) == 0 {
				t.Fatalf("scope of %s = %+v, %v; want producer %q", id, scope, err, producer)
			}
			for sub, app := range scope.RecipientApps {
				if (sub != companySub || app != company.ID) && (sub != personalSub || app != personal.ID) {
					t.Fatalf("scope of %s maps %s to %s", id, sub, app)
				}
			}
		}
	})
}
