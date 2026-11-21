package tasktoolrehearsal_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestHostedTaskRehearsalDeadlineAndTerminalInvalidation(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	until := now.Add(api.HostedTaskBindingEvidenceTTL)
	pins := api.HostedTaskRehearsalPrepare{
		IntentSHA256:  toolpublication.Digest([]byte("intent")),
		ReviewSHA256:  toolpublication.Digest([]byte("review")),
		BindingSHA256: toolpublication.Digest([]byte("binding")),
	}
	binding := api.HostedTaskBindingVerification{
		Version: 1, AccountID: uuid.NewString(), IntentID: toolpublication.Digest([]byte("id")),
		IntentSHA256: pins.IntentSHA256, ReviewSHA256: pins.ReviewSHA256, BindingSHA256: pins.BindingSHA256,
		Status: "passed", Checks: tasktoolbinding.Results("passed"), OwnerAuthorizationRequired: true,
		CreatedAt: now, ExpiresAt: now.Add(api.HostedTaskSourceRetention), CheckedAt: &now, ValidUntil: &until,
	}
	r, err := tasktoolrehearsal.New(binding, pins, now)
	if err != nil || tasktoolrehearsal.ValidateRecord(r, binding.AccountID, binding.IntentID) != nil || !r.Plan.ExpiresAt.Equal(until) {
		t.Fatalf("invalid rehearsal plan: %v", err)
	}
	owner := uuid.NewString()
	digest := r.Plan.PlanSHA256
	decision := api.HostedTaskRehearsalDecision{PlanSHA256: digest, Decision: "approve"}
	if late, err := tasktoolrehearsal.Decide(r, decision, owner, until); err != nil || late.Plan.Status != tasktoolrehearsal.Expired || late.Plan.ApprovedAt != nil {
		t.Fatalf("late approval: %+v %v", late.Plan, err)
	}
	approved, err := tasktoolrehearsal.Decide(r, decision, owner, now)
	if err != nil || approved.Plan.Status != tasktoolrehearsal.Approved {
		t.Fatalf("approve: %+v %v", approved.Plan, err)
	}
	if late, acquired, err := tasktoolrehearsal.Claim(approved, digest, owner, until); err != nil || acquired || late.Plan.Status != tasktoolrehearsal.Expired {
		t.Fatalf("late claim: %+v %v", late.Plan, err)
	}
	changed := binding
	replacement := binding.CheckedAt.Add(time.Second)
	changed.CheckedAt = &replacement
	for _, tt := range []struct {
		name      string
		binding   api.HostedTaskBindingVerification
		authority bool
	}{
		{"observation replaced", changed, true},
		{"approval authority revoked", binding, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stale := tasktoolrehearsal.Refresh(approved, tt.binding, tt.authority, now.Add(time.Second))
			stale = tasktoolrehearsal.Refresh(stale, binding, true, now.Add(2*time.Second))
			out, acquired, err := tasktoolrehearsal.Claim(stale, digest, owner, now.Add(2*time.Second))
			if err != nil || acquired || out.Plan.Status != tasktoolrehearsal.Stale {
				t.Fatalf("stale plan revived: %+v %v", out.Plan, err)
			}
		})
	}
	decision.Decision = "revoke"
	revoked, err := tasktoolrehearsal.Decide(approved, decision, owner, now.Add(time.Second))
	if err != nil || revoked.Plan.Status != tasktoolrehearsal.Revoked {
		t.Fatalf("revocation: %+v %v", revoked.Plan, err)
	}
	revoked.Plan.TargetID = uuid.NewString()
	if tasktoolrehearsal.ValidateRecord(revoked, binding.AccountID, binding.IntentID) == nil {
		t.Fatal("replacement destination accepted with original digest")
	}
}
