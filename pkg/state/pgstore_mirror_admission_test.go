//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreMirrorAdmissionOutcomesRoundTripAndSummarize(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := uuid.NewString(), uuid.NewString()
	sourceDeploymentID, mirrorDeploymentID, ruleID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `
INSERT INTO accounts (id, email, plan, created_at)
VALUES ($1::uuid, $2, 'pro', now())`, accountID, accountID+"@mirror-test.invalid"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO apps (id, account_id, slug, ram_mb, max_concurrency, status, created_at)
VALUES ($1::uuid, $2::uuid, $3, 128, 5, 'active', now())`, appID, accountID, "mirror-outcome-"+appID); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	for _, deploymentID := range []string{sourceDeploymentID, mirrorDeploymentID} {
		if _, err := pool.Exec(ctx, `
INSERT INTO deployments (id, app_id, scope, image_digest, status, created_at)
VALUES ($1::uuid, $2::uuid, $3, $4, 'live', now())`, deploymentID, appID,
			"mirror-"+deploymentID, "sha256:"+deploymentID); err != nil {
			t.Fatalf("insert deployment: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO mirror_rules (
    id, account_id, app_id, source_deployment_id, mirror_deployment_id,
    percent, enabled, include_body, redact_headers
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 100, true, false, '{}'::text[])`,
		ruleID, accountID, appID, sourceDeploymentID, mirrorDeploymentID); err != nil {
		t.Fatalf("insert mirror rule: %v", err)
	}

	results := []state.MirrorInvocationResult{
		{AdmissionFailureReason: state.MirrorAdmissionFailureTimeout, ComparisonIncomplete: true},
		{AdmissionFailureReason: state.MirrorAdmissionFailureRejected, ComparisonIncomplete: true},
		{AdmissionFailureReason: state.MirrorAdmissionFailureError, ComparisonIncomplete: true},
		{Crashed: true, ComparisonIncomplete: true},
	}
	for i := range results {
		results[i].MirrorRuleID = ruleID
		results[i].AccountID = accountID
		results[i].AppID = appID
		results[i].SourceDeploymentID = sourceDeploymentID
		results[i].MirrorDeploymentID = mirrorDeploymentID
		results[i].RequestID = uuid.NewString()
		results[i].CompletedAt = time.Now().UTC()
		if err := store.InsertMirrorResult(ctx, results[i]); err != nil {
			t.Fatalf("insert mirror result %d: %v", i, err)
		}
	}

	rows, err := store.ListMirrorResults(ctx, ruleID, time.Now().Add(-time.Minute), 0)
	if err != nil {
		t.Fatalf("list mirror results: %v", err)
	}
	seenReasons := map[state.MirrorAdmissionFailureReason]bool{}
	for _, row := range rows {
		if row.AdmissionFailureReason != state.MirrorAdmissionFailureNone {
			seenReasons[row.AdmissionFailureReason] = true
		}
	}
	if !seenReasons[state.MirrorAdmissionFailureTimeout] || !seenReasons[state.MirrorAdmissionFailureRejected] || !seenReasons[state.MirrorAdmissionFailureError] {
		t.Fatalf("round-tripped admission reasons = %v, want timeout, rejected, and error", seenReasons)
	}

	summary, err := store.MirrorSummary(ctx, ruleID, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("mirror summary: %v", err)
	}
	if summary.CrashCount != 1 || summary.IncompleteComparisonCount != 4 ||
		summary.SchedulerAdmissionTimeoutCount != 1 || summary.SchedulerAdmissionRejectedCount != 1 || summary.SchedulerAdmissionErrorCount != 1 {
		t.Fatalf("summary = %+v, want one guest crash, three scheduler reasons, and four incomplete comparisons", summary)
	}
}
