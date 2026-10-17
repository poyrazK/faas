package ci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

var _ AutomationStore = (*PostgresStore)(nil)

func automationPolicyRow(row sqlc.CiAutomationPolicy) (AutomationPolicy, error) {
	out := AutomationPolicy{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), BindingID: uuidString(row.BindingID), BindingGeneration: row.BindingGeneration,
		AutomationRequest: AutomationRequest{ProfileID: uuidString(row.ProfileID), LatestOnly: row.LatestOnly}, Enabled: row.Enabled, Generation: row.Generation, CreatedAt: row.CreatedAt.Time}
	if err := json.Unmarshal(row.PushBranches, &out.PushBranches); err != nil {
		return AutomationPolicy{}, errors.New("decode CI automation policy")
	}
	if err := json.Unmarshal(row.PrBranches, &out.PullRequestBranches); err != nil {
		return AutomationPolicy{}, errors.New("decode CI PR automation policy")
	}
	if json.Unmarshal(row.IncludePaths, &out.IncludePaths) != nil || json.Unmarshal(row.ExcludePaths, &out.ExcludePaths) != nil {
		return AutomationPolicy{}, errors.New("decode CI automation path filters")
	}
	return out, nil
}

func (s *PostgresStore) AutomationPolicies(ctx context.Context, account string) ([]AutomationPolicy, error) {
	rows, err := s.q.ListCIAutomationPolicies(ctx, s.pool, sqlc.ListCIAutomationPoliciesParams{AccountID: dbUUID(account), MaxRows: api.CIGitHubMaxBindingsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]AutomationPolicy, 0, len(rows))
	for _, row := range rows {
		policy, err := automationPolicyRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	return out, nil
}

func (s *PostgresStore) putAutomationPolicy(ctx context.Context, account string, binding GitHubBinding, req AutomationRequest) (AutomationPolicy, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return AutomationPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.validateAutomationProfile(ctx, tx, account, binding, req.ProfileID); err != nil {
		return AutomationPolicy{}, err
	}
	existing, err := s.q.GetCIAutomationPolicy(ctx, tx, sqlc.GetCIAutomationPolicyParams{AccountID: dbUUID(account), BindingID: dbUUID(binding.ID)})
	if err == nil {
		policy, err := automationPolicyRow(existing)
		if err != nil {
			return AutomationPolicy{}, err
		}
		if policy.Enabled && policy.ProfileID == req.ProfileID && policy.BindingGeneration == binding.Generation && policy.LatestOnly == req.LatestOnly &&
			slices.Equal(policy.PushBranches, req.PushBranches) && slices.Equal(policy.PullRequestBranches, req.PullRequestBranches) &&
			slices.Equal(policy.IncludePaths, req.IncludePaths) && slices.Equal(policy.ExcludePaths, req.ExcludePaths) {
			return policy, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AutomationPolicy{}, storeError(err)
	}
	branches, _ := json.Marshal(nonNilBranches(req.PushBranches))
	prs, _ := json.Marshal(nonNilBranches(req.PullRequestBranches))
	includes, _ := json.Marshal(nonNilBranches(req.IncludePaths))
	excludes, _ := json.Marshal(nonNilBranches(req.ExcludePaths))
	row, err := s.q.UpsertCIAutomationPolicy(ctx, tx, sqlc.UpsertCIAutomationPolicyParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account),
		BindingID: dbUUID(binding.ID), BindingGeneration: binding.Generation, ProfileID: dbUUID(req.ProfileID), PushBranches: branches, PrBranches: prs, LatestOnly: req.LatestOnly, IncludePaths: includes, ExcludePaths: excludes})
	if err != nil {
		return AutomationPolicy{}, storeError(err)
	}
	if err := s.ignoreAutomationBinding(ctx, tx, account, binding.ID, "policy_disabled"); err != nil {
		return AutomationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AutomationPolicy{}, storeError(err)
	}
	return automationPolicyRow(row)
}

func (s *PostgresStore) validateAutomationProfile(ctx context.Context, tx pgx.Tx, account string, binding GitHubBinding, id string) error {
	if err := s.jobsAccount(ctx, tx, account); err != nil {
		return err
	}
	if err := s.jobsBinding(ctx, tx, binding); err != nil {
		return err
	}
	row, err := s.q.GetCIExecutionProfile(ctx, tx, sqlc.GetCIExecutionProfileParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return storeError(err)
	}
	profile, err := profileRow(row)
	if err != nil {
		return err
	}
	req := JobsSubmitRequest{ProfileID: id, BindingID: binding.ID, SubmitRequest: SubmitRequest{RevisionID: binding.RevisionID}, Templates: profile.Templates}
	saved, err := s.jobsProfile(ctx, tx, account, req, binding)
	if err != nil {
		return err
	}
	revision, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(account), ID: dbUUID(binding.RevisionID)})
	if err != nil {
		return storeError(err)
	}
	raw, err := s.profileSnapshot(ctx, tx, account, ExecutionProfileRequest{RevisionID: binding.RevisionID, BindingID: binding.ID, Templates: profile.Templates}, binding, revision)
	if err != nil {
		return err
	}
	var current JobsExecution
	if json.Unmarshal(raw, &current) != nil || !reflect.DeepEqual(saved.Templates, current.Templates) {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) ignoreAutomationBinding(ctx context.Context, tx pgx.Tx, account, binding, reason string) error {
	return storeError(s.q.IgnoreCIAutomationBindingEvents(ctx, tx, sqlc.IgnoreCIAutomationBindingEventsParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), LastError: reason}))
}

func (s *PostgresStore) DisableAutomationPolicy(ctx context.Context, account, binding string) (AutomationPolicy, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return AutomationPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.DisableCIAutomationPolicy(ctx, tx, sqlc.DisableCIAutomationPolicyParams{AccountID: dbUUID(account), BindingID: dbUUID(binding)})
	if err != nil {
		return AutomationPolicy{}, storeError(err)
	}
	if err := s.ignoreAutomationBinding(ctx, tx, account, binding, "policy_disabled"); err != nil {
		return AutomationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AutomationPolicy{}, storeError(err)
	}
	return automationPolicyRow(row)
}
