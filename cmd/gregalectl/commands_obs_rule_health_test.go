package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func ruleHealthTestResponse(ctx context.Context, t *testing.T) api.ObsRuleHealthResponse {
	t.Helper()
	now := time.Now().UTC()
	out := operatorobs.RuleHealth(ctx, nil, now)
	out.State, out.Reason = "healthy", ""
	out.Totals = &api.ObsRuleHealthTotals{Groups: 1, Rules: 1, Healthy: 1}
	zero, age := 0, int64(0)
	out.Items = []api.ObsRuleHealthItem{{Kind: "rule", ID: strings.Repeat("1", 64), GroupID: strings.Repeat("2", 64), GroupName: "faas", FileName: "faas.rules.yml", RuleName: "FaasApiDown", State: "healthy", Issues: []string{}, ReportedState: "inactive", LastEvaluatedAt: &now, EvaluationAgeSeconds: &age, Pending: &zero, Firing: &zero}}
	out.TotalItems = 1
	return out
}

func TestCmdObsRuleHealth(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	response := operatorobs.RuleHealth(t.Context(), nil, time.Now().UTC())
	mode := "ok"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.RuleHealthPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			http.Error(w, "private upstream body", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsRuleHealth}); code != 0 || !strings.Contains(out.String(), "totals=unknown") {
		t.Fatalf("unknown: %d %s %s", code, out, stderr)
	}
	response = ruleHealthTestResponse(t.Context(), t)
	out.Reset()
	if code := cmdObsRuleHealth(nil); code != 0 || !strings.Contains(out.String(), "pending=0 firing=0") || !strings.Contains(out.String(), "expected_inventory=unobserved") {
		t.Fatalf("known quiet: %d %s %s", code, out, stderr)
	}
	response.State, response.Reason = "degraded", "rule_diagnostics_present"
	response.Totals.Healthy, response.Totals.Stale = 0, 1
	response.Items[0].State = "stale"
	response.Items[0].Issues = []string{"evaluation_stale"}
	response.Items[0].Pending, response.Items[0].Firing = nil, nil
	out.Reset()
	if code := cmdObsRuleHealth(nil); code != 0 || !strings.Contains(out.String(), "pending=unknown firing=unknown") {
		t.Fatalf("unhealthy counts: %d %s %s", code, out, stderr)
	}
	out.Reset()
	if code := cmdObsRuleHealth([]string{"--json"}); code != 0 || !strings.Contains(out.String(), `"pending": null`) {
		t.Fatalf("json: %d %s %s", code, out, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--timeout", "0s"}, {"extra"}, {"--since", "now"}} {
		if code := cmdObsRuleHealth(args); code != 2 {
			t.Fatalf("invalid args: %d", code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsRuleHealth(nil); code != 2 || calls != before {
		t.Fatal("invalid arguments or missing token made request")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	mode = "error"
	out.Reset()
	stderr.Reset()
	if code := cmdObsRuleHealth(nil); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("error redaction: %d %s %s", code, out, stderr)
	}
	mode = "ok"
	response = ruleHealthTestResponse(t.Context(), t)
	response.Items = nil
	if code := cmdObsRuleHealth(nil); code != 1 {
		t.Fatal("missing diagnostics accepted")
	}
}

func TestObsBundleRuleHealthAdvertisementAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		calls      int
	}{
		{"current", operatorobs.RuleHealthPath, 4}, {"legacy", "", 3}, {"untrusted", "https://private.example/rules", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := obsBundleTestSnapshot(obsBundleTestRef, 2, 3)
			snapshot.Health.Data.AlertHistory.DiagnosticsPath = tc.path
			response := ruleHealthTestResponse(t.Context(), t)
			response.State, response.Reason = "degraded", "rule_diagnostics_present"
			response.Totals.Healthy, response.Totals.Failed = 0, 1
			response.Items[0].State = "failed"
			response.Items[0].Issues = []string{"evaluation_failed"}
			response.Items[0].Pending, response.Items[0].Firing = nil, nil
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.URL.Path == operatorobs.RuleHealthPath {
					body, _ := json.Marshal(response)
					_, _ = w.Write(append(body[:len(body)-1], []byte(`,"raw_query":"private query","raw_error":"private error"}`)...))
					return
				}
				obsBundleTestSection(w, r, snapshot)
			}))
			defer srv.Close()
			bundle, err := collectObsBundle(t.Context(), srv.URL, obsCredential{bearer: "token"}, obsBundleTestRef, 2, 3)
			if err != nil || len(paths) != tc.calls {
				t.Fatalf("capture: calls=%v err=%v", paths, err)
			}
			if tc.name == "current" {
				if bundle.RuleHealth == nil || bundle.RuleHealth.CollectedAt.IsZero() || bundle.Coverage.RuleHealthState != "degraded" || paths[3] != operatorobs.RuleHealthPath {
					t.Fatalf("missing diagnostics: %+v", bundle.Coverage)
				}
			} else if bundle.RuleHealth != nil || bundle.Coverage.RuleHealthState != "unknown" {
				t.Fatalf("unsupported source invented coverage: %+v", bundle.Coverage)
			}
			body, err := marshalObsBundle(bundle)
			if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "raw_query") {
				t.Fatalf("bundle redaction: %s %v", body, err)
			}
		})
	}
}

func TestObsBundleRuleHealthDeadlineAndFailure(t *testing.T) {
	for _, mode := range []string{"error", "timeout", "invalid", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := obsBundleTestSnapshot(obsBundleTestRef, 2, 3)
			snapshot.Health.Data.AlertHistory.DiagnosticsPath = operatorobs.RuleHealthPath
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != operatorobs.RuleHealthPath {
					obsBundleTestSection(w, r, snapshot)
					return
				}
				switch mode {
				case "error":
					http.Error(w, "private error", 503)
				case "timeout":
					<-r.Context().Done()
				case "invalid":
					response := ruleHealthTestResponse(r.Context(), t)
					response.Totals.Healthy = 0
					_ = json.NewEncoder(w).Encode(response)
				default:
					_ = json.NewEncoder(w).Encode(operatorobs.RuleHealth(r.Context(), nil, time.Now().UTC()))
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			bundle, err := collectObsBundle(ctx, srv.URL, obsCredential{bearer: "token"}, obsBundleTestRef, 2, 3)
			if mode == "disabled" {
				if err != nil || bundle.Coverage.RuleHealthState != "disabled" {
					t.Fatalf("explicit disabled source: %v %+v", err, bundle.Coverage)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed source fabricated diagnostics: %v", err)
			}
		})
	}
}
