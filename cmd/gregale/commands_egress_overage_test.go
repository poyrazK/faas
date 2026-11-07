// adr: 20261103131742 — gregale billing egress-overage shows and sets the
// egress overage choice for the account or an organization.
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestCmdEgressOverage(t *testing.T) {
	enabled := false
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/egress-overage" && r.URL.Path != "/v1/orgs/acme/egress-overage" {
			http.NotFound(w, r)
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			var body map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			enabled = body["enabled"]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "billed": true, "included_gib": 500, "millicents_per_gib": 3000})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "Egress overage: off. Apps stop at the 500 GiB"},
		{[]string{"on"}, "egress costs $0.03 per GiB, up to your spend cap"},
		{[]string{"--org", "acme", "off"}, "Egress overage: off."},
	} {
		var out bytes.Buffer
		previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
		(*main.OsStdout), (*main.JsonOutput) = &out, false
		code := main.CmdEgressOverage(tc.args)
		(*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON
		if code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("egress-overage %v = %d:\n%s\nwant %q", tc.args, code, out.String(), tc.want)
		}
	}
	want := "GET /v1/account/egress-overage,PUT /v1/account/egress-overage,PUT /v1/orgs/acme/egress-overage"
	if got := strings.Join(requests, ","); got != want {
		t.Fatalf("requests %s, want %s", got, want)
	}
	if code := main.CmdEgressOverage([]string{"maybe"}); code == 0 {
		t.Fatal("an unknown argument was accepted")
	}
	if code := main.CmdEgressOverage([]string{"on", "unexpected"}); code == 0 {
		t.Fatal("a trailing argument was accepted")
	}
}

func TestCmdEgressOverageJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "billed": true, "included_gib": 500, "millicents_per_gib": 3000})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	defer func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON }()
	if code := main.CmdEgressOverage(nil); code != 0 {
		t.Fatalf("egress-overage --json = %d:\n%s", code, out.String())
	}
	var got struct {
		Enabled     bool `json:"enabled"`
		IncludedGiB int  `json:"included_gib"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !got.Enabled || got.IncludedGiB != 500 {
		t.Fatalf("egress-overage --json = %+v, %v:\n%s", got, err, out.String())
	}
}
