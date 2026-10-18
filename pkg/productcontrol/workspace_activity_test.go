package productcontrol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkspaceActivityProjection(t *testing.T) {
	permissions := api.ProductPermissions{ViewWork: true, RunWork: true, ViewBilling: true, ManagePlan: true}
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, kind := range []string{"work_succeeded", "work_failed", "work_cancelled", "work_attention", "plan_changed", "statement_finalized", "credit_recorded", "help_replied", "help_resolved"} {
		t.Run(kind, func(t *testing.T) {
			id := uuid.NewString()
			e := api.ProductWorkspaceActivityEvent{ID: kind + "/" + id, Kind: kind, ReferenceID: id, OccurredAt: at}
			switch WorkspaceActivityCategory(kind) {
			case "work":
				e.Feature = "export"
			case "plans":
				e.Plan = &api.ProductWorkspaceActivityPlan{Before: "starter", After: "pro"}
			case "billing":
				e.Billing = &api.ProductWorkspaceActivityBill{Period: "2026-09", Currency: "EUR", AmountMillicents: 25000}
				if kind == "credit_recorded" {
					e.Billing.AmountMillicents = -25000
				}
			case "help":
				e.HelpKind = "run"
			}
			if !WorkspaceActivityVisible(e, permissions) || WorkspaceActivityVisible(e, api.ProductPermissions{}) {
				t.Fatal("projection did not enforce permission")
			}
			if e.Billing != nil {
				bad := e
				bill := *e.Billing
				bill.AmountMillicents = -bill.AmountMillicents
				bad.Billing = &bill
				if WorkspaceActivityVisible(bad, permissions) {
					t.Fatal("billing sign changed")
				}
			}
			for _, what := range []string{"private billing on work", "private plan on help", "unknown kind", "wrong reference", "missing time"} {
				bad := e
				switch what {
				case "private billing on work":
					if e.Billing != nil {
						continue
					}
					bad.Billing = &api.ProductWorkspaceActivityBill{Period: "2026-09", Currency: "EUR", AmountMillicents: 1}
				case "private plan on help":
					if e.Plan != nil {
						continue
					}
					bad.Plan = &api.ProductWorkspaceActivityPlan{Before: "starter", After: "pro"}
				case "unknown kind":
					bad.Kind = "invoice_handoff_recorded"
				case "wrong reference":
					bad.ReferenceID = uuid.NewString()
				case "missing time":
					bad.OccurredAt = time.Time{}
				}
				if WorkspaceActivityVisible(bad, permissions) {
					t.Fatal(what)
				}
			}
		})
	}
}

func TestWorkspaceActivityContinuation(t *testing.T) {
	p := api.ProductPermissions{ViewWork: true}
	account, app, tenant, auth, org := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := WorkspaceActivityScope(account, app, tenant, auth, org, "viewer", "all", p)
	e := api.ProductWorkspaceActivityEvent{Kind: "work_succeeded", ReferenceID: uuid.NewString(), OccurredAt: time.Now().UTC().Truncate(time.Microsecond), Feature: "export"}
	e.ID = e.Kind + "/" + e.ReferenceID
	raw := EncodeWorkspaceActivityCursor(scope, e)
	if _, err := DecodeWorkspaceActivityCursor(raw, scope); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		WorkspaceActivityScope(account, app, uuid.NewString(), auth, org, "viewer", "all", p),
		WorkspaceActivityScope(account, app, tenant, uuid.NewString(), org, "viewer", "all", p),
		WorkspaceActivityScope(account, app, tenant, auth, uuid.NewString(), "viewer", "all", p),
		WorkspaceActivityScope(account, app, tenant, auth, org, "billing", "all", p),
		WorkspaceActivityScope(account, app, tenant, auth, org, "viewer", "work", p),
		WorkspaceActivityScope(account, app, tenant, auth, org, "viewer", "all", api.ProductPermissions{ViewBilling: true}),
	} {
		if _, err := DecodeWorkspaceActivityCursor(raw, changed); err == nil {
			t.Fatal("cursor crossed current view")
		}
	}
	for _, invalid := range []string{"!", raw + "=", strings.Repeat("a", api.ProductControlOwnerCursorMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"` + scope + `","extra":1}`))} {
		if _, err := DecodeWorkspaceActivityCursor(invalid, scope); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	page := api.ProductWorkspaceActivityPage{AccountID: account, AppID: app, TenantID: tenant, Filter: "all", Events: []api.ProductWorkspaceActivityEvent{e}, ObservedAt: time.Now().UTC(), HistoryScope: "retained_product_control_records"}
	if err := ValidateWorkspaceActivityPage(page, p, scope, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceActivityPage(page, p, scope, raw); err == nil {
		t.Fatal("repeated boundary accepted")
	}
	page.Events = append(page.Events, e)
	if err := ValidateWorkspaceActivityPage(page, p, scope, ""); err == nil {
		t.Fatal("duplicate event accepted")
	}
	page.Events = page.Events[:1]
	page.NextCursor = raw
	if err := ValidateWorkspaceActivityPage(page, p, scope, ""); err == nil {
		t.Fatal("short page continuation accepted")
	}
	page.NextCursor = ""
	page.Filter = "billing"
	if err := ValidateWorkspaceActivityPage(page, p, scope, ""); err == nil {
		t.Fatal("event crossed filter")
	}
	// Ensure JSON timestamps preserve the microsecond boundary used by SQL.
	b, _ := json.Marshal(e)
	if !strings.Contains(string(b), e.OccurredAt.Format(time.RFC3339Nano)) {
		t.Fatal("timestamp changed")
	}
}
