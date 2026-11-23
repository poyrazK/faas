package apidserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func setAppNameBlocklist(t *testing.T, e testEnv, list string) {
	t.Helper()
	raw, _ := json.Marshal(list)
	if err := e.s.runtimeConfig.apply(runtimeConfigAppNameBlocklist, raw); err != nil {
		t.Fatalf("apply app_name_blocklist: %v", err)
	}
}

// A blocked name is answered like a taken one (409), so a CLI deploy falls
// back to a suffixed name; an unblocked name is created as before.
func TestCreateAppRefusesBlocklistedNameAsTaken(t *testing.T) {
	e := setup(t, api.PlanPro)
	setAppNameBlocklist(t, e, "paypal, *-login")
	for _, slug := range []string{"paypal", "bank-login"} {
		rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
		assertProblem(t, rec, http.StatusConflict, api.CodeValidation)
	}
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "paypal-shop-x7k2q"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("unblocked name: status %d: %s", rec.Code, rec.Body)
	}
}

// The blocklist binds only new names: an app that already has the name keeps
// working, and renaming to a blocked name is refused.
func TestBlocklistKeepsExistingAppsAndRefusesRename(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "secure-pay")
	mustSeedApp(t, e, "my-shop")
	setAppNameBlocklist(t, e, "secure-*")
	if rec := e.do(t, "GET", "/v1/apps/secure-pay", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("existing app: status %d: %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "POST", "/v1/apps/my-shop/rename", api.RenameAppRequest{NewSlug: "secure-login"}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeValidation)
}

func TestAppNameBlocklistRejectsBadEntries(t *testing.T) {
	e := setup(t, api.PlanPro)
	raw, _ := json.Marshal("pay_pal")
	if err := validateAppNameBlocklist(raw); err == nil {
		t.Fatal("pay_pal accepted")
	}
	if got := e.s.runtimeConfig.appNameBlocklist(); len(got) != 0 {
		t.Fatalf("default blocklist = %v, want empty", got)
	}
}

func TestAppNameReportListsMatchesWithoutChangingThem(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "paypal")
	mustSeedApp(t, e, "bank-login")
	mustSeedApp(t, e, "my-shop")
	report := func(query string) api.AppNameReportResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		e.s.appNameReport(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/app-names/report"+query, nil), e.acct)
		if rec.Code != http.StatusOK {
			t.Fatalf("report%s: status %d: %s", query, rec.Code, rec.Body)
		}
		var out api.AppNameReportResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := report(""); got.Source != "live" || got.Entries != 0 || len(got.Matches) != 0 {
		t.Fatalf("empty live list: %+v", got)
	}
	got := report("?blocklist=paypal,*-login")
	if got.Source != "preview" || got.Entries != 2 || len(got.Matches) != 2 ||
		got.Matches[0].Slug != "bank-login" || got.Matches[0].Entry != "*-login" || got.Matches[1].Slug != "paypal" {
		t.Fatalf("preview: %+v", got)
	}
	setAppNameBlocklist(t, e, "my-*")
	if got := report(""); len(got.Matches) != 1 || got.Matches[0].Slug != "my-shop" {
		t.Fatalf("live: %+v", got)
	}
	// Report only: the matched apps still exist.
	if rec := e.do(t, "GET", "/v1/apps/my-shop", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("my-shop after report: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	e.s.appNameReport(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/app-names/report?blocklist=pay_pal", nil), e.acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad preview: status %d", rec.Code)
	}
}
