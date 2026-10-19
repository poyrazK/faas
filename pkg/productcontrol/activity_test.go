package productcontrol

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductActivityCursorAndOrder(t *testing.T) {
	a, app, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := ActivityScope(a, app, tenant)
	e := api.ProductCustomerActivityEvent{Kind: "work_admitted", SourceID: uuid.NewString(), OccurredAt: time.Now().UTC().Truncate(time.Microsecond)}
	raw := EncodeActivityCursor(scope, e)
	c, err := DecodeActivityCursor(raw, scope)
	if err != nil || c.ID != e.SourceID || !c.At.Equal(e.OccurredAt) {
		t.Fatalf("cursor round trip: %+v %v", c, err)
	}
	for _, bad := range []string{raw + "=", "bad", strings.Repeat("a", api.ProductControlOwnerCursorMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"` + scope + `","at":"2026-10-16T00:00:00Z","kind":"work_admitted","id":"` + e.SourceID + `","extra":true}`))} {
		if _, err := DecodeActivityCursor(bad, scope); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	if _, err := DecodeActivityCursor(raw, ActivityScope(a, app, uuid.NewString())); err == nil {
		t.Fatal("cross-customer cursor accepted")
	}
	later := e
	later.Kind = "work_resolved"
	if !ActivityBefore(later, e) || ActivityBefore(e, later) || ActivityBefore(e, e) {
		t.Fatal("simultaneous source milestones do not have a strict order")
	}
}

func TestProductActivityEvidenceValidation(t *testing.T) {
	tenant, id, now := uuid.NewString(), uuid.NewString(), time.Now().UTC()
	p := api.ProductCustomerActivityPage{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: tenant, ObservedAt: now, HistoryScope: "retained_product_control_records", Events: []api.ProductCustomerActivityEvent{{ID: "customer_assignment_changed:" + id, Kind: "customer_assignment_changed", SourceID: id, OccurredAt: now, Assignment: &api.ProductActivityAssignment{After: api.ProductCustomer{TenantID: tenant, Plan: "pro", Revision: strings.Repeat("a", 64)}}}}}
	if err := ValidateActivityPage(p, ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ProductCustomerActivityPage)
	}{
		{"cross customer", func(p *api.ProductCustomerActivityPage) { p.TenantID = uuid.NewString() }},
		{"invented outcome", func(p *api.ProductCustomerActivityPage) { p.Events[0].Outcome = api.OperationSucceeded }},
		{"invented actor", func(p *api.ProductCustomerActivityPage) { p.Events[0].Assignment.ActorRole = "owner" }},
		{"wrong source", func(p *api.ProductCustomerActivityPage) { p.Events[0].SourceID = uuid.NewString() }},
		{"unexpected snapshot", func(p *api.ProductCustomerActivityPage) {
			p.Events[0].Document = &api.ProductBillingDocument{Snapshot: &api.ProductBillingReview{}}
		}},
		{"duplicate", func(p *api.ProductCustomerActivityPage) { p.Events = append(p.Events, p.Events[0]) }},
		{"inactivity inferred", func(p *api.ProductCustomerActivityPage) { p.HistoryScope = "complete_customer_history" }},
		{"bad continuation", func(p *api.ProductCustomerActivityPage) { p.NextCursor = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := p
			copy.Events = append([]api.ProductCustomerActivityEvent(nil), p.Events...)
			a := *copy.Events[0].Assignment
			copy.Events[0].Assignment = &a
			tc.change(&copy)
			if err := ValidateActivityPage(copy, ""); err == nil {
				t.Fatal("unsupported evidence claim accepted")
			}
		})
	}
}
