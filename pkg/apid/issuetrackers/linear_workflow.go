package issuetrackers

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

// LinearWorkflowMapping explicitly selects the destinations for outbound sync.
type LinearWorkflowMapping struct {
	OpenStateID     string
	ResolvedStateID string
}

// VerifyWorkflowMapping validates both destinations without writing to Linear.
// The returned snapshot is scoped to the connection and issue group observed
// throughout verification; callers must persist that scope with delegation.
func (s LinearService) VerifyWorkflowMapping(ctx context.Context, mapping LinearWorkflowMapping) (api.NativeLinearIssueTrackerStatus, error) {
	open, e1 := uuid.Parse(mapping.OpenStateID)
	resolved, e2 := uuid.Parse(mapping.ResolvedStateID)
	if e1 != nil || e2 != nil || open == uuid.Nil || resolved == uuid.Nil || open == resolved {
		return api.NativeLinearIssueTrackerStatus{}, api.ErrValidation("select distinct nonzero open and resolved Linear workflow state UUIDs")
	}
	verifier, ok := s.Provider.(issuetracker.LinearWorkflowVerifier)
	if !ok {
		return api.NativeLinearIssueTrackerStatus{}, unavailable()
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	for _, target := range []struct{ id, state string }{{open.String(), "open"}, {resolved.String(), "resolved"}} {
		if err := s.fresh(ctx, p); err != nil {
			return api.NativeLinearIssueTrackerStatus{}, err
		}
		if err := verifier.VerifyLinearWorkflowState(ctx, token, p.Connection.Linear.Scope.Identity.TeamID, target.id, target.state); err != nil {
			return api.NativeLinearIssueTrackerStatus{}, mappedError(err)
		}
	}
	observed, err := s.Read(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	return observed, nil
}

// ReadWithWorkflows pins the connection and group across catalog discovery and
// the native issue snapshot. Page loads must not call this provider operation.
func (s LinearService) ReadWithWorkflows(ctx context.Context) (api.NativeLinearIssueTrackerStatus, []issuetracker.LinearWorkflowState, error) {
	lister, ok := s.Provider.(issuetracker.LinearWorkflowLister)
	if !ok {
		return api.NativeLinearIssueTrackerStatus{}, nil, unavailable()
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, err
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, err
	}
	catalog, err := lister.ListLinearWorkflowStates(ctx, token, p.Connection.Linear.Scope.Identity.TeamID)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, mappedError(err)
	}
	catalog, err = issuetracker.NormalizeLinearWorkflowStates(catalog, p.Connection.Linear.Scope.Identity.TeamID)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, unavailable()
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, err
	}
	snapshot, err := s.Read(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, err
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, err
	}
	return snapshot, catalog, nil
}
