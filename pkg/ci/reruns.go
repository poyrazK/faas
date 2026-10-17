package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRerunNotReady = errors.New("CI rerun requires a terminal execution and completed native cleanup")

// ExecutionRerun links a fresh manual execution to its immutable source history.
// It grants no automation, native attempt or runtime capability authority.
type ExecutionRerun struct {
	ParentExecutionID string `json:"parent_execution_id"`
	RootExecutionID   string `json:"root_execution_id"`
}

type ExecutionRerunStore interface {
	JobsStore
	executionRerunReplay(context.Context, string, string, string) (Execution, bool, error)
}

type executionRerunRequest struct{ Parent Execution }

func rerunLineage(parent Execution) *ExecutionRerun {
	root := parent.ID
	if parent.Rerun != nil {
		root = parent.Rerun.RootExecutionID
	}
	return &ExecutionRerun{ParentExecutionID: parent.ID, RootExecutionID: root}
}

func (r *ExecutionRerun) valid(id string) bool {
	return r == nil || ValidID(r.ParentExecutionID) && ValidID(r.RootExecutionID) && r.ParentExecutionID != id && r.RootExecutionID != id
}

// RerunJobs admits the whole frozen pipeline with new execution/attempt/output
// identities. Replayed requests read their receipt without refreshing source
// authority or creating work, including after the parent has been archived.
func RerunJobs(ctx context.Context, cfg Config, store ExecutionRerunStore, verify GitHubVerifier, account, parentID, key string) (Execution, bool, error) {
	if !cfg.DispatchAllows(account) || store == nil || verify == nil {
		return Execution{}, false, ErrDispatchDisabled
	}
	if !ValidID(parentID) || !ValidIdempotencyKey(key) {
		return Execution{}, false, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	if row, replay, err := store.executionRerunReplay(ctx, account, parentID, key); replay || err != nil {
		return row, replay, err
	}
	parent, err := store.Execution(ctx, account, parentID)
	if err != nil {
		return Execution{}, false, err
	}
	if parent.Mode != ExecutionJobs || parent.Compute == nil {
		return Execution{}, false, ErrNotFound
	}
	if !terminal(parent.Status) || parent.FinishedAt == nil {
		return Execution{}, false, ErrRerunNotReady
	}
	req := JobsSubmitRequest{ProfileID: parent.ProfileID, SubmitRequest: SubmitRequest{CommitSHA: parent.CommitSHA}, rerun: &executionRerunRequest{Parent: parent}}
	if req.ProfileID == "" {
		req.RevisionID, req.BindingID = parent.RevisionID, parent.Compute.Binding.ID
		req.Templates = make(map[string]string, len(parent.Compute.Templates))
		for name, template := range parent.Compute.Templates {
			req.Templates[name] = template.ID
		}
	}
	freshVerify := func(ctx context.Context, account string, installation, repository int64, name string) error {
		if err := verify(ctx, account, installation, repository, name); err != nil {
			if errors.Is(err, ErrGitHubAccess) {
				return ErrGitHubAccess
			}
			return ErrGitHubUnavailable
		}
		return nil
	}
	return AdmitJobs(ctx, cfg, store, freshVerify, account, key, req)
}
