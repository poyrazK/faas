package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func bindingRow(row sqlc.CiGithubBinding) (GitHubBinding, error) {
	b := GitHubBinding{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), Name: row.Name, Repository: row.Repository, Enabled: row.Enabled, Generation: row.Generation, CreatedAt: row.CreatedAt.Time,
		GitHubBindingRequest: GitHubBindingRequest{RevisionID: uuidString(row.RevisionID), InstallationID: row.InstallationID, RepositoryID: row.RepositoryID}}
	if err := json.Unmarshal(row.PushBranches, &b.PushBranches); err != nil {
		return b, fmt.Errorf("decode CI push filters: %w", err)
	}
	if err := json.Unmarshal(row.PrBranches, &b.PullRequestBranches); err != nil {
		return b, fmt.Errorf("decode CI PR filters: %w", err)
	}
	return b, nil
}

func (s *PostgresStore) githubBindings(ctx context.Context, db sqlc.DBTX, account string) ([]GitHubBinding, error) {
	rows, err := s.q.ListCIGitHubBindings(ctx, db, sqlc.ListCIGitHubBindingsParams{AccountID: dbUUID(account), MaxRows: api.CIGitHubMaxBindingsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]GitHubBinding, 0, len(rows))
	for _, row := range rows {
		b, err := bindingRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *PostgresStore) GitHubBindings(ctx context.Context, account string) ([]GitHubBinding, error) {
	return s.githubBindings(ctx, s.pool, account)
}

func (s *PostgresStore) PutGitHubBinding(ctx context.Context, account, name string, req GitHubBindingRequest) (GitHubBinding, error) {
	req, err := NormalizeGitHubBinding(req)
	if err != nil || !ValidName(name) {
		return GitHubBinding{}, fmt.Errorf("invalid CI GitHub binding")
	}
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return GitHubBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(account), ID: dbUUID(req.RevisionID)})
	if err != nil {
		return GitHubBinding{}, storeError(err)
	}
	revision, err := revisionRow(r)
	if err != nil {
		return GitHubBinding{}, err
	}
	bindings, err := s.githubBindings(ctx, tx, account)
	if err != nil {
		return GitHubBinding{}, err
	}
	found := false
	for _, b := range bindings {
		if b.Name == name {
			found = true
			if b.Enabled && sameGitHubBindingRequest(b.GitHubBindingRequest, req) {
				return b, nil
			}
		}
	}
	if !found && len(bindings) >= api.CIGitHubMaxBindingsPerAccount {
		return GitHubBinding{}, &LimitError{Resource: "github_bindings", Limit: api.CIGitHubMaxBindingsPerAccount, Observed: len(bindings) + 1}
	}
	push, _ := json.Marshal(nonNilBranches(req.PushBranches))
	prs, _ := json.Marshal(nonNilBranches(req.PullRequestBranches))
	row, err := s.q.UpsertCIGitHubBinding(ctx, tx, sqlc.UpsertCIGitHubBindingParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account), Name: name, RevisionID: r.ID, InstallationID: req.InstallationID, RepositoryID: req.RepositoryID, Repository: revision.Definition.Repository, PushBranches: push, PrBranches: prs})
	if err != nil {
		return GitHubBinding{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GitHubBinding{}, storeError(err)
	}
	return bindingRow(row)
}

func nonNilBranches(branches []string) []string {
	if branches == nil {
		return []string{}
	}
	return branches
}

func (s *PostgresStore) DisableGitHubBinding(ctx context.Context, account, name string) error {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := s.q.DisableCIGitHubBinding(ctx, tx, sqlc.DisableCIGitHubBindingParams{AccountID: dbUUID(account), Name: name})
	if err != nil {
		return storeError(err)
	}
	if err := s.q.IgnoreCIGitHubBindingTriggers(ctx, tx, sqlc.IgnoreCIGitHubBindingTriggersParams{AccountID: b.AccountID, BindingID: b.ID}); err != nil {
		return storeError(err)
	}
	if err := s.q.IgnoreCIGitHubBindingChecks(ctx, tx, sqlc.IgnoreCIGitHubBindingChecksParams{AccountID: b.AccountID, BindingID: b.ID}); err != nil {
		return storeError(err)
	}
	if err := s.ignoreAutomationBinding(ctx, tx, account, uuidString(b.ID), "binding_disabled"); err != nil {
		return err
	}
	return storeError(tx.Commit(ctx))
}

