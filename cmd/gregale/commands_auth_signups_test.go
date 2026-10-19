package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAuthSignupsListApproveAndRemove(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	seen := fakeAuthAdmin(t, key, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch {
		case r.Method == http.MethodGet:
			writeJSONTest(w, map[string]any{"signups": []map[string]any{
				{"email": "wait@example.com", "requested_at": "2026-10-19T10:00:00Z"},
				{"email": "ok@example.com", "approved_at": "2026-10-19T11:00:00Z"},
				{"email": "in@example.com", "approved_at": "2026-10-18T11:00:00Z", "signed_up": true},
			}, "total": 3, "page": 1, "per_page": 50})
		case r.Method == http.MethodPost:
			writeJSONTest(w, map[string]any{"approved": 2, "notified": 1})
		case r.URL.Path == "/admin/v1/signups/gone@example.com":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found"}`))
		}
	})
	out := captureAuthStdout(t)
	for _, args := range [][]string{
		{"signups", "list", "shop-auth", "--status", "pending"},
		{"signups", "approve", "shop-auth", "ada@example.com", "bob@example.com"},
		{"signups", "approve", "shop-auth", "eve@example.com", "--no-email"},
		{"signups", "remove", "shop-auth", "Gone@example.com"},
	} {
		if code := runAuthAdmin(t, key, args...); code != 0 {
			t.Fatalf("%v = %d\n%s", args, code, out)
		}
	}
	want := []string{
		"GET /admin/v1/signups?page=1&per_page=50&status=pending",
		`POST /admin/v1/signups/approve {"emails":["ada@example.com","bob@example.com"],"notify":true}`,
		`POST /admin/v1/signups/approve {"emails":["eve@example.com"],"notify":false}`,
		"DELETE /admin/v1/signups/gone@example.com",
	}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("requests\n%v\nwant\n%v", *seen, want)
	}
	for _, line := range []string{"wait@example.com", "waiting since 2026-10-19T10:00:00Z", "approved 2026-10-19T11:00:00Z", "signed up", "Approved 2 address(es); 1 emailed"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	before := len(*seen)
	for _, args := range [][]string{
		{"signups", "approve", "shop-auth"},
		{"signups", "approve", "shop-auth", "not-an-address"},
		{"signups", "list", "shop-auth", "--status", "old"},
		{"signups", "list", "shop-auth", "--no-email"},
		{"signups", "remove", "shop-auth", "a@example.com", "b@example.com"},
	} {
		if code := runAuthAdmin(t, key, args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
	if code := runAuthAdmin(t, key, "signups", "remove", "shop-auth", "nobody@example.com"); code == 0 {
		t.Error("removing an unknown address succeeded")
	}
	if len(*seen) != before+1 {
		t.Fatalf("refused commands sent requests: %v", (*seen)[before:])
	}
}
