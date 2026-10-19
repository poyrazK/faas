package ci

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) skipAutomationPaths(ctx context.Context, tx pgx.Tx, req JobsSubmitRequest, binding GitHubBinding) error {
	if req.automation == nil || req.automationPaths == nil || req.automationPaths.Reason != "paths_filtered" ||
		req.automationPaths.HeadSHA != req.CommitSHA || len(req.automation.Policy.IncludePaths)+len(req.automation.Policy.ExcludePaths) == 0 {
		return ErrConflict
	}
	// Match the normal image/profile authority before committing any skip or
	// cancellation. Capacity is irrelevant because this creates no execution.
	if err := s.validateAutomationProfile(ctx, tx, binding.AccountID, binding, req.ProfileID); err != nil {
		return err
	}
	event := req.automation
	raw, err := json.Marshal(req.automationPaths)
	if err != nil {
		return err
	}
	if len(raw) > api.CIAutomationPathDecisionMaxBytes {
		return ErrConflict
	}
	n, err := s.q.SkipCIAutomationPaths(ctx, tx, sqlc.SkipCIAutomationPathsParams{AccountID: dbUUID(event.AccountID), ID: dbUUID(event.ID), LeaseToken: dbUUID(event.LeaseToken), PathDecision: raw})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrLeaseLost
	}
	if !req.captureChecks {
		return nil
	}
	revision, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(event.AccountID), ID: dbUUID(binding.RevisionID)})
	if err != nil {
		return storeError(err)
	}
	d := req.automationPaths
	check := ExecutionCheck{AccountID: event.AccountID, AutomationEventID: event.ID, BindingID: binding.ID, Pipeline: revision.Name,
		InstallationID: binding.InstallationID, RepositoryID: binding.RepositoryID, Repository: binding.Repository, CommitSHA: event.Source.CommitSHA,
		Version: 1, Status: "completed", Conclusion: "skipped",
		Summary: fmt.Sprintf("CI skipped by automation path filters. No compute execution was created.\n\nSource event: %s. Comparison: %s...%s. Merge base: %s. Changed files: %d.\n", event.ID, d.BaseSHA, d.HeadSHA, d.MergeBaseSHA, d.FileCount)}
	if !check.Valid() {
		return ErrConflict
	}
	raw, err = json.Marshal(check)
	if err != nil {
		return err
	}
	return storeError(s.q.InsertCIAutomationPathCheck(ctx, tx, sqlc.InsertCIAutomationPathCheckParams{AccountID: dbUUID(event.AccountID), EventID: dbUUID(event.ID), Snapshot: raw}))
}
