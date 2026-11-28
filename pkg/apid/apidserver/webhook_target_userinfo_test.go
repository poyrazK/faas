package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Webhook targets with credentials were stored, listed and audited in plain
// text (prod hunt r81, 2026-11-28). They are refused like log drain targets.
func TestValidateWebhookURLRefusesUserinfo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		url    string
		reject bool
	}{
		{"https://example.com/hooks/x", false},
		{"https://example.com/hooks/x?token=abc", false},
		{"https://user:pass@example.com/x", true},
		{"https://token@example.com/x", true},
		{"https://user:@example.com/x", true},
	} {
		prob := apidserver.ValidateWebhookURL(tc.url)
		if (prob != nil) != tc.reject {
			t.Errorf("ValidateWebhookURL(%q) = %v, want reject=%v", tc.url, prob, tc.reject)
		}
		if prob != nil && !strings.Contains(prob.Detail, "credentials") {
			t.Errorf("ValidateWebhookURL(%q) detail = %q, want it to name credentials", tc.url, prob.Detail)
		}
	}
}

// A malformed webhook target was refused as alert_rule_invalid with the alerts
// docs link (prod hunt r81); the egress denial keeps its stable code.
func TestWebhookTargetProblemUsesWebhookCode(t *testing.T) {
	t.Parallel()
	got := apidserver.WebhookTargetProblem(api.ErrAlertRuleInvalid("the URL's host 0 is a numeric address"))
	if got == nil || got.Code == api.CodeAlertRuleInvalid || !strings.Contains(got.Detail, "numeric address") {
		t.Fatalf("WebhookTargetProblem = %+v, want a webhook problem with the same detail", got)
	}
	denied := &api.Problem{Status: 403, Code: api.CodeImageEgressDenied}
	if apidserver.WebhookTargetProblem(denied) != denied {
		t.Fatal("egress denial must keep its code")
	}
	if apidserver.WebhookTargetProblem(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