func (s *PostgresStore) RecordGitHubDelivery(ctx context.Context, account, deliveryID string, event GitHubEvent) error {
	return s.recordGitHubDelivery(ctx, account, deliveryID, event, false)
}

func (s *PostgresStore) recordGitHubDelivery(ctx context.Context, account, deliveryID string, event GitHubEvent, automation bool) error {
	if !ValidID(deliveryID) || !event.valid() {
		return fmt.Errorf("invalid authenticated CI GitHub event")
	}
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bindings, err := s.githubBindings(ctx, tx, account)
	if err != nil {
		return err
	}
	count, err := s.q.CountCIGitHubTriggers(ctx, tx, dbUUID(account))
	if err != nil {
		return storeError(err)
	}
	for _, b := range bindings {
		if !bindingMatches(b, event) {
			// A retarget to an unlisted base still invalidates an in-flight
			// observation for this PR. It creates no source or observation row.
			if b.Enabled && event.Kind == "pull_request" && b.InstallationID == event.InstallationID &&
				b.RepositoryID == event.RepositoryID && strings.EqualFold(b.Repository, event.Repository) {
				if err := s.q.InvalidateCIAutomationPullRequest(ctx, tx, sqlc.InvalidateCIAutomationPullRequestParams{AccountID: dbUUID(account), BindingID: dbUUID(b.ID), PrNumber: int64(event.PRNumber)}); err != nil {
					return storeError(err)
				}
			}
			continue
		}
		recorded, err := s.gitHubEventRecorded(ctx, tx, account, b.ID, event.Key())
		if err != nil {
			return err
		}
		if recorded {
			continue
		}
		count++
		if count > api.CIGitHubMaxTriggersPerAccount {
			return &LimitError{Resource: "github_triggers", Limit: api.CIGitHubMaxTriggersPerAccount, Observed: int(count)}
		}
		switch event.Kind {
		case "push":
			if err := s.q.InvalidateCIAutomationHead(ctx, tx, sqlc.InvalidateCIAutomationHeadParams{AccountID: dbUUID(account), BindingID: dbUUID(b.ID), Branch: event.Branch}); err != nil {
				return storeError(err)
			}
		case "pull_request":
			if err := s.q.InvalidateCIAutomationPullRequest(ctx, tx, sqlc.InvalidateCIAutomationPullRequestParams{AccountID: dbUUID(account), BindingID: dbUUID(b.ID), PrNumber: int64(event.PRNumber)}); err != nil {
				return storeError(err)
			}
		}
		if automation {
			captured, err := s.captureAutomation(ctx, tx, b, event, deliveryID)
			if err != nil {
				return err
			}
			if captured {
				continue
			}
		}
		row := GitHubTrigger{ID: uuid.NewString(), AccountID: b.AccountID, Binding: b, Source: GitHubSource{GitHubEvent: event, BindingID: b.ID, Generation: b.Generation, DeliveryID: deliveryID}}
		data, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode CI trigger: %w", err)
		}
		if len(data) > api.CIGitHubSnapshotMaxBytes {
			return fmt.Errorf("CI trigger snapshot exceeds allowance")
		}
		if err := s.q.InsertCIGitHubTrigger(ctx, tx, sqlc.InsertCIGitHubTriggerParams{ID: dbUUID(row.ID), AccountID: dbUUID(account), BindingID: dbUUID(b.ID), EventKey: event.Key(), Snapshot: data}); err != nil {
			return storeError(err)
		}
	}
	return storeError(tx.Commit(ctx))
}

