package ci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

var _ ProfileStore = (*PostgresStore)(nil)

func profileRow(row sqlc.CiExecutionProfile) (ExecutionProfile, error) {
	out := ExecutionProfile{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), Name: row.Name,
		RevisionID: uuidString(row.RevisionID), RevisionDigest: row.RevisionDigest, BindingID: uuidString(row.BindingID),
		BindingGeneration: row.BindingGeneration, State: row.State, CreatedAt: row.CreatedAt.Time}
	if err := json.Unmarshal(row.Templates, &out.Templates); err != nil {
		return ExecutionProfile{}, errors.New("decode CI execution profile")
	}
	return out, nil
}

func (s *PostgresStore) ExecutionProfile(ctx context.Context, account, id string) (ExecutionProfile, error) {
	row, err := s.q.GetCIExecutionProfile(ctx, s.pool, sqlc.GetCIExecutionProfileParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	return profileRow(row)
}

func (s *PostgresStore) ExecutionProfiles(ctx context.Context, account string) ([]ExecutionProfile, error) {
	rows, err := s.q.ListCIExecutionProfiles(ctx, s.pool, sqlc.ListCIExecutionProfilesParams{AccountID: dbUUID(account), MaxRows: api.CIMaxExecutionProfilesPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]ExecutionProfile, 0, len(rows))
	for _, row := range rows {
		profile, err := profileRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, profile)
	}
	return out, nil
}

func (s *PostgresStore) DisableExecutionProfile(ctx context.Context, account, id string) (ExecutionProfile, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return ExecutionProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.DisableCIExecutionProfile(ctx, tx, sqlc.DisableCIExecutionProfileParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	if err := s.q.IgnoreCIAutomationProfileEvents(ctx, tx, sqlc.IgnoreCIAutomationProfileEventsParams{AccountID: dbUUID(account), ProfileID: dbUUID(id)}); err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	return profileRow(row)
}

func sameProfileRequest(profile ExecutionProfile, req ExecutionProfileRequest) bool {
	return profile.RevisionID == req.RevisionID && profile.BindingID == req.BindingID && reflect.DeepEqual(profile.Templates, req.Templates)
}

func (s *PostgresStore) putExecutionProfile(ctx context.Context, account, name string, req ExecutionProfileRequest, binding GitHubBinding) (ExecutionProfile, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return ExecutionProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.jobsAccount(ctx, tx, account); err != nil {
		return ExecutionProfile{}, err
	}
	if err := s.jobsBinding(ctx, tx, binding); err != nil {
		return ExecutionProfile{}, err
	}
	existing, err := s.q.GetCIExecutionProfileByName(ctx, tx, sqlc.GetCIExecutionProfileByNameParams{AccountID: dbUUID(account), Name: name})
	if err == nil {
		profile, err := profileRow(existing)
		if err == nil && (!sameProfileRequest(profile, req) || profile.BindingGeneration != binding.Generation) {
			err = ErrConflict
		}
		return profile, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExecutionProfile{}, storeError(err)
	}
	count, err := s.q.CountCIExecutionProfiles(ctx, tx, dbUUID(account))
	if err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	if count >= api.CIMaxExecutionProfilesPerAccount {
		return ExecutionProfile{}, &LimitError{Resource: "execution_profiles", Limit: api.CIMaxExecutionProfilesPerAccount, Observed: int(count) + 1}
	}
	revision, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(account), ID: dbUUID(req.RevisionID)})
	if err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	snapshot, err := s.profileSnapshot(ctx, tx, account, req, binding, revision)
	if err != nil {
		return ExecutionProfile{}, err
	}
	templates, _ := json.Marshal(req.Templates)
	row, err := s.q.InsertCIExecutionProfile(ctx, tx, sqlc.InsertCIExecutionProfileParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account),
		Name: name, RevisionID: revision.ID, RevisionDigest: revision.Digest, BindingID: dbUUID(binding.ID), BindingGeneration: binding.Generation,
		Templates: templates, Snapshot: snapshot})
	if err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionProfile{}, storeError(err)
	}
	return profileRow(row)
}

func (s *PostgresStore) profileSnapshot(ctx context.Context, tx pgx.Tx, account string, req ExecutionProfileRequest, binding GitHubBinding, record sqlc.CiPipelineRevision) ([]byte, error) {
	revision, err := revisionRow(record)
	if err != nil {
		return nil, err
	}
	plan, _, err := Compile(revision.Definition)
	if err != nil {
		return nil, err
	}
	if plan.DefinitionDigest != record.Digest || plan.Repository != binding.Repository || len(plan.Jobs) != len(req.Templates) {
		return nil, ErrConflict
	}
	snapshot := JobsExecution{Binding: binding, Templates: make(map[string]JobsTemplate, len(req.Templates))}
	for _, job := range plan.Jobs {
		template, err := s.jobsTemplate(ctx, tx, account, req.Templates[job.Name], job)
		if err != nil {
			return nil, err
		}
		snapshot.Templates[job.Name] = template
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(raw) > api.CIProfileSnapshotMaxBytes {
		return nil, &LimitError{Resource: "profile_snapshot_bytes", Limit: api.CIProfileSnapshotMaxBytes, Observed: len(raw)}
	}
	return raw, nil
}

// The caller holds the account lock through insertion of execution intent.
// Disable and binding edits therefore serialize with new profile admissions.
func (s *PostgresStore) jobsProfile(ctx context.Context, tx pgx.Tx, account string, req JobsSubmitRequest, binding GitHubBinding) (*JobsExecution, error) {
	if req.ProfileID == "" {
		return nil, nil
	}
	record, err := s.q.GetCIExecutionProfile(ctx, tx, sqlc.GetCIExecutionProfileParams{AccountID: dbUUID(account), ID: dbUUID(req.ProfileID)})
	if err != nil {
		return nil, storeError(err)
	}
	profile, err := profileRow(record)
	if err != nil {
		return nil, err
	}
	input := ExecutionProfileRequest{RevisionID: req.RevisionID, BindingID: req.BindingID, Templates: req.Templates}
	var snapshot JobsExecution
	if json.Unmarshal(record.Snapshot, &snapshot) != nil {
		return nil, errors.New("decode CI profile image snapshot")
	}
	if profile.State != ProfileActive || !sameProfileRequest(profile, input) || profile.BindingGeneration != binding.Generation || !sameJobsBinding(snapshot.Binding, binding) {
		return nil, ErrConflict
	}
	return &snapshot, nil
}
