package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 amendment 12 — a gated release waits for approval
// before its release command. It is not live yet, but approvers find it among
// the app's pending approvals, see when it expires, and approve it as usual.
func TestReleaseWaitingBeforeItsReleaseCommandIsPending(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-before-migrate", time.Now(), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	held, err := (*f.e.Store()).CreateDeployment(ctx, state.Deployment{
		AppID: f.app.ID, ImageDigest: "sha256:migrate", Status: state.DeploySnapshotting,
		ReleaseCommand: []string{"bin/migrate"}, CreatedAt: time.Now().UTC().Add(2 * time.Second),
		DeployedByUserID: (*f.e.Acct()).ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	pendingFor := func() *api.ReleaseApprovalStatus {
		t.Helper()
		var pending api.ListReleaseApprovalsResponse
		if rec := guardDo(t, f.e, http.MethodGet, "/v1/apps/approval-before-migrate/release-approvals", nil, &pending); rec.Code != http.StatusOK {
			t.Fatalf("pending = %d %s", rec.Code, rec.Body)
		}
		for i := range pending.Items {
			if pending.Items[i].DeploymentID == held.ID {
				return &pending.Items[i]
			}
		}
		return nil
	}
	waiting := pendingFor()
	if waiting == nil || waiting.State != api.ReleaseApprovalStateWaiting || waiting.ExpiresAt == "" || waiting.WaitingSince == "" {
		t.Fatalf("held release in pending = %+v; want waiting with an expiry", waiting)
	}

	var status api.ReleaseApprovalStatus
	rec := sessionDo(t, f.e, http.MethodPost, "/v1/deployments/"+held.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove, Comment: "migration reviewed"}, &status)
	if rec.Code != http.StatusOK || status.State != api.ReleaseApprovalStateApproved {
		t.Fatalf("approve = %d %+v", rec.Code, status)
	}
	if after := pendingFor(); after != nil {
		t.Fatalf("approved release still pending: %+v", after)
	}
}
