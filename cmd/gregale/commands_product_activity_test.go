package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func productActivityResponse() api.ProductCustomerActivityPage {
	id, tenant, now := uuid.NewString(), uuid.NewString(), time.Now().UTC()
	p := api.ProductCustomerActivityPage{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: tenant, ObservedAt: now, HistoryScope: "retained_product_control_records", Events: []api.ProductCustomerActivityEvent{{ID: "workspace_visit_observed:" + id, Kind: "workspace_visit_observed", SourceID: id, OccurredAt: now}}}
	p.Events[0].ReviewURL = productcontrol.ActivityReviewURL("exports", tenant, p.Events[0])
	return p
}

func TestProductCustomerActivityCLI(t *testing.T) {
	page, calls := productActivityResponse(), 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/exports/product/customers/activity" || r.Header.Get("Authorization") != "Bearer owner" || r.URL.Query().Get("tenant") != page.TenantID || len(r.URL.Query()) != 2 {
			t.Errorf("activity request changed scope or wrote data: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()
	c, err := main.ParseProductCommand([]string{"customers", "activity", "--app", "exports", "--tenant", page.TenantID})
	if err != nil {
		t.Fatal(err)
	}
	value, err := main.RunProductCommand(t.Context(), main.NewClient(srv.URL, "owner"), c)
	if err != nil || calls != 1 {
		t.Fatalf("activity CLI: %+v %v, calls %d", value, err, calls)
	}
	var out bytes.Buffer
	if err = main.WriteProductOutput(&out, value, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{page.TenantID, "First verified workspace visit", "Source:", "Review:", "not a complete audit log"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	out.Reset()
	if err = main.WriteProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var decoded api.ProductCustomerActivityPage
	if err = json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Events[0].ID != page.Events[0].ID {
		t.Fatalf("activity JSON: %s %v", out.String(), err)
	}
	for _, args := range [][]string{{"customers", "activity", "--app", "exports"}, {"customers", "activity", "--app", "exports", "--tenant", "bad"}, {"customers", "activity", "--app", "exports", "--tenant", page.TenantID, "--self"}} {
		if _, err := main.ParseProductCommand(args); err == nil {
			t.Fatalf("invalid activity flags: %v", args)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ProductCustomerActivityPage)
	}{
		{"foreign customer", func(p *api.ProductCustomerActivityPage) { p.TenantID = uuid.NewString() }},
		{"external link", func(p *api.ProductCustomerActivityPage) { p.Events[0].ReviewURL = "https://example.com" }},
		{"different product", func(p *api.ProductCustomerActivityPage) {
			p.Events[0].ReviewURL = strings.Replace(p.Events[0].ReviewURL, "/exports/", "/other/", 1)
		}},
		{"unsupported outcome", func(p *api.ProductCustomerActivityPage) { p.Events[0].Outcome = api.OperationSucceeded }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := page
			copy.Events = append([]api.ProductCustomerActivityEvent(nil), page.Events...)
			tc.change(&copy)
			if err := main.ValidateProductCustomerActivity(copy, c); err == nil {
				t.Fatal("unscoped activity accepted")
			}
		})
	}
}
