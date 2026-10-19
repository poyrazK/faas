package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// H10-97: `crons rm <id> --yes` printed a bare usage line. --yes is accepted
// on either side of the id; an unknown flag fails before any request.
func TestCmdCronsRmFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantCall bool
	}{
		{name: "id only", args: []string{cronsCancelID}, wantCall: true},
		{name: "yes after id", args: []string{cronsCancelID, "--yes"}, wantCall: true},
		{name: "yes before id", args: []string{"--yes", cronsCancelID}, wantCall: true},
		{name: "unknown flag", args: []string{cronsCancelID, "--force"}, wantCode: 1},
		{name: "missing id", args: []string{"--yes"}, wantCode: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/crons/"+cronsCancelID {
					http.Error(w, "unexpected", http.StatusNotFound)
					return
				}
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			configureCronCancelClient(t, srv.URL)
			if code := cmdCronsRm(tc.args); code != tc.wantCode {
				t.Fatalf("crons rm %v = %d, want %d", tc.args, code, tc.wantCode)
			}
			if called != tc.wantCall {
				t.Fatalf("DELETE called = %v, want %v", called, tc.wantCall)
			}
		})
	}
}
