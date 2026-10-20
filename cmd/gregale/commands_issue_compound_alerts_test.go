// adr: 1662
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueCompoundAlertsCLI(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			var in api.UpdateIssueCompoundAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision != 2 || len(in.Rules) != 2 || in.Rules[1].MinimumCustomers != 3 {
				t.Errorf("save=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"revision":3,"rules":[]}`))
		case http.MethodPost:
			var in api.PreviewIssueCompoundAlertRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Rule.MinimumCustomers != 3 || in.IssueID == "" {
				t.Errorf("preview=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"allowed":false,"reason":"customer_threshold"}`))
		default:
			_, _ = w.Write([]byte(`{"revision":2,"rules":[{"key":"other","enabled":true,"environment":"","minimum_events":1,"minimum_customers":1,"window_seconds":60,"cooldown_seconds":60}]}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previous := (*main.OsStdout)
	var output bytes.Buffer
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = previous })
	base := []string{"--app", "payments", "--key", "payments", "--minimum-events", "20", "--minimum-customers", "3", "--window-seconds", "600", "--cooldown-seconds", "1800"}
	if main.CmdIssueCompoundAlerts(append([]string{"save", "--expected-revision", "2"}, base...)) != 0 {
		t.Fatal("save")
	}
	if main.CmdIssueCompoundAlerts(append([]string{"preview", "--issue", "28ad5060-28e6-4ba5-ac6a-9990691c11c3"}, base...)) != 0 {
		t.Fatal("preview")
	}
	if requests != 3 {
		t.Fatal(requests)
	}
	for _, args := range [][]string{{"save", "--app", "payments"}, {"get", "--app", "payments", "--key", "unexpected"}, {"remove", "--app", "payments", "--expected-revision", "2"}, {"preview", "--app", "payments", "--issue", "bad"}} {
		if main.CmdIssueCompoundAlerts(args) == 0 {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	if requests != 3 {
		t.Fatal("invalid input made API request")
	}
}
