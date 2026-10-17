package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueLifecycleAlertClient(t *testing.T) {
	methods := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var in faas.PreviewIssueLifecycleAlertRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Event != "regressed" || r.URL.Path != "/v1/apps/payments/issue-lifecycle-alert-policy/preview" {
				t.Errorf("preview=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"issue_id":"one","event":"regressed","environment":"production","policy_revision":1,"allowed":false,"reason":"cooldown","next_eligible_at":"2026-10-17T14:01:00Z"}`))
			return
		}
		if r.URL.Path != "/v1/apps/payments/issue-lifecycle-alert-policy" {
			t.Error(r.URL.Path)
		}
		if r.Method == http.MethodPut {
			var in faas.UpdateIssueLifecycleAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision != 0 || in.CooldownSeconds != 60 {
				t.Errorf("save=%+v err=%v", in, err)
			}
		}
		_, _ = w.Write([]byte(`{"revision":1,"environments":[],"cooldown_seconds":60}`))
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if p, err := c.GetIssueLifecycleAlertPolicy(ctx, "payments"); err != nil || p.CooldownSeconds != 60 || p.Environments == nil {
		t.Fatal(p, err)
	}
	zero := int64(0)
	if p, err := c.SetIssueLifecycleAlertPolicy(ctx, "payments", faas.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &zero, Environments: []string{}, CooldownSeconds: 60}); err != nil || p.Revision != 1 {
		t.Fatal(p, err)
	}
	if p, err := c.PreviewIssueLifecycleAlert(ctx, "payments", faas.PreviewIssueLifecycleAlertRequest{IssueID: "one", Event: "regressed", Environments: []string{}, CooldownSeconds: 60}); err != nil || p.Allowed || p.NextEligibleAt == nil || p.Reason != "cooldown" {
		t.Fatal(p, err)
	}
	if len(methods) != 3 || methods[0] != "GET" || methods[1] != "PUT" || methods[2] != "POST" {
		t.Fatal(methods)
	}
}
