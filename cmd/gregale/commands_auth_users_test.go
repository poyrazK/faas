package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAuthBanForAndUserReferences(t *testing.T) {
	for value, want := range map[string]string{"24h": "24h", "7d": "168h", "forever": authBanForever, "999999h": "999999h"} {
		if got, err := authBanFor(value); err != nil || got != want {
			t.Errorf("authBanFor(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"", "0h", "1w", "-1d", "24", "1000000h", "41667d", "24h "} {
		if got, err := authBanFor(value); err == nil {
			t.Errorf("authBanFor(%q) = %q, want an error", value, got)
		}
	}
	for _, ref := range []string{"0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70", "ada@example.com"} {
		if err := authUserRefValid(ref); err != nil {
			t.Errorf("%q refused: %v", ref, err)
		}
	}
	for _, ref := range []string{"", "ada", "0b5f2f0e", "ada @example.com"} {
		if authUserRefValid(ref) == nil {
			t.Errorf("%q accepted", ref)
		}
	}
}

// fakeAuthAdmin serves /admin/v1 for shop-auth.gregale.app with one key and
// records each request as "METHOD path?query body".
func fakeAuthAdmin(t *testing.T, key string, handle func(w http.ResponseWriter, r *http.Request, body map[string]any)) *[]string {
	t.Helper()
	var seen []string
	old := authAdminHTTPClient
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.Host != "shop-auth.gregale.app" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("request to %s with the wrong key", r.URL.Host)
			recorder.WriteHeader(http.StatusUnauthorized)
			return recorder.Result(), nil
		}
		var body map[string]any
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		line := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			line += "?" + r.URL.RawQuery
		}
		if len(raw) > 0 {
			line += " " + string(raw)
		}
		seen = append(seen, line)
		handle(recorder, r, body)
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient = old })
	return &seen
}

// runAuthAdmin runs one `gregale auth` command with the key on stdin.
func runAuthAdmin(t *testing.T, key string, args ...string) int {
	t.Helper()
	old := osStdin
	osStdin = strings.NewReader(key + "\n")
	t.Cleanup(func() { osStdin = old })
	return cmdAuth(append(args, "--admin-key-stdin"))
}

