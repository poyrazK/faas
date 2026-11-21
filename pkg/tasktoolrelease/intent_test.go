package tasktoolrelease_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func fixture() api.TaskToolReleaseIntent {
	in := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), Protocol: api.MCPProtocolVersion, WorkerRuntime: "node",
		ObserverMetricApp: "worker", Endpoint: "/mcp", AccessMode: "open", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: "Review report worker"}
	for _, role := range []string{"web", "worker", "observer"} {
		in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: api.AppReleaseReview{
			AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("b", 64), ServingDeploymentIDs: []string{}}})
	}
	return in
}

func TestMCPTaskIntentApprovalLifetime(t *testing.T) {
	now := time.Now().UTC()
	r, err := tasktoolrelease.New(uuid.NewString(), uuid.NewString(), strings.Repeat("c", 64), fixture(), now)
	if err != nil {
		t.Fatal(err)
	}
	decision := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, Decision: "approve"}
	approved, err := tasktoolrelease.Decide(r, decision, now.Add(time.Second))
	if err != nil || approved.Status != "approved" || approved.ReadyToApply || approved.BindingsVerified {
		t.Fatal("review approval granted execution authority", approved, err)
	}
	if retry, err := tasktoolrelease.Decide(approved, decision, now.Add(2*time.Second)); err != nil || !retry.ExpiresAt.Equal(r.ExpiresAt) || !retry.DecidedAt.Equal(*approved.DecidedAt) {
		t.Fatal("retry changed the original decision/deadline", retry, err)
	}
	wrong := decision
	wrong.IntentSHA256 = strings.Repeat("d", 64)
	if _, err := tasktoolrelease.Decide(r, wrong, now); err == nil {
		t.Fatal("approval was not bound to the complete intent")
	}
	if _, err := tasktoolrelease.Decide(approved, decision, r.ExpiresAt); err == nil {
		t.Fatal("expired approval revived")
	}
	for _, sourceChanged := range []bool{false, true} {
		fingerprint, nativeFresh := r.Intent.ReleaseFingerprint, false
		if sourceChanged {
			fingerprint, nativeFresh = strings.Repeat("e", 64), true
		}
		stale := tasktoolrelease.Check(approved, fingerprint, nativeFresh, now)
		if stale.Status != "stale" || tasktoolrelease.Check(stale, r.Intent.ReleaseFingerprint, true, now).Status != "stale" {
			t.Fatal("restoring old inputs revived a stale approval")
		}
	}
	decision.Decision = "revoke"
	revoked, err := tasktoolrelease.Decide(approved, decision, now)
	decision.Decision = "approve"
	if err != nil || revoked.Status != "revoked" {
		t.Fatal("owner could not revoke", err)
	}
	if _, err := tasktoolrelease.Decide(revoked, decision, now); err == nil {
		t.Fatal("revoked review was re-approved")
	}
}

func TestMCPTaskIntentRejectsUnsupportedReviews(t *testing.T) {
	for _, scenario := range []string{"duplicate app", "replaced app", "named environment", "missing setup", "unknown runtime", "invalid protocol", "missing permission", "invalid reason"} {
		t.Run(scenario, func(t *testing.T) {
			in := fixture()
			switch scenario {
			case "duplicate app":
				in.Apps[1].App = in.Apps[0].App
			case "replaced app":
				in.Apps[1].Review.AppID = in.Apps[0].Review.AppID
			case "named environment":
				in.Apps[0].Review.Scope = "staging"
			case "missing setup":
				in.RequiredSetup = in.RequiredSetup[:1]
			case "unknown runtime":
				in.WorkerRuntime = "python"
			case "invalid protocol":
				in.Protocol = api.MCPLegacyProtocolVersion
			case "missing permission":
				in.ToolScopes = nil
			case "invalid reason":
				in.Reason = "review\nrelease"
			}
			if tasktoolrelease.Validate(in) == nil {
				t.Fatal("unsupported intent accepted")
			}
		})
	}
}
