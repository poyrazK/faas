package tasktoolsource_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

func sourceReviewRecord(t *testing.T) api.HostedTaskSourceIntent {
	t.Helper()
	r := tasktoolsource.Review{Kind: "hosted_task_source_review", AccountID: uuid.NewString(), Sources: map[string]string{"web": strings.Repeat("a", 64), "worker": strings.Repeat("b", 64)},
		TaskReleaseReview: api.TaskReleaseReview{Version: 1, Status: "review_only", Protocol: api.MCPProtocolVersion, WorkerRuntime: "node", WebApp: "web", WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "observer", Endpoint: "/mcp", AccessMode: "open", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), PreviousWorkerApps: []string{}, ReadinessRoles: []string{}, TimeoutSeconds: 60, MigrationsRequired: true}}
	for _, role := range []string{"web", "worker", "observer"} {
		r.Apps = append(r.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("c", 64), ServingDeploymentIDs: []string{}}})
	}
	r.Fingerprint = tasktoolsource.Fingerprint(r)
	record, err := tasktoolsource.NewRecord(r.AccountID, uuid.NewString(), strings.Repeat("d", 64), r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestHostedTaskSourceOwnerDecisionLifetime(t *testing.T) {
	r := sourceReviewRecord(t)
	now := r.CreatedAt.Add(time.Second)
	in := api.HostedTaskSourceDecision{IntentSHA256: r.IntentSHA256, ReviewSHA256: r.ReviewSHA256, Decision: "approve"}
	approved, err := tasktoolsource.Change(r, "decide", in, true, true, now)
	if err != nil || approved.ReviewStatus != "approved" || approved.DecidedAt == nil || approved.ReadyToApply || approved.BindingsVerified || !approved.OwnerAuthorizationRequired {
		t.Fatal("source review approval crossed the execution boundary", approved, err)
	}
	replayed, err := tasktoolsource.Change(approved, "decide", in, true, true, now.Add(time.Second))
	if err != nil || !replayed.DecidedAt.Equal(*approved.DecidedAt) || !replayed.ExpiresAt.Equal(r.ExpiresAt) || replayed.IntentSHA256 != r.IntentSHA256 || string(replayed.Review) != string(r.Review) {
		t.Fatal("replay changed the immutable review or original decision", err)
	}
	for _, scenario := range []string{"intent", "review", "execution"} {
		bad := in
		switch scenario {
		case "intent":
			bad.IntentSHA256 = strings.Repeat("e", 64)
		case "review":
			bad.ReviewSHA256 = strings.Repeat("e", 64)
		case "execution":
			bad.Decision = "authorize"
		}
		if _, err := tasktoolsource.Change(r, "decide", bad, true, true, now); err == nil {
			t.Fatal("unbound or executable decision accepted", scenario)
		}
	}
	for _, current := range []struct {
		at     time.Time
		source bool
	}{{r.ExpiresAt, true}, {now, false}} {
		out, err := tasktoolsource.Change(approved, "decide", in, true, current.source, current.at)
		if err != nil || out.ReviewStatus != "expired" || out.SourceState != "expired" || !out.ExpiresAt.Equal(r.ExpiresAt) {
			t.Fatal("expired or missing retained source was approved", out, err)
		}
	}
	stale, err := tasktoolsource.Change(approved, "check", in, false, true, now)
	if err != nil || stale.ReviewStatus != "stale" {
		t.Fatal("native drift did not invalidate approval", err)
	}
	if out, err := tasktoolsource.Change(stale, "decide", in, true, true, now); err != nil || out.ReviewStatus != "stale" {
		t.Fatal("restoring old facts revived stale approval", err)
	}
	in.Decision = "revoke"
	revoked, err := tasktoolsource.Change(approved, "decide", in, false, true, now.Add(time.Second))
	if err != nil || revoked.ReviewStatus != "revoked" {
		t.Fatal("cancellation required live requester facts", err)
	}
	in.Decision = "approve"
	if _, err := tasktoolsource.Change(revoked, "decide", in, true, true, now); err == nil {
		t.Fatal("revoked source review was re-approved")
	}
}

func TestHostedTaskSourceDecisionReceiptValidation(t *testing.T) {
	r := sourceReviewRecord(t)
	r.ReviewStatus = "" // A retained N-1 receipt has no decision fields.
	if tasktoolsource.ValidateRecord(r, r.AccountID, r.ID) != nil || tasktoolsource.View(r, true, r.CreatedAt).ReviewStatus != "pending" {
		t.Fatal("legacy pending receipt was not preserved")
	}
	r.SourceState = "expired"
	if tasktoolsource.ValidateRecord(r, r.AccountID, r.ID) != nil || tasktoolsource.View(r, false, r.CreatedAt).ReviewStatus != "expired" {
		t.Fatal("legacy expired receipt was not preserved")
	}
	for _, status := range []string{"approved", "rejected", "revoked", "executing", "ready"} {
		bad := r
		bad.SourceState, bad.ReviewStatus = "retained", status
		if tasktoolsource.ValidateRecord(bad, bad.AccountID, bad.ID) == nil {
			t.Fatal("malformed decision receipt accepted", status)
		}
	}
}
