package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func progressAllowed(state runtimeState, cfg Config, cancelled bool) bool {
	return cfg.TransportEnabled && cfg.DispatchAllows(state.execution.AccountID) && !cancelled && !state.grant.SealedAt.Valid &&
		state.task.Status == "claimed" && state.task.InstanceState.String == "running" && !terminal(state.execution.Status) && len(state.attempt.Output) == 0
}

func (s *PostgresStore) AppendJobsRuntimeProgress(ctx context.Context, cfg Config, proof RuntimeProof, update ProgressUpdate) (ProgressReceipt, error) {
	state, cancelled, err := s.authorizedRuntime(ctx, proof, &cfg)
	if err != nil {
		return ProgressReceipt{}, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if !progressAllowed(state, cfg, cancelled) {
		return ProgressReceipt{}, ErrRuntimeAuthority
	}
	if update.Sequence < 1 {
		return ProgressReceipt{}, ErrBackendProtocol
	}
	if update.Sequence > api.CIJobsProgressMaxUpdates {
		return ProgressReceipt{}, ErrLimit
	}
	raw, err := json.Marshal(update)
	if err != nil || len(raw) > api.CIJobsProgressRequestMaxBytes {
		return ProgressReceipt{}, ErrBackendProtocol
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	before, err := decodeJobsProgress(state.attempt.Progress, state.manifest.Job)
	if err != nil {
		return ProgressReceipt{}, err
	}
	if update.Sequence == state.attempt.ProgressSequence && digest == state.attempt.ProgressDigest {
		return ProgressReceipt{Sequence: update.Sequence, NextOffset: len(before.Logs)}, storeError(state.tx.Commit(ctx))
	}
	if update.Sequence != state.attempt.ProgressSequence+1 {
		return ProgressReceipt{}, ErrConflict
	}
	next, err := appendJobsProgress(state.manifest.Job, before, update)
	if err != nil {
		return ProgressReceipt{}, err
	}
	raw, err = json.Marshal(next)
	if err != nil || len(raw) > api.CIJobsProgressSnapshotMaxBytes {
		return ProgressReceipt{}, ErrLimit
	}
	if err := s.q.SaveCIJobsProgress(ctx, state.tx, sqlc.SaveCIJobsProgressParams{AttemptID: dbUUID(proof.RunID), Progress: raw, ProgressSequence: update.Sequence, ProgressDigest: digest}); err != nil {
		return ProgressReceipt{}, storeError(err)
	}
	return ProgressReceipt{Sequence: update.Sequence, NextOffset: len(next.Logs)}, storeError(state.tx.Commit(ctx))
}

var _ RuntimeProgressStore = (*PostgresStore)(nil)
