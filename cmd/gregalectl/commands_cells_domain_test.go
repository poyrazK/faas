package main

// adr: 657
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCellsCLIAppsDomainOmissionSetAndClear(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		present bool
		value   string
	}{
		{"omitted", nil, false, ""},
		{"set", []string{"--apps-domain=apps.fsn.example.com"}, true, "apps.fsn.example.com"},
		{"clear", []string{"--apps-domain="}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				value, present := body["apps_domain"]
				if present != tc.present || present && value != tc.value {
					t.Error("domain intent changed", body)
				}
				_ = json.NewEncoder(w).Encode(api.GlobalCellResponse{Region: "eu-fsn", AppsDomain: tc.value})
			}))
			defer srv.Close()
			installTestOperatorSession(t, srv.URL, "operator-session")
			old := osStdout
			var output bytes.Buffer
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"set", "--cell-id", uuid.NewString(), "--region=eu-fsn", "--api-base=https://api.example.com", "--enabled=true"}
			if code := cmdCellsDispatch(append(args, tc.flags...)); code != 0 || !called {
				t.Fatal(code, called)
			}
			if tc.value != "" && !strings.Contains(output.String(), "apps_domain="+tc.value) {
				t.Fatal("domain missing from operator output", output.String())
			}
			called = false
			if code := cmdCellsDispatch(append(args, "--apps-domain=https://apps.example.com")); code != 2 || called {
				t.Fatal("invalid domain submitted", code, called)
			}
		})
	}
}