func triggerRow(row sqlc.CiGithubTrigger) (GitHubTrigger, error) {
	var out GitHubTrigger
	if err := json.Unmarshal(row.Snapshot, &out); err != nil {
		return out, fmt.Errorf("decode CI trigger: %w", err)
	}
	if out.ID != uuidString(row.ID) || out.AccountID != uuidString(row.AccountID) || out.Binding.ID != uuidString(row.BindingID) || !sameAccount(out.Binding.AccountID, out.AccountID) || out.Source.BindingID != out.Binding.ID || out.Source.Key() != row.EventKey {
		return GitHubTrigger{}, fmt.Errorf("CI trigger snapshot identity mismatch")
	}
	out.Status, out.Attempts = row.Status, int(row.Attempts)
	if row.LeaseToken.Valid {
		out.LeaseToken = uuidString(row.LeaseToken)
	}
	if row.RunID.Valid {
		out.RunID = uuidString(row.RunID)
	}
	return out, nil
}

func (s *PostgresStore) ClaimGitHubTrigger(ctx context.Context, account string) (GitHubTrigger, error) {
	if err := s.q.ExpireCIGitHubTriggers(ctx, s.pool, sqlc.ExpireCIGitHubTriggersParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return GitHubTrigger{}, storeError(err)
	}
	row, err := s.q.ClaimCIGitHubTrigger(ctx, s.pool, sqlc.ClaimCIGitHubTriggerParams{AccountID: dbUUID(account), Token: dbUUID(uuid.NewString()), LeaseSeconds: api.CIGitHubLeaseSeconds, MaxAttempts: api.CIGitHubMaxAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubTrigger{}, ErrNoGitHubWork
	}
	if err != nil {
		return GitHubTrigger{}, storeError(err)
	}
	return triggerRow(row)
}

func (s *PostgresStore) AdmitGitHubTrigger(ctx context.Context, claim GitHubTrigger) (Run, error) {
	tx, err := s.accountTx(ctx, claim.AccountID)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := s.q.LockCIGitHubTrigger(ctx, tx, sqlc.LockCIGitHubTriggerParams{AccountID: dbUUID(claim.AccountID), ID: dbUUID(claim.ID)})
	if err != nil {
		return Run{}, storeError(err)
	}
	t, err := triggerRow(stored)
	if err != nil {
		return Run{}, err
	}
	if t.Status == "admitted" {
		r, err := s.q.GetCIRun(ctx, tx, sqlc.GetCIRunParams{AccountID: stored.AccountID, ID: stored.RunID})
		if err != nil {
			return Run{}, storeError(err)
		}
		return runRow(r)
	}
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return Run{}, storeError(err)
	}
	if t.Status != "processing" || t.LeaseToken != claim.LeaseToken || !now.Time.Before(stored.LeaseUntil.Time) {
		return Run{}, ErrLeaseLost
	}
	b, err := s.q.GetCIGitHubBinding(ctx, tx, sqlc.GetCIGitHubBindingParams{AccountID: stored.AccountID, ID: stored.BindingID})
	if err != nil {
		return Run{}, storeError(err)
	}
	binding, err := bindingRow(b)
	if err != nil {
		return Run{}, err
	}
	if !githubBindingStillAuthorized(binding, t.Binding) {
		return Run{}, ErrGitHubIgnored
	}
	count, err := s.q.CountCIRuns(ctx, tx, stored.AccountID)
	if err != nil {
		return Run{}, storeError(err)
	}
	if count >= api.CIMaxRunsPerAccount {
		return Run{}, &LimitError{Resource: "runs", Limit: api.CIMaxRunsPerAccount, Observed: int(count) + 1}
	}
	return s.admitGitHubRun(ctx, tx, t)
}

func (s *PostgresStore) admitGitHubRun(ctx context.Context, tx pgx.Tx, t GitHubTrigger) (Run, error) {
	r, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(t.AccountID), ID: dbUUID(t.Binding.RevisionID)})
	if err != nil {
		return Run{}, storeError(err)
	}
	row, err := s.insertRunRecord(ctx, tx, t.AccountID, "github:"+t.ID, SubmitRequest{RevisionID: t.Binding.RevisionID, CommitSHA: t.Source.CommitSHA}, r)
	if err != nil {
		return Run{}, err
	}
	source, err := json.Marshal(t.Source)
	if err != nil {
		return Run{}, fmt.Errorf("encode CI source: %w", err)
	}
	row, err = s.q.SetCIRunGitHubSource(ctx, tx, sqlc.SetCIRunGitHubSourceParams{AccountID: row.AccountID, ID: row.ID, GithubSource: source})
	if err != nil {
		return Run{}, storeError(err)
	}
	if err := s.q.InsertCIGitHubCheck(ctx, tx, sqlc.InsertCIGitHubCheckParams{AccountID: row.AccountID, RunID: row.ID}); err != nil {
		return Run{}, storeError(err)
	}
	if err := s.q.AdmitCIGitHubTrigger(ctx, tx, sqlc.AdmitCIGitHubTriggerParams{AccountID: row.AccountID, ID: dbUUID(t.ID), RunID: row.ID}); err != nil {
		return Run{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, storeError(err)
	}
	return runRow(row)
}

