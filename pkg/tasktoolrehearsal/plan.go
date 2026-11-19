// Package tasktoolrehearsal defines the authority and replay rules for a
// contained hosted Task rehearsal plan.
package tasktoolrehearsal

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

const (
	Planned  = "planned"
	Approved = "approved"
	Claimed  = "claimed"
	Revoked  = "revoked"
	Stale    = "stale"
	Expired  = "expired"
)

var (
	ErrInvalid  = errors.New("invalid hosted Task rehearsal plan")
	ErrConflict = errors.New("hosted Task rehearsal plan conflict")
)

// Record is private journal data. Rehearsal approval is pinned to a persistent
// personal admin key so its authority can be rechecked during a later claim.
type Record struct {
	Plan          api.HostedTaskRehearsalPlan
	ApprovalKeyID string
}

func ValidPrepare(in api.HostedTaskRehearsalPrepare) bool {
	return toolpublication.ValidID(in.IntentSHA256) && toolpublication.ValidID(in.ReviewSHA256) && toolpublication.ValidID(in.BindingSHA256)
}

// New pins the completed observation as well as the immutable binding selection.
// This prevents a future replacement observation from renewing a plan.
func New(binding api.HostedTaskBindingVerification, in api.HostedTaskRehearsalPrepare, now time.Time) (Record, error) {
	if !ValidPrepare(in) {
		return Record{}, ErrInvalid
	}
	if binding.Status != "passed" || tasktoolbinding.ValidateReceipt(binding, binding.AccountID, binding.IntentID) != nil ||
		in != bindingDigest(binding) || now.Before(*binding.CheckedAt) || !now.Before(*binding.ValidUntil) {
		return Record{}, ErrConflict
	}
	target := uuid.NewString()
	p := api.HostedTaskRehearsalPlan{
		HostedTaskRehearsalPrepare: in,
		Version:                    1, AccountID: binding.AccountID, IntentID: binding.IntentID, TargetID: target,
		QueueNamespace: "rehearsal-" + target,
		Status:         Planned, CreatedAt: now, BindingCheckedAt: *binding.CheckedAt, ExpiresAt: *binding.ValidUntil,
		NoTraffic: true,
	}
	p.PlanSHA256 = fingerprint(p)
	return Record{Plan: p}, nil
}

func bindingDigest(b api.HostedTaskBindingVerification) api.HostedTaskRehearsalPrepare {
	return api.HostedTaskRehearsalPrepare{IntentSHA256: b.IntentSHA256, ReviewSHA256: b.ReviewSHA256, BindingSHA256: b.BindingSHA256}
}

func fingerprint(p api.HostedTaskRehearsalPlan) string {
	p.PlanSHA256, p.Status, p.ApprovedAt, p.ClaimedAt = "", "", nil, nil
	p.BindingCheckedAt, p.CreatedAt, p.ExpiresAt = p.BindingCheckedAt.UTC(), p.CreatedAt.UTC(), p.ExpiresAt.UTC()
	body, _ := json.Marshal(p)
	return toolpublication.Digest(append([]byte("hosted-task-rehearsal-plan:v1:"), body...))
}

