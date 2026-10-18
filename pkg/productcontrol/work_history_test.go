package productcontrol

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductControlWorkHistoryFilters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		f     api.ProductWorkHistoryFilter
		valid bool
	}{
		{"default", api.ProductWorkHistoryFilter{}, true},
		{"historical feature", api.ProductWorkHistoryFilter{Feature: "removed-feature", State: "unavailable", From: "2024-02-29", Through: "2024-02-29"}, true},
		{"reference", api.ProductWorkHistoryFilter{OperationID: uuid.NewString()}, true},
		{"state", api.ProductWorkHistoryFilter{State: "expired"}, false},
		{"reference prefix", api.ProductWorkHistoryFilter{OperationID: "1234"}, false},
		{"nil reference", api.ProductWorkHistoryFilter{OperationID: uuid.Nil.String()}, false},
		{"feature", api.ProductWorkHistoryFilter{Feature: "../other"}, false},
		{"impossible date", api.ProductWorkHistoryFilter{From: "2026-02-29"}, false},
		{"date with time", api.ProductWorkHistoryFilter{From: "2026-10-01T00:00:00Z"}, false},
		{"year zero", api.ProductWorkHistoryFilter{From: "0000-01-01"}, false},
		{"reversed", api.ProductWorkHistoryFilter{From: "2026-10-02", Through: "2026-10-01"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NormalizeWorkHistoryFilter(tc.f)
			if (err == nil) != tc.valid {
				t.Fatal(f, err)
			}
			if err == nil && f.State == "" {
				t.Fatal("state not normalized")
			}
		})
	}
}

func TestProductControlWorkHistoryCursorAndPage(t *testing.T) {
	p := api.ProductPermissions{ViewWork: true}
	f := api.ProductWorkHistoryFilter{State: "all"}
	account, app, tenant, auth, org := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := WorkHistoryScope(account, app, tenant, auth, org, "viewer", p, f)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	e := api.ProductWorkHistoryEntry{OperationID: uuid.NewString(), Feature: "old-feature", Plan: "old-plan", Period: at.Format("2006-01"), AdmittedAt: at, Outcome: api.ProductRunOutcome{State: "unavailable"}}
	raw := EncodeWorkHistoryCursor(scope, e)
	if c, err := DecodeWorkHistoryCursor(raw, scope); err != nil || c.ID != e.OperationID {
		t.Fatal(c, err)
	}
	for _, bad := range []string{"!", strings.Repeat("a", api.ProductControlWorkHistoryCursorMaxBytes+1), raw + "=", base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"` + scope + `","at":"` + at.Format(time.RFC3339Nano) + `","id":"` + e.OperationID + `","owner":true}`))} {
		if _, err := DecodeWorkHistoryCursor(bad, scope); err == nil {
			t.Fatal("invalid cursor accepted", bad)
		}
	}
	for _, changed := range []string{WorkHistoryScope(account, app, uuid.NewString(), auth, org, "viewer", p, f), WorkHistoryScope(account, app, tenant, uuid.NewString(), org, "viewer", p, f), WorkHistoryScope(account, app, tenant, auth, uuid.NewString(), "viewer", p, f), WorkHistoryScope(account, app, tenant, auth, org, "other", p, f), WorkHistoryScope(account, app, tenant, auth, org, "viewer", api.ProductPermissions{ViewWork: true, ViewBilling: true}, f), WorkHistoryScope(account, app, tenant, auth, org, "viewer", p, api.ProductWorkHistoryFilter{State: "failed"})} {
		if _, err := DecodeWorkHistoryCursor(raw, changed); err == nil {
			t.Fatal("cursor reused in changed view")
		}
	}
	page := api.ProductWorkHistoryPage{AccountID: account, AppID: app, TenantID: tenant, Filter: f, Entries: []api.ProductWorkHistoryEntry{e}, ObservedAt: time.Now().UTC(), HistoryScope: "retained_product_control_receipts"}
	if err := ValidateWorkHistoryPage(page, p, scope, ""); err != nil {
		t.Fatal(err)
	}
	for _, what := range []string{"identity", "permission", "duplicate", "future", "invented outcome", "result", "filter", "cursor", "null entries"} {
		t.Run(what, func(t *testing.T) {
			b := page
			b.Entries = append([]api.ProductWorkHistoryEntry{}, page.Entries...)
			permissions := p
			switch what {
			case "identity":
				b.AccountID = "bad"
			case "permission":
				permissions.ViewWork = false
			case "duplicate":
				b.Entries = append(b.Entries, e)
			case "future":
				b.Entries[0].AdmittedAt = page.ObservedAt.Add(time.Hour)
			case "invented outcome":
				b.Entries[0].Outcome.State = "failed"
			case "result":
				b.Entries[0].Outcome.ResultAvailable = true
			case "filter":
				b.Filter.State = "running"
			case "cursor":
				b.NextCursor = raw
			case "null entries":
				b.Entries = nil
			}
			if ValidateWorkHistoryPage(b, permissions, scope, "") == nil {
				t.Fatal("invalid page accepted")
			}
		})
	}
	// Sub-millisecond order must survive browser and PostgreSQL precision.
	newer := e
	newer.AdmittedAt = at.Add(time.Microsecond)
	if !WorkHistoryBefore(newer, e) || WorkHistoryBefore(e, newer) {
		t.Fatal("microsecond order lost")
	}
	if ValidateWorkHistoryPage(page, p, scope, raw) == nil {
		t.Fatal("cursor boundary repeated")
	}
}
