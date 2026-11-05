// adr: 20261101214152

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Support raises an account's object-storage ceiling in whole GiB, or returns
// it to the plan's, through the admin ops route with a reason and a trace id.
func TestAccountsObjectStorageCeiling(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const gib = int64(1 << 30)
	for _, tc := range []struct {
		name    string
		args    []string
		wantGiB string
		grant   *int64
		want    string
	}{
		{name: "raise", args: []string{"--gib", "500"}, wantGiB: "500", grant: func() *int64 { b := 500 * gib; return &b }(), want: "grant=500GiB ceiling=383GiB"},
		{name: "back to the plan", args: []string{"--plan"}, wantGiB: "plan", want: "grant=none ceiling=250GiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/ops/accounts/"+testAccountID+"/object-storage-ceiling" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if q := r.URL.Query(); q.Get("confirm") != "true" || q.Get("reason") != "support_ticket" || q.Get("gib") != tc.wantGiB {
					t.Errorf("query = %q", r.URL.RawQuery)
				}
				if got := r.Header.Get(operatorTraceIDHeader); got != traceID {
					t.Errorf("trace id = %q, want %q", got, traceID)
				}
				ceiling := 250 * gib
				if tc.grant != nil {
					ceiling = 383 * gib
				}
				writeTestJSON(w, http.StatusOK, api.ObsObjectStorageCeilingResponse{
					OwnerID: testAccountID, Plan: api.PlanPro, PlanCeilingBytes: 250 * gib, GrantBytes: tc.grant, CeilingBytes: ceiling,
				})
			}))
			defer server.Close()

			installTestOperatorSession(t, server.URL, "opaque-session")
			out, stderr, restore := captureOperatorIO()
			defer restore()
			args := append([]string{"object-storage-ceiling", "--account-id", testAccountID, "--reason", "support_ticket", "--trace-id", traceID, "--yes"}, tc.args...)
			if code := cmdAccountsDispatch(args); code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
			}
			if !strings.Contains(out.String(), tc.want) || !strings.Contains(out.String(), "plan_ceiling=250GiB") {
				t.Fatalf("stdout = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestAccountsObjectStorageCeilingNeedsOneCeiling(t *testing.T) {
	_, stderr, restore := captureOperatorIO()
	defer restore()
	for _, args := range [][]string{
		{"object-storage-ceiling", "--account-id", testAccountID, "--reason", "support_ticket", "--yes"},
		{"object-storage-ceiling", "--account-id", testAccountID, "--reason", "support_ticket", "--yes", "--gib", "5", "--plan"},
	} {
		stderr.Reset()
		if code := cmdAccountsDispatch(args); code != 2 || !strings.Contains(stderr.String(), "exactly one of --gib N") {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}
