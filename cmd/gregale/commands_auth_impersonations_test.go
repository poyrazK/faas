package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAuthImpersonateListAndEnd(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	const ada = "6a1b2c3d-0000-4000-8000-000000000001"
	const session = "9e1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11"
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSONTest(w, map[string]any{"id": session, "session_id": "s1", "expires_at": "2026-10-19T12:30:00Z", "url": "https://shop.example.com/#access_token=a&refresh_token=r&type=impersonation"})
		case r.Method == http.MethodGet:
			writeJSONTest(w, map[string]any{"impersonations": []map[string]any{
				{"id": session, "email": "ada@example.com", "actor": "admin:0A1B2C3D", "reason": "Ticket 4821", "started_at": "2026-10-19T12:00:00Z", "expires_at": "2026-10-19T12:30:00Z", "active": true},
			}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	out := captureAuthStdout(t)
	for _, args := range [][]string{
		{"users", "impersonate", "shop-auth", ada, "--reason", "Ticket 4821", "--minutes", "15"},
		{"impersonations", "list", "shop-auth", "--user", ada},
		{"impersonations", "end", "shop-auth", session},
	} {
		if code := runAuthAdmin(t, key, args...); code != 0 {
			t.Fatalf("%v = %d\n%s", args, code, out)
		}
	}
	want := []string{
		`POST /admin/v1/users/` + ada + `/impersonate {"minutes":15,"reason":"Ticket 4821"}`,
		"GET /admin/v1/impersonations?user=" + ada,
		"DELETE /admin/v1/impersonations/" + session,
	}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests\n%v\nwant\n%v", *seen, want)
	}
	for _, line := range []string{"#access_token=a", "private window", "admin:0A1B2C3D", "active until 2026-10-19T12:30:00Z: Ticket 4821", "Ended session"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	before := len(*seen)
	for _, args := range [][]string{
		{"users", "impersonate", "shop-auth", ada},
		{"users", "impersonate", "shop-auth", ada, "--reason", "x", "--minutes", "61"},
		{"users", "get", "shop-auth", ada, "--reason", "x"},
		{"impersonations", "end", "shop-auth", "not-an-id"},
	} {
		if code := runAuthAdmin(t, key, args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
	if len(*seen) != before {
		t.Fatalf("refused commands sent requests: %v", (*seen)[before:])
	}
}
