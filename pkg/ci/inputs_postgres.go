package ci

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

// RuntimeInputStore extends the optional runtime transport without widening
// RuntimeStore or accepting client-selected object keys.
type RuntimeInputStore interface {
	JobsRuntimeInput(context.Context, Config, RuntimeProof, string) (JobsArtifact, error)
}

func (s *PostgresStore) dispatchArtifactInputs(ctx context.Context, tx pgx.Tx, req StartRequest) ([]JobsInput, error) {
	if len(req.Job.Inputs) == 0 {
		return nil, nil
	}
	saved, err := s.q.GetCIExecution(ctx, tx, sqlc.GetCIExecutionParams{AccountID: dbUUID(req.Key.AccountID), ID: dbUUID(req.Key.ExecutionID)})
	if err != nil {
		return nil, storeError(err)
	}
	row, err := executionRow(saved)
	if err != nil {
		return nil, err
	}
	inputs, err := resolveArtifactInputs(row.Execution, req.Job)
	if err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if _, err := s.verifiedInput(ctx, tx, req.Key.AccountID, req.Key.ExecutionID, input); err != nil {
			return nil, err
		}
	}
	return inputs, nil
}

func (s *PostgresStore) verifiedInput(ctx context.Context, db sqlc.DBTX, account, execution string, input JobsInput) (JobsArtifact, error) {
	attempt, err := s.q.CIStorageAttempt(ctx, db, sqlc.CIStorageAttemptParams{AccountID: dbUUID(account), ExecutionID: dbUUID(execution), AttemptID: dbUUID(input.AttemptID)})
	if err != nil {
		return JobsArtifact{}, inputStoreError(err)
	}
	if attempt.JobName != input.Job || !attempt.RunID.Valid || uuidString(attempt.RunID) != input.AttemptID {
		return JobsArtifact{}, ErrArtifactInput
	}
	grant, err := s.q.ReadCIJobsRuntime(ctx, db, attempt.AttemptID)
	if err != nil {
		return JobsArtifact{}, inputStoreError(err)
	}
	task, err := s.q.ReadCIJobsTask(ctx, db, attempt.RunID)
	if err != nil {
		return JobsArtifact{}, inputStoreError(err)
	}
	if !grant.SealedAt.Valid || !task.Stopped || task.Status != StatusSucceeded || task.Attempt != 1 {
		return JobsArtifact{}, ErrArtifactInput
	}
	var output Observation
	if json.Unmarshal(attempt.Output, &output) != nil || output.Status != StatusSucceeded || output.OutputIncomplete {
		return JobsArtifact{}, ErrArtifactInput
	}
	wanted := ArtifactRef{Path: input.Path, Digest: input.Digest, Bytes: input.Bytes}
	found := false
	for _, artifact := range output.Artifacts {
		found = found || artifact == wanted
	}
	if !found {
		return JobsArtifact{}, ErrArtifactInput
	}
	key := jobsObjectPrefix(AttemptKey{AccountID: account, ExecutionID: execution, AttemptID: input.AttemptID}) + "output/artifacts/" + input.Path
	sealed, err := DecodeJobsResult(grant.Result)
	if err != nil || sealed.Version != 1 || sealed.Status != StatusSucceeded || sealed.AccountID != account || sealed.ExecutionID != execution || sealed.AttemptID != input.AttemptID {
		return JobsArtifact{}, ErrArtifactInput
	}
	found = false
	for _, artifact := range sealed.Artifacts {
		found = found || artifact.ArtifactRef == wanted && artifact.Key == key
	}
	if !found {
		return JobsArtifact{}, ErrArtifactInput
	}
	stored, err := s.q.ReadCIManagedObject(ctx, db, key)
	if err != nil {
		return JobsArtifact{}, inputStoreError(err)
	}
	if stored.State != "ready" || stored.Kind != "artifact" || uuidString(stored.AccountID) != account || uuidString(stored.ExecutionID) != execution || uuidString(stored.AttemptID) != input.AttemptID || stored.Digest != input.Digest || stored.Bytes != input.Bytes {
		return JobsArtifact{}, ErrArtifactInput
	}
	return JobsArtifact{ArtifactRef: wanted, Key: key}, nil
}

func inputStoreError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactInput
	}
	return storeError(err)
}

func (s *PostgresStore) JobsRuntimeInput(ctx context.Context, cfg Config, proof RuntimeProof, name string) (JobsArtifact, error) {
	if !ValidName(name) {
		return JobsArtifact{}, ErrRuntimeAuthority
	}
	state, cancelled, err := s.authorizedRuntime(ctx, proof, &cfg)
	if err != nil {
		return JobsArtifact{}, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if !progressAllowed(state, cfg, cancelled) {
		return JobsArtifact{}, ErrRuntimeAuthority
	}
	for _, input := range state.manifest.Inputs {
		if input.Name != name {
			continue
		}
		artifact, err := s.verifiedInput(ctx, state.tx, state.manifest.AccountID, state.manifest.ExecutionID, input)
		if err != nil {
			return JobsArtifact{}, err
		}
		return artifact, storeError(state.tx.Commit(ctx))
	}
	return JobsArtifact{}, ErrRuntimeAuthority
}

var _ RuntimeInputStore = (*PostgresStore)(nil)
