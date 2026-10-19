package issuetrackers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// InboundPull is an owner's explicit review; no callback can construct it as
// authority. GuardRevision comes from the preview before any provider reads.
type InboundPull struct {
	GuardRevision     int64
	Expected          api.IssueTrackerProviderStatus
	FixedDeploymentID string
}

func pullInbound(ctx context.Context, store state.IssueTrackerInboundStore, r state.IssueTrackerAutoSyncRecord, in InboundPull, read func(context.Context) (InboundPreview, api.IssueTrackerProviderStatus, error)) (api.Issue, error) {
	if in.GuardRevision <= 0 {
		return api.Issue{}, api.ErrValidation("preview the inbound change before applying")
	}
	if in.Expected.State == "resolved" {
		id, err := uuid.Parse(in.FixedDeploymentID)
		if err != nil || id == uuid.Nil {
			return api.Issue{}, api.ErrValidation("select a nonzero fixing deployment UUID for an inbound resolution")
		}
		in.FixedDeploymentID = id.String()
	} else if in.FixedDeploymentID != "" {
		return api.Issue{}, api.ErrValidation("a fixing deployment applies only to resolution")
	}
	if r.Policy.PendingState != "" || r.ClaimUntil.After(time.Now()) {
		return api.Issue{}, api.NewProblem(409, "issue_tracker_inbound_blocked", "Tracker sync is in progress", "wait for the current sync check and review any unconfirmed write before pulling")
	}
	guard, err := store.GetIssueTrackerInboundGuard(ctx, r.AppID, r.AccountID)
	if err != nil {
		return api.Issue{}, err
	}
	if guard != in.GuardRevision {
		return api.Issue{}, state.IssueTrackerInboundChanged()
	}
	preview, observed, err := read(ctx)
	if err != nil {
		return api.Issue{}, err
	}
	if observed != in.Expected {
		return api.Issue{}, state.IssueTrackerInboundChanged()
	}
	if preview.Outcome != "candidate" && preview.Outcome != "needs_deployment" {
		return api.Issue{}, api.NewProblem(409, "issue_tracker_inbound_blocked", "Inbound change needs review", "review the current inbound assessment before applying")
	}
	return store.ApplyIssueTrackerInbound(ctx, state.IssueTrackerInboundApply{Record: r, GuardRevision: guard, Observed: observed, FixedDeploymentID: in.FixedDeploymentID, Now: time.Now().UTC()})
}

func (s Service) Pull(ctx context.Context, store state.IssueTrackerInboundStore, r state.IssueTrackerAutoSyncRecord, in InboundPull) (api.Issue, error) {
	return pullInbound(ctx, store, r, in, func(ctx context.Context) (InboundPreview, api.IssueTrackerProviderStatus, error) {
		snapshot, preview, err := s.ReadInboundPreview(ctx, r)
		return preview, snapshot.Status, err
	})
}

func (s LinearService) Pull(ctx context.Context, store state.IssueTrackerInboundStore, r state.IssueTrackerAutoSyncRecord, in InboundPull) (api.Issue, error) {
	return pullInbound(ctx, store, r, in, func(ctx context.Context) (InboundPreview, api.IssueTrackerProviderStatus, error) {
		// The exact mapped state was verified when the policy was enabled. A fresh
		// native read supplies its current classification; no workflow write occurs.
		p, err := s.prepare(ctx)
		if err != nil {
			return InboundPreview{}, api.IssueTrackerProviderStatus{}, err
		}
		snapshot, err := s.Read(ctx)
		if err != nil {
			return InboundPreview{}, snapshot.Status, err
		}
		if err := s.fresh(ctx, p); err != nil {
			return InboundPreview{}, snapshot.Status, err
		}
		return inboundPreview(p, r, snapshot.Status), snapshot.Status, nil
	})
}
