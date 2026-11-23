package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func setAppNameBlocklist(t *testing.T, e apidserver.TestEnv, list string) {
	t.Helper()
	raw, _ := json.Marshal(list)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigAppNameBlocklist, raw); err != nil {
		t.Fatalf("apply app_name_blocklist: %v", err)
	}
}

// A blocked name gets a 409 with its own code, so a CLI deploy falls back to
// a suffixed name; an unblocked name is created as before, and a malformed
// name that a pattern matches is still refused as invalid.
func TestCreateAppRefusesBlocklistedName(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	setAppNameBlocklist(t, e, "paypal, *-login")
	for _, slug := range []string{"paypal", "bank-login"} {
		rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
		apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeAppNameUnavailable)
	}
	invalid := strings.Repeat("x", 60) + "-login"
	rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: invalid}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "paypal-shop-x7k2q"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("unblocked name: status %d: %s", rec.Code, rec.Body)
	}
}

// The blocklist binds only new names: an app that already has the name keeps
// working, renaming it to its own name stays a no-op, and renaming another
// app to a blocked name is refused.
func TestBlocklistKeepsExistingAppsAndRefusesRename(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "secure-pay")
	apidserver.MustSeedApp(t, e, "my-shop")
	setAppNameBlocklist(t, e, "secure-*")
	if rec := e.Do(t, "GET", "/v1/apps/secure-pay", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("existing app: status %d: %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "POST", "/v1/apps/secure-pay/rename", api.RenameAppRequest{NewSlug: "secure-pay"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("same-name rename: status %d: %s", rec.Code, rec.Body)
	}
	rec := e.Do(t, "POST", "/v1/apps/my-shop/rename", api.RenameAppRequest{NewSlug: "secure-login"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeAppNameUnavailable)
}

func TestAppNameBlocklistRejectsBadEntries(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	raw, _ := json.Marshal("pay_pal")
	if err := apidserver.ValidateAppNameBlocklist(raw); err == nil {
		t.Fatal("pay_pal accepted")
	}
	if got := (*(*e.S()).RuntimeConfig()).AppNameBlocklist(); len(got) != 0 {
		t.Fatalf("default blocklist = %v, want empty", got)
	}
}
