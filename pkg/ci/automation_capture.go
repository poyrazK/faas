package ci

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

// This ingress seam belongs only after the App HMAC check. A logical source
// event already recorded in either journal can never change admission mode.
func (s *PostgresStore) RecordGitHubAutomationDelivery(ctx context.Context, account, delivery string, event GitHubEvent) error {
	return s.recordGitHubDelivery(ctx, account, delivery, event, true)
}

func (s *PostgresStore) gitHubEventRecorded(ctx context.Context, tx pgx.Tx, account, binding, key string) (bool, error) {
	_, err := s.q.GetCIGitHubTriggerByEvent(ctx, tx, sqlc.GetCIGitHubTriggerByEventParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), EventKey: key})
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, storeError(err)
	}
	_, err = s.q.GetCIAutomationEventByKey(ctx, tx, sqlc.GetCIAutomationEventByKeyParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), EventKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, storeError(err)
}

func (s *PostgresStore) captureAutomation(ctx context.Context, tx pgx.Tx, binding GitHubBinding, event GitHubEvent, delivery string) (bool, error) {
	record, err := s.q.GetCIAutomationPolicy(ctx, tx, sqlc.GetCIAutomationPolicyParams{AccountID: dbUUID(binding.AccountID), BindingID: dbUUID(binding.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storeError(err)
	}
	policy, err := automationPolicyRow(record)
	if err != nil {
		return false, err
	}
	if !policy.Enabled || policy.BindingGeneration != binding.Generation || !automationPolicyMatches(policy, event) {
		return false, nil
	}
	profile, err := s.q.GetCIExecutionProfile(ctx, tx, sqlc.GetCIExecutionProfileParams{AccountID: dbUUID(binding.AccountID), ID: dbUUID(policy.ProfileID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storeError(err)
	}
	if profile.State != ProfileActive || uuidString(profile.BindingID) != binding.ID || profile.BindingGeneration != binding.Generation || uuidString(profile.RevisionID) != binding.RevisionID {
		return false, nil
	}
	row := AutomationEvent{ID: uuid.NewString(), AccountID: binding.AccountID, Policy: policy, Binding: binding,
		Source: GitHubSource{GitHubEvent: event, BindingID: binding.ID, Generation: binding.Generation, DeliveryID: delivery}}
	raw, err := json.Marshal(row)
	if err != nil {
		return false, err
	}
	if len(raw) > api.CIGitHubSnapshotMaxBytes {
		return false, &LimitError{Resource: "automation_snapshot_bytes", Limit: api.CIGitHubSnapshotMaxBytes, Observed: len(raw)}
	}
	err = s.q.InsertCIAutomationEvent(ctx, tx, sqlc.InsertCIAutomationEventParams{ID: dbUUID(row.ID), AccountID: dbUUID(row.AccountID), BindingID: dbUUID(binding.ID),
		PolicyID: dbUUID(policy.ID), PolicyGeneration: policy.Generation, ProfileID: dbUUID(policy.ProfileID), EventKey: event.Key(), Snapshot: raw})
	return err == nil, storeError(err)
}