func (s *PostgresStore) FailGitHubTrigger(ctx context.Context, claim GitHubTrigger, reason string) error {
	now, err := s.q.CIExecutionClock(ctx, s.pool)
	if err != nil {
		return storeError(err)
	}
	count, err := s.q.FailCIGitHubTrigger(ctx, s.pool, sqlc.FailCIGitHubTriggerParams{AccountID: dbUUID(claim.AccountID), ID: dbUUID(claim.ID), LeaseToken: dbUUID(claim.LeaseToken), Status: githubFailureState(claim.Attempts, reason), LastError: reason, NextAttemptAt: timestamp(githubRetry(claim.Attempts, now.Time))})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) ClaimGitHubCheck(ctx context.Context, account string) (GitHubCheck, error) {
	if err := s.q.ExpireCIGitHubChecks(ctx, s.pool, sqlc.ExpireCIGitHubChecksParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return GitHubCheck{}, storeError(err)
	}
	row, err := s.q.ClaimCIGitHubCheck(ctx, s.pool, sqlc.ClaimCIGitHubCheckParams{AccountID: dbUUID(account), Token: dbUUID(uuid.NewString()), LeaseSeconds: api.CIGitHubLeaseSeconds, MaxAttempts: api.CIGitHubMaxAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubCheck{}, ErrNoGitHubWork
	}
	if err != nil {
		return GitHubCheck{}, storeError(err)
	}
	run, err := s.Run(ctx, account, uuidString(row.RunID))
	if err != nil {
		return GitHubCheck{}, err
	}
	if run.GitHub == nil {
		return GitHubCheck{}, fmt.Errorf("CI check source missing")
	}
	out := GitHubCheck{AccountID: run.AccountID, RunID: run.ID, Pipeline: run.Pipeline, Source: *run.GitHub, Attempts: int(row.Attempts), CheckID: row.CheckID, LeaseToken: uuidString(row.LeaseToken)}
	b, err := s.q.GetCIGitHubBinding(ctx, s.pool, sqlc.GetCIGitHubBindingParams{AccountID: row.AccountID, ID: dbUUID(out.Source.BindingID)})
	if err != nil {
		return GitHubCheck{}, storeError(err)
	}
	if !b.Enabled || b.InstallationID != out.Source.InstallationID || b.RepositoryID != out.Source.RepositoryID || !strings.EqualFold(b.Repository, out.Source.Repository) {
		return GitHubCheck{}, errors.Join(ErrGitHubIgnored, s.FinishGitHubCheck(ctx, out, 0, "binding_disabled"))
	}
	return out, nil
}

func (s *PostgresStore) FinishGitHubCheck(ctx context.Context, claim GitHubCheck, checkID int64, reason string) error {
	if reason == "" && checkID <= 0 {
		return fmt.Errorf("CI check publication requires a remote identity")
	}
	status := "sent"
	if reason != "" {
		status = githubFailureState(claim.Attempts, reason)
	}
	now, err := s.q.CIExecutionClock(ctx, s.pool)
	if err != nil {
		return storeError(err)
	}
	count, err := s.q.FinishCIGitHubCheck(ctx, s.pool, sqlc.FinishCIGitHubCheckParams{AccountID: dbUUID(claim.AccountID), RunID: dbUUID(claim.RunID), LeaseToken: dbUUID(claim.LeaseToken), Status: status, CheckID: checkID, LastError: reason, NextAttemptAt: timestamp(githubRetry(claim.Attempts, now.Time))})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

var _ GitHubStore = (*PostgresStore)(nil)