func TestAuthUsersFindsByEmailAndChangesTheUser(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	const ada = "0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70"
	deletes := 0
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/admin/v1/users":
			// GoTrue matches part of the address: nada@ comes back too.
			writeJSONTest(w, map[string]any{"users": []map[string]any{
				{"id": "6a1b2c3d-0000-4000-8000-000000000001", "email": "nada@example.com", "providers": []string{"email"}},
				{"id": ada, "email": "ada@example.com", "providers": []string{"email"}},
			}, "total": 2, "page": 1, "per_page": 100})
		case r.Method == "GET" && r.URL.Path == "/admin/v1/users/"+ada:
			writeJSONTest(w, map[string]any{"user": map[string]any{"id": ada, "email": "ada@example.com", "email_confirmed_at": "2026-10-01T00:00:00Z", "providers": []string{"email", "google"}},
				"organizations": []map[string]any{{"id": "o1", "slug": "acme", "name": "Acme", "role": "owner"}}, "active_sessions": 2})
		case r.Method == "POST" && r.URL.Path == "/admin/v1/users/"+ada+"/sign-out":
			writeJSONTest(w, map[string]any{"sessions_ended": 2})
		case r.Method == "POST" && r.URL.Path == "/admin/v1/users/"+ada+"/ban":
			until := "2026-10-25T10:00:00Z"
			if body["duration"] == "none" {
				until = ""
			}
			writeJSONTest(w, map[string]any{"user": map[string]any{"id": ada, "email": "ada@example.com", "banned_until": until}})
		case r.Method == "DELETE" && r.URL.Path == "/admin/v1/users/"+ada:
			if deletes++; deletes == 1 {
				writeJSONTestStatus(w, http.StatusConflict, map[string]any{"status": 409, "code": "last_owner", "organizations": []string{"acme"}})
				return
			}
			writeJSONTest(w, map[string]any{"deleted": true, "organizations_deleted": 1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out := captureAuthStdout(t)
	for _, step := range [][]string{
		{"users", "get", "shop-auth", "ada@example.com"},
		{"users", "sign-out", "shop-auth", ada},
		{"users", "ban", "shop-auth", ada, "--for", "7d"},
		{"users", "unban", "shop-auth", ada},
	} {
		if code := runAuthAdmin(t, key, step...); code != 0 {
			t.Fatalf("%v = %d\n%s", step, code, out)
		}
	}
	if code := runAuthAdmin(t, key, "users", "delete", "shop-auth", ada, "--yes"); code == 0 {
		t.Fatal("deleted the last owner of acme")
	}
	if code := runAuthAdmin(t, key, "users", "delete", "shop-auth", ada, "--yes"); code != 0 {
		t.Fatalf("delete = %d", code)
	}
	want := []string{
		"GET /admin/v1/users?page=1&per_page=100&search=ada%40example.com",
		"GET /admin/v1/users/" + ada,
		"POST /admin/v1/users/" + ada + "/sign-out {}",
		"POST /admin/v1/users/" + ada + `/ban {"duration":"168h"}`,
		"POST /admin/v1/users/" + ada + `/ban {"duration":"none"}`,
		"DELETE /admin/v1/users/" + ada,
		"DELETE /admin/v1/users/" + ada,
	}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(*seen, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range []string{"ada@example.com (confirmed)", "email, google", "2 active", "acme (owner)", "2 sessions ended", "Banned " + ada + " (ada@example.com) until 2026-10-25T10:00:00Z", "Lifted the ban", "Deleted user", "1 organizations they alone belonged to"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
}

func TestAuthUsersRefusesBeforeAnyRequest(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) { w.WriteHeader(http.StatusTeapot) })
	captureAuthStdout(t)
	for name, args := range map[string][]string{
		"delete with the key on stdin and no --yes": {"users", "delete", "shop-auth", "ada@example.com"},
		"ban without --for":                         {"users", "ban", "shop-auth", "ada@example.com"},
		"ban for a week":                            {"users", "ban", "shop-auth", "ada@example.com", "--for", "1w"},
		"a flag of another subcommand":              {"users", "get", "shop-auth", "ada@example.com", "--for", "7d"},
		"a name that is neither ID nor email":       {"users", "get", "shop-auth", "ada"},
		"a list page size over the limit":           {"users", "list", "shop-auth", "--limit", "101"},
		"an unknown subcommand":                     {"users", "promote", "shop-auth", "ada@example.com"},
		"an organization that is not a slug":        {"orgs", "get", "shop-auth", "Acme Inc"},
		"search on orgs get":                        {"orgs", "get", "shop-auth", "acme", "--search", "a"},
	} {
		if code := runAuthAdmin(t, key, args...); code == 0 {
			t.Errorf("%s accepted", name)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("requests sent: %v", *seen)
	}
}

func TestAuthUsersListAndOrgs(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	const acme = "7c1b2c3d-0000-4000-8000-000000000009"
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch r.URL.Path {
		case "/admin/v1/users":
			writeJSONTest(w, map[string]any{"users": []map[string]any{
				{"id": "6a1b2c3d-0000-4000-8000-000000000001", "email": "ada@example.com", "email_confirmed_at": "2026-10-01T00:00:00Z", "last_sign_in_at": "2026-10-17T09:00:00Z"},
				{"id": "6a1b2c3d-0000-4000-8000-000000000002", "email": "bob@example.com"},
				{"id": "6a1b2c3d-0000-4000-8000-000000000003", "anonymous": true},
				{"id": "6a1b2c3d-0000-4000-8000-000000000004", "phone": "+15555550100", "banned_until": "2999-01-01T00:00:00Z"},
			}, "total": 120, "page": 1, "per_page": 50})
		case "/admin/v1/orgs":
			writeJSONTest(w, map[string]any{"organizations": []map[string]any{
				{"id": "7c1b2c3d-0000-4000-8000-000000000008", "slug": "acme-labs", "name": "Acme Labs", "members": 3},
				{"id": acme, "slug": "acme", "name": "Acme", "members": 2},
			}, "total": 2, "page": 1, "per_page": 100})
		case "/admin/v1/orgs/" + acme:
			writeJSONTest(w, map[string]any{"organization": map[string]any{"id": acme, "slug": "acme", "name": "Acme", "created_at": "2026-10-01T00:00:00Z"},
				"members":     []map[string]any{{"user_id": "6a1b2c3d-0000-4000-8000-000000000001", "email": "ada@example.com", "role": "owner"}},
				"invitations": []map[string]any{{"id": "i1", "email": "eve@example.com", "role": "member", "expires_at": "2026-10-24T00:00:00Z"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out := captureAuthStdout(t)
	if code := runAuthAdmin(t, key, "users", "list", "shop-auth", "--search", "example.com"); code != 0 {
		t.Fatalf("users list = %d", code)
	}
	if code := runAuthAdmin(t, key, "orgs", "get", "shop-auth", "acme"); code != 0 {
		t.Fatalf("orgs get = %d\n%s", code, out)
	}
	want := []string{"GET /admin/v1/users?page=1&per_page=50&search=example.com", "GET /admin/v1/orgs?page=1&per_page=100&search=acme", "GET /admin/v1/orgs/" + acme}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests %v", *seen)
	}
	for _, line := range []string{"last sign-in 2026-10-17T09:00:00Z", "unconfirmed", "(anonymous)", "+15555550100", "banned until 2999-01-01", "Page 1 of 120 users; next: --page 2",
		"Organization acme (Acme)", "Members (1):", "owner", "Pending invitations (1):", "eve@example.com"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(out.String(), "acme-labs") {
		t.Fatal("a slug lookup matched part of another slug")
	}
}

func TestAuthOrgsAuditPrintsAPageWithItsCursor(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	const acme = "7c1b2c3d-0000-4000-8000-000000000009"
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		writeJSONTest(w, map[string]any{"entries": []map[string]any{
			{"occurred_at": "2026-10-19T10:02:00Z", "action": "member.role_changed", "actor_email": "ada@example.com", "target_email": "bob@example.com", "data": map[string]any{"from": "member", "to": "admin"}},
			{"occurred_at": "2026-10-19T10:01:00Z", "action": "member.added", "target_email": "bob@example.com", "data": map[string]any{"role": "member"}},
		}, "next": "WyIyMDI2LTEwLTE5Il0"})
	})
	out := captureAuthStdout(t)
	if code := runAuthAdmin(t, key, "orgs", "audit", "shop-auth", acme, "--limit", "2", "--cursor", "WyJ4Il0"); code != 0 {
		t.Fatalf("audit = %d\n%s", code, out)
	}
	if want := []string{"GET /admin/v1/orgs/" + acme + "/audit-log?cursor=WyJ4Il0&limit=2"}; !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests %v", *seen)
	}
	for _, line := range []string{"member.role_changed", "by ada@example.com", `{"from":"member","to":"admin"}`, "by (the application)", "More: --cursor WyIyMDI2LTEwLTE5Il0"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	for _, args := range [][]string{{"orgs", "audit", "shop-auth", acme, "--cursor", "a b"}, {"orgs", "get", "shop-auth", acme, "--cursor", "WyJ4Il0"}} {
		if code := runAuthAdmin(t, key, args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
}
