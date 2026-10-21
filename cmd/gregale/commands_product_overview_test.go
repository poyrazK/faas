package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func productOverviewResponse() api.ProductCustomerOverviewPage {
	app, tenant, revision := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	now := time.Now().UTC()
	return api.ProductCustomerOverviewPage{AppID: app, ObservedAt: now, ObservationScope: "responding_api_node", QualificationState: "unverified", Period: now.Format("2006-01"), Customers: []api.ProductCustomerOverview{{Name: "Alice", TenantStatus: "active", Customer: api.ProductCustomer{TenantID: tenant, Plan: "starter", Revision: revision}, Readiness: api.ProductDoctorReport{AppID: app, TenantID: tenant, Revision: revision, ObservedAt: now, ObservationScope: "responding_api_node", ConfigurationState: "observed", QualificationState: "unverified", Checks: []api.ProductDoctorCheck{{Check: "binding", Status: "observed", Impact: "submission"}}}, Activation: api.ProductCustomerActivation{State: "not_recorded"}, NextActions: []api.ProductCustomerNextAction{{Code: "review_activation", Label: "Review onboarding and activation", URL: "/dashboard/apps/exports/product-control/onboarding?tenant=" + tenant}}}}}
}

func TestProductCustomerOverviewCLI(t *testing.T) {
	page := productOverviewResponse()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/exports/product/customers/overview" || r.Header.Get("Authorization") != "Bearer owner" || r.URL.Query().Get("cursor") != "" {
			t.Errorf("overview request changed scope or wrote data: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()
	c, err := parseProductCommand([]string{"customers", "overview", "--app", "exports"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runProductCommand(t.Context(), NewClient(srv.URL, "owner"), c)
	if err != nil || calls != 1 {
		t.Fatalf("overview command: %+v %v, calls %d", value, err, calls)
	}
	var out bytes.Buffer
	if err = writeProductOutput(&out, value, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Alice", "configuration observed", "No guided activation record", "native qualification: unverified", "all retained months", "Review onboarding"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	out.Reset()
	if err = writeProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var decoded api.ProductCustomerOverviewPage
	if err = json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Customers[0].Customer.TenantID != page.Customers[0].Customer.TenantID {
		t.Fatalf("JSON output changed scope: %s %v", out.String(), err)
	}
	for _, args := range [][]string{{"customers", "overview"}, {"customers", "overview", "--app", "exports", "--cursor", "bad"}, {"customers", "overview", "--app", "exports", "--tenant", uuid.NewString()}} {
		if _, err = parseProductCommand(args); err == nil {
			t.Fatalf("invalid overview args accepted: %q", args)
		}
	}
}

func TestProductCustomerOverviewRejectsFalseClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.ProductCustomerOverviewPage)
	}{
		{"missing customer page", func(p *api.ProductCustomerOverviewPage) { p.Customers = nil }},
		{"native qualification", func(p *api.ProductCustomerOverviewPage) { p.QualificationState = "qualified" }},
		{"wrong customer", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Readiness.TenantID = uuid.NewString() }},
		{"wrong app", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Readiness.AppID = uuid.NewString() }},
		{"wrong revision", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Readiness.Revision = strings.Repeat("b", 64) }},
		{"false readiness", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Readiness.ConfigurationState = "blocked" }},
		{"invented activation", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Activation.State = "first_success_observed" }},
		{"missing setup", func(p *api.ProductCustomerOverviewPage) {
			p.Customers[0].Activation.FirstSuccess = &api.ProductOnboardingSuccess{OperationID: uuid.NewString(), Feature: "export", VerifiedAt: time.Now()}
		}},
		{"invalid pending count", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].Work.Unresolved = -1 }},
		{"review exceeds pending", func(p *api.ProductCustomerOverviewPage) {
			p.Customers[0].Work.NeedsReview = 1
			p.Customers[0].Work.ReviewPeriod = "2026-09"
		}},
		{"review month absent", func(p *api.ProductCustomerOverviewPage) {
			p.Customers[0].Work.Unresolved = 1
			p.Customers[0].Work.NeedsReview = 1
		}},
		{"external action", func(p *api.ProductCustomerOverviewPage) { p.Customers[0].NextActions[0].URL = "https://example.com" }},
		{"foreign action", func(p *api.ProductCustomerOverviewPage) {
			p.Customers[0].NextActions[0].URL = "/dashboard/apps/other/product-control?tenant=" + p.Customers[0].Customer.TenantID
		}},
		{"wrong cursor", func(p *api.ProductCustomerOverviewPage) { p.NextCursor = uuid.NewString() }},
		{"duplicate customer", func(p *api.ProductCustomerOverviewPage) { p.Customers = append(p.Customers, p.Customers[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := productOverviewResponse()
			tc.change(&page)
			if err := validateProductCustomerOverview(page, productCommand{app: "exports"}); err == nil {
				t.Fatal("unscoped or unsupported claim accepted")
			}
		})
	}
}
