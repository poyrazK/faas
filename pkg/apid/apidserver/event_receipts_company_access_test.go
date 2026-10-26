package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app keeps its rows under its creator's account, so the account
// event routes showed a creator who left the company its events and its
// recipients. They now apply the company access the app routes apply.
//
// adr: 20261025211616
func TestAccountEventReceiptsFollowCompanyAccess(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := (*e.Acct()).ID
	ctx := t.Context()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "receipt-access", "Receipt Access", api.PlanPro)
	company, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator, OrgID: org.ID, Slug: "company-orders", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator, Slug: "personal-orders", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	subs := map[string]string{}
	for _, app := range []state.App{company, personal} {
		sub, _, err := store.UpsertEventSubscription(ctx, creator, app.ID, "*", "*", nil)
		if err != nil {
			t.Fatal(err)
		}
		subs[app.ID] = sub.ID
	}
	publish := func(id, producer string) {
		t.Helper()
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`), Producer: producer}).Normalize(creator, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(envelope)
		if err := store.AppendEvent(ctx, "apid", "event.published", &creator, payload); err != nil {
			t.Fatal(err)
		}
	}
	// Both events reach both apps while the creator works in the company.
	publish("company-event", eventcontract.AppProducer(company.ID))
	publish("personal-event", eventcontract.ProducerPersonal)
	receiptPath := func(id string) string {
		return "/v1/events/receipt?" + url.Values{"source": {"orders"}, "id": {id}}.Encode()
	}
	recipients := func(id string) map[string]bool {
		t.Helper()
		r := e.Do(t, http.MethodGet, receiptPath(id), nil, nil)
		var receipt api.EventReceiptResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &receipt) != nil {
			t.Fatalf("receipt %s = %d %s", id, r.Code, r.Body)
		}
		apps := map[string]bool{}
		for _, recipient := range receipt.Recipients {
			apps[uuid.MustParse(recipient.AppID).String()] = true
		}
		return apps
	}
	if got := recipients("company-event"); !got[company.ID] || !got[personal.ID] {
		t.Fatalf("member's company receipt recipients = %v", got)
	}

	newOwner, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(ctx, org.ID, creator, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator); err != nil {
		t.Fatal(err)
	}

	if r := e.Do(t, http.MethodGet, receiptPath("company-event"), nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("removed creator's company receipt = %d %s", r.Code, r.Body)
	}
	if got := recipients("personal-event"); got[company.ID] || !got[personal.ID] {
		t.Fatalf("removed creator's personal receipt recipients = %v, want only the personal app", got)
	}
	for _, c := range []struct{ id, app string }{{"company-event", personal.ID}, {"personal-event", company.ID}} {
		query := url.Values{"source": {"orders"}, "id": {c.id}, "subscription_id": {subs[c.app]}}.Encode()
		if r := e.Do(t, http.MethodGet, "/v1/events/receipt/attempts?"+query, nil, nil); r.Code != http.StatusNotFound {
			t.Fatalf("attempts %s/%s = %d %s", c.id, c.app, r.Code, r.Body)
		}
		if r := e.Do(t, http.MethodGet, "/v1/events/receipt/replays?"+query, nil, nil); r.Code != http.StatusNotFound {
			t.Fatalf("replays %s/%s = %d %s", c.id, c.app, r.Code, r.Body)
		}
		r := e.Do(t, http.MethodGet, "/v1/events/recipient-blocker?"+query, nil, nil)
		var blocker api.EventRecipientBlockerResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &blocker) != nil || blocker.Reason != "receipt_unavailable" {
			t.Fatalf("recipient blocker %s/%s = %d %s", c.id, c.app, r.Code, r.Body)
		}
	}
	query := url.Values{"source": {"orders"}, "id": {"personal-event"}, "subscription_id": {subs[personal.ID]}}.Encode()
	if r := e.Do(t, http.MethodGet, "/v1/events/receipt/attempts?"+query, nil, nil); r.Code != http.StatusOK {
		t.Fatalf("attempts on the creator's own recipient = %d %s", r.Code, r.Body)
	}
}
