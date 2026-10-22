package main_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestAuthServiceAccountsManageThroughTheAdminAPI(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	const org = "3f2b1c0d-0000-4000-8000-000000000001"
	const account = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	const secret = "1b4e28ba-2fa1-41d2-883f-0016d3cca427"
	clientSecret := "gasa_" + strings.Repeat("s", 39) + "WXYZ"
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.URL.Path == "/admin/v1/orgs":
			main.WriteJSONTest(w, map[string]any{"organizations": []map[string]any{{"id": org, "slug": "acme", "name": "Acme"}}, "total": 1, "page": 1, "per_page": 100})
		case r.Method == http.MethodGet:
			main.WriteJSONTest(w, map[string]any{"service_accounts": []map[string]any{
				{"id": account, "org_id": org, "name": "CI deploys", "role": "deployer", "disabled": false, "created_at": "2026-10-21T10:00:00Z", "secrets": []map[string]any{{"id": secret, "hint": "WXYZ"}}},
			}})
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			main.WriteJSONTest(w, map[string]any{"id": account, "org_id": org, "name": "CI deploys", "role": "deployer", "client_id": account, "client_secret": clientSecret, "secret": map[string]any{"id": secret, "hint": "WXYZ", "expires_at": nil}})
		case r.Method == http.MethodPatch:
			main.WriteJSONTest(w, map[string]any{"id": account, "org_id": org, "name": "CI deploys", "role": "viewer", "disabled": true})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	out := captureAuthStdout(t)
	for _, args := range [][]string{
		{"service-accounts", "add", "shop-auth", "--org", "acme", "--name", "CI deploys", "--role", "deployer", "--expires-in", "90"},
		{"service-accounts", "list", "shop-auth", "--org", org},
		{"service-accounts", "update", "shop-auth", account, "--role", "viewer", "--disable"},
		{"service-accounts", "add-secret", "shop-auth", account},
		{"service-accounts", "revoke-secret", "shop-auth", account, secret},
		{"service-accounts", "remove", "shop-auth", account, "--yes"},
	} {
		if code := runAuthAdmin(t, key, args...); code != 0 {
			t.Fatalf("%v = %d\n%s", args, code, out)
		}
	}
	want := []string{
		"GET /admin/v1/orgs?page=1&per_page=100&search=acme",
		`POST /admin/v1/service-accounts {"expires_in_days":90,"name":"CI deploys","org":"` + org + `","role":"deployer"}`,
		"GET /admin/v1/service-accounts?org=" + org,
		`PATCH /admin/v1/service-accounts/` + account + ` {"disabled":true,"role":"viewer"}`,
		`POST /admin/v1/service-accounts/` + account + `/secrets {}`,
		"DELETE /admin/v1/service-accounts/" + account + "/secrets/" + secret,
		"DELETE /admin/v1/service-accounts/" + account,
	}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests\n%v\nwant\n%v", *seen, want)
	}
	for _, line := range []string{"Client ID:     " + account, clientSecret, "valid until revoked", "/auth/v1/oauth/token", "CI deploys  secrets …WXYZ", "viewer, disabled", "Revoked secret " + secret, "Removed service account " + account} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	before := len(*seen)
	for _, args := range [][]string{
		{"service-accounts", "add", "shop-auth", "--name", "x", "--role", "member"},
		{"service-accounts", "add", "shop-auth", "--org", org, "--role", "member"},
		{"service-accounts", "add", "shop-auth", "--org", org, "--name", "x", "--role", "Admins"},
		{"service-accounts", "add", "shop-auth", "--org", org, "--name", "x", "--role", "member", "--expires-in", "0"},
		{"service-accounts", "update", "shop-auth", account},
		{"service-accounts", "update", "shop-auth", account, "--disable", "--enable"},
		{"service-accounts", "update", "shop-auth", "not-an-id", "--disable"},
		{"service-accounts", "list", "shop-auth", "--role", "member"},
		{"service-accounts", "revoke-secret", "shop-auth", account},
		{"service-accounts", "remove", "shop-auth", account},
		{"service-accounts", "rotate", "shop-auth", account},
	} {
		if code := runAuthAdmin(t, key, args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
	if len(*seen) != before {
		t.Fatalf("refused commands sent requests: %v", (*seen)[before:])
	}
}