// Validate checks the public receipt including its immutable identity.
func Validate(p api.HostedTaskRehearsalPlan, account, id string) error {
	if p.Version != 1 || p.AccountID != account || p.IntentID != id || uuid.Validate(account) != nil ||
		!toolpublication.ValidID(id) || !ValidPrepare(p.HostedTaskRehearsalPrepare) || uuid.Validate(p.TargetID) != nil ||
		p.QueueNamespace != "rehearsal-"+p.TargetID || !p.NoTraffic || p.PlanSHA256 != fingerprint(p) ||
		p.BindingCheckedAt.IsZero() || p.CreatedAt.Before(p.BindingCheckedAt) || !p.CreatedAt.Before(p.ExpiresAt) ||
		p.ExpiresAt.Sub(p.BindingCheckedAt) > api.HostedTaskBindingEvidenceTTL {
		return ErrInvalid
	}
	if p.ApprovedAt != nil && (p.ApprovedAt.Before(p.CreatedAt) || !p.ApprovedAt.Before(p.ExpiresAt)) {
		return ErrInvalid
	}
	switch p.Status {
	case Claimed:
		if p.ApprovedAt == nil || p.ClaimedAt == nil || p.ClaimedAt.Before(*p.ApprovedAt) || !p.ClaimedAt.Before(p.ExpiresAt) {
			return ErrInvalid
		}
	case Approved, Planned, Revoked, Stale, Expired:
		if p.ClaimedAt != nil || p.Status == Approved && p.ApprovedAt == nil || p.Status == Planned && p.ApprovedAt != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func ValidateRecord(r Record, account, id string) error {
	if (r.ApprovalKeyID == "") != (r.Plan.ApprovedAt == nil) || r.ApprovalKeyID != "" && uuid.Validate(r.ApprovalKeyID) != nil {
		return ErrInvalid
	}
	return Validate(r.Plan, account, id)
}

// Refresh persists terminal invalidation of an unconsumed plan. A claimed
// receipt records a historical decision and cannot be claimed again.
func Refresh(r Record, binding api.HostedTaskBindingVerification, approvalFresh bool, now time.Time) Record {
	r = expire(r, now)
	p := &r.Plan
	if p.Status != Planned && p.Status != Approved {
		return r
	}
	if !approvalFresh || binding.Status != "passed" || bindingDigest(binding) != p.HostedTaskRehearsalPrepare ||
		binding.CheckedAt == nil || !p.BindingCheckedAt.Equal(*binding.CheckedAt) ||
		binding.ValidUntil == nil || !p.ExpiresAt.Equal(*binding.ValidUntil) {
		p.Status = Stale
	}
	return r
}

func expire(r Record, now time.Time) Record {
	if (r.Plan.Status == Planned || r.Plan.Status == Approved) && !now.Before(r.Plan.ExpiresAt) {
		r.Plan.Status = Expired
	}
	return r
}

// Decide changes metadata only. Terminal receipts replay without mutable effects.
func Decide(r Record, in api.HostedTaskRehearsalDecision, owner string, now time.Time) (Record, error) {
	if !toolpublication.ValidID(in.PlanSHA256) || uuid.Validate(owner) != nil || (in.Decision != "approve" && in.Decision != "revoke") {
		return r, ErrInvalid
	}
	if r.Plan.PlanSHA256 != in.PlanSHA256 {
		return r, ErrConflict
	}
	r = expire(r, now)
	switch in.Decision {
	case "approve":
		if r.Plan.Status == Planned {
			r.Plan.Status, r.Plan.ApprovedAt, r.ApprovalKeyID = Approved, &now, owner
		} else if (r.Plan.Status == Approved || r.Plan.Status == Claimed) && r.ApprovalKeyID != owner {
			return r, ErrConflict
		}
	case "revoke":
		if r.Plan.Status == Planned || r.Plan.Status == Approved {
			r.Plan.Status = Revoked
		}
	}
	return r, nil
}

// Claim consumes approval exactly once. Only the claiming transaction receives
// true. A future executor must never treat the public claimed status as a grant.
func Claim(r Record, digest, owner string, now time.Time) (Record, bool, error) {
	if !toolpublication.ValidID(digest) || uuid.Validate(owner) != nil {
		return r, false, ErrInvalid
	}
	if digest != r.Plan.PlanSHA256 || r.ApprovalKeyID != "" && owner != r.ApprovalKeyID {
		return r, false, ErrConflict
	}
	r = expire(r, now)
	if r.Plan.Status != Approved {
		return r, false, nil
	}
	r.Plan.Status, r.Plan.ClaimedAt = Claimed, &now
	return r, true, nil
}
