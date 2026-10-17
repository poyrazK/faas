package ci

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

var _ RuntimeStore = (*PostgresStore)(nil)

type runtimeState struct {
	tx        pgx.Tx
	execution Execution
	attempt   sqlc.CiJobAttempt
	grant     sqlc.CiJobRuntime
	task      sqlc.CIJobsRuntimeTaskRow
	manifest  JobsManifest
	now       time.Time
}

func runtimeCapability(run, instance, lease string) string {
	mac := hmac.New(sha256.New, []byte(lease))
	_, _ = mac.Write([]byte("gregale:ci-runtime:v1\x00" + run + "\x00" + instance))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *PostgresStore) runtimeState(ctx context.Context, runID string) (runtimeState, error) {
	owner, err := s.q.CIJobsRuntimeOwner(ctx, s.pool, dbUUID(runID))
	if err != nil {
		return runtimeState{}, ErrRuntimeAuthority
	}
	tx, err := s.accountTx(ctx, uuidString(owner.AccountID))
	if err != nil {
		return runtimeState{}, err
	}
	failed := func(err error) (runtimeState, error) { _ = tx.Rollback(ctx); return runtimeState{}, err }
	record, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: owner.AccountID, ID: owner.ExecutionID})
	if err != nil {
		return failed(ErrRuntimeAuthority)
	}
	row, err := executionRow(record)
	if err != nil {
		return failed(err)
	}
	attempt, err := s.q.LockCIJobsAttempt(ctx, tx, sqlc.LockCIJobsAttemptParams{AccountID: owner.AccountID, ExecutionID: owner.ExecutionID, AttemptID: dbUUID(runID)})
	if err != nil || !attempt.RunID.Valid {
		return failed(ErrRuntimeAuthority)
	}
	key := AttemptKey{AccountID: uuidString(owner.AccountID), ExecutionID: uuidString(owner.ExecutionID), AttemptID: runID, JobName: attempt.JobName, Fence: row.Fence}
	req, _, err := jobsAttemptFor(row.Execution, key)
	if err != nil {
		return failed(ErrRuntimeAuthority)
	}
	var source SourceArchive
	if json.Unmarshal(attempt.Source, &source) != nil {
		return failed(ErrRuntimeAuthority)
	}
	inputs, err := resolveArtifactInputs(row.Execution, req.Job)
	if err != nil {
		return failed(err)
	}
	raw, err := jobsManifest(req, source, inputs...)
	if err != nil {
		return failed(err)
	}
	var manifest JobsManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return failed(ErrRuntimeAuthority)
	}
	task, err := s.q.CIJobsRuntimeTask(ctx, tx, dbUUID(runID))
	if err != nil {
		return failed(ErrRuntimeAuthority)
	}
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return failed(err)
	}
	return runtimeState{tx: tx, execution: row.Execution, attempt: attempt, task: task, manifest: manifest, now: now}, nil
}

func (s *PostgresStore) runtimePolicy(ctx context.Context, state runtimeState) bool {
	if state.execution.CancelRequested || state.attempt.CancelRequested || !state.now.Before(state.manifest.Deadline) {
		return false
	}
	if s.jobsAccount(ctx, state.tx, state.execution.AccountID) != nil || s.jobsBinding(ctx, state.tx, state.execution.Compute.Binding) != nil {
		return false
	}
	if !s.runtimeRunMatches(ctx, state) {
		return false
	}
	template := state.execution.Compute.Templates[state.attempt.JobName]
	current, err := s.jobsTemplate(ctx, state.tx, state.execution.AccountID, template.ID, state.manifest.Job)
	return err == nil && current == template
}

func runtimeLiveTask(state runtimeState) bool {
	return state.task.Attempt == 1 && state.task.BoundInstance && state.task.InstanceID.Valid && state.task.InstanceState.Valid &&
		(state.task.InstanceState.String == "running" || state.task.InstanceState.String == "cold_booting")
}

// IssueJobsRuntime is a host-only seam for future scheduler wiring. The result
// is derived from the current task lease and is never persisted in plaintext.
func (s *PostgresStore) IssueJobsRuntime(ctx context.Context, cfg Config, host RuntimeHostProof) (RuntimeProof, error) {
	if !ValidID(host.RunID) || !ValidID(host.InstanceID) || !ValidID(host.LeaseToken) {
		return RuntimeProof{}, ErrRuntimeAuthority
	}
	state, err := s.runtimeState(ctx, host.RunID)
	if err != nil {
		return RuntimeProof{}, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if !cfg.TransportEnabled || !cfg.DispatchAllows(state.execution.AccountID) || !runtimeLiveTask(state) || state.task.Status != "claimed" ||
		uuidString(state.task.InstanceID) != host.InstanceID || !state.task.LeaseToken.Valid || uuidString(state.task.LeaseToken) != host.LeaseToken ||
		!state.task.LeaseExpiresAt.Valid || !state.now.Before(state.task.LeaseExpiresAt.Time) || !s.runtimePolicy(ctx, state) {
		return RuntimeProof{}, ErrRuntimeAuthority
	}
	proof := RuntimeProof{RunID: host.RunID, InstanceID: host.InstanceID, Capability: runtimeCapability(host.RunID, host.InstanceID, host.LeaseToken)}
	digest := sha256.Sum256([]byte(proof.Capability))
	if err := s.q.InsertCIJobsRuntime(ctx, state.tx, sqlc.InsertCIJobsRuntimeParams{AttemptID: dbUUID(host.RunID), InstanceID: dbUUID(host.InstanceID), CapabilityHash: digest[:], ExpiresAt: timestamp(runtimeExpiry(state.manifest.Deadline)), CreatedAt: timestamp(state.now)}); err != nil {
		return RuntimeProof{}, storeError(err)
	}
	grant, err := s.q.ReadCIJobsRuntime(ctx, state.tx, dbUUID(host.RunID))
	if err != nil || uuidString(grant.InstanceID) != host.InstanceID || subtle.ConstantTimeCompare(grant.CapabilityHash, digest[:]) != 1 || grant.SealedAt.Valid || grant.CancelledAt.Valid {
		return RuntimeProof{}, ErrRuntimeAuthority
	}
	return proof, storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) authorizedRuntime(ctx context.Context, proof RuntimeProof, cfg *Config) (runtimeState, bool, error) {
	if !ValidRuntimeProof(proof) {
		return runtimeState{}, false, ErrRuntimeAuthority
	}
	state, err := s.runtimeState(ctx, proof.RunID)
	if err != nil {
		return runtimeState{}, false, err
	}
	failed := func(err error) (runtimeState, bool, error) {
		_ = state.tx.Rollback(ctx)
		return runtimeState{}, false, err
	}
	state.grant, err = s.q.ReadCIJobsRuntime(ctx, state.tx, dbUUID(proof.RunID))
	digest := sha256.Sum256([]byte(proof.Capability))
	if err != nil || !runtimeLiveTask(state) || uuidString(state.task.InstanceID) != proof.InstanceID || uuidString(state.grant.InstanceID) != proof.InstanceID ||
		subtle.ConstantTimeCompare(state.grant.CapabilityHash, digest[:]) != 1 || !state.now.Before(state.grant.ExpiresAt.Time) {
		return failed(ErrRuntimeAuthority)
	}
	leased := state.task.Status == "claimed" && state.task.LeaseToken.Valid && state.task.LeaseExpiresAt.Valid && state.now.Before(state.task.LeaseExpiresAt.Time)
	if leased {
		expected := runtimeCapability(proof.RunID, proof.InstanceID, uuidString(state.task.LeaseToken))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(proof.Capability)) != 1 {
			return failed(ErrRuntimeAuthority)
		}
	}
	if !leased && state.task.Status != "cancelled" {
		return failed(ErrRuntimeAuthority)
	}
	policy := s.runtimePolicy(ctx, state)
	if cfg != nil && !cfg.DispatchAllows(state.execution.AccountID) {
		policy = false
	}
	cancelled := !policy || state.task.Status == "cancelled" || state.grant.CancelledAt.Valid
	if cancelled {
		at := state.now
		if state.grant.CancelledAt.Valid {
			at = state.grant.CancelledAt.Time
		}
		if state.task.Status == "cancelled" && state.task.FinishedAt.Valid && state.task.FinishedAt.Time.Before(at) {
			at = state.task.FinishedAt.Time
		}
		if state.manifest.Deadline.Before(at) {
			at = state.manifest.Deadline
		}
		if !state.now.Before(runtimeExpiry(at)) {
			return failed(ErrRuntimeAuthority)
		}
		if err := s.q.CancelCIJobsRuntime(ctx, state.tx, sqlc.CancelCIJobsRuntimeParams{AttemptID: dbUUID(proof.RunID), CancelledAt: timestamp(at)}); err != nil {
			return failed(storeError(err))
		}
	}
	return state, cancelled, nil
}

func (s *PostgresStore) AuthorizeJobsRuntime(ctx context.Context, cfg Config, proof RuntimeProof, action string) (RuntimeView, error) {
	state, cancelled, err := s.authorizedRuntime(ctx, proof, &cfg)
	if err != nil {
		return RuntimeView{}, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if action != RuntimeSource && action != RuntimeControl && action != RuntimeUpload && action != RuntimeProgress && action != RuntimeInput {
		return RuntimeView{}, ErrRuntimeAuthority
	}
	// A grant may be staged before boot, but source cannot leave until schedd
	// has verified the held-start acknowledgement and published VM identity.
	if (action == RuntimeSource || action == RuntimeInput) && (!cfg.TransportEnabled || cancelled || state.grant.SealedAt.Valid || state.task.InstanceState.String != "running") {
		return RuntimeView{}, ErrRuntimeAuthority
	}
	progress := progressAllowed(state, cfg, cancelled)
	if action == RuntimeProgress && !progress {
		return RuntimeView{}, ErrRuntimeAuthority
	}
	view := RuntimeView{Manifest: state.manifest, CancelRequested: cancelled, Sealed: state.grant.SealedAt.Valid, ProgressEnabled: progress}
	return view, storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) BeginJobsRuntimeTransfer(ctx context.Context, proof RuntimeProof) (string, error) {
	state, _, err := s.authorizedRuntime(ctx, proof, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if state.grant.SealedAt.Valid {
		return "", ErrConflict
	}
	if state.grant.TransferUntil.Valid && state.now.Before(state.grant.TransferUntil.Time) {
		return "", ErrLeaseBusy
	}
	token := uuid.NewString()
	// The HTTP I/O deadline is shorter than this reservation, including when a
	// process dies without releasing it. One source/upload per attempt fleet-wide.
	until := state.now.Add(2 * time.Duration(api.CIJobsTransferSeconds) * time.Second)
	if err := s.q.BeginCIJobsRuntimeTransfer(ctx, state.tx, sqlc.BeginCIJobsRuntimeTransferParams{AttemptID: dbUUID(proof.RunID), TransferToken: dbUUID(token), TransferUntil: timestamp(until)}); err != nil {
		return "", storeError(err)
	}
	return token, storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) EndJobsRuntimeTransfer(ctx context.Context, proof RuntimeProof, token string) error {
	if !ValidID(token) || !ValidRuntimeProof(proof) {
		return ErrRuntimeAuthority
	}
	return storeError(s.q.EndCIJobsRuntimeTransfer(ctx, s.pool, sqlc.EndCIJobsRuntimeTransferParams{AttemptID: dbUUID(proof.RunID), InstanceID: dbUUID(proof.InstanceID), TransferToken: dbUUID(token)}))
}

func (s *PostgresStore) ReserveJobsRuntimeObject(ctx context.Context, proof RuntimeProof, object RuntimeObject) (string, bool, error) {
	state, _, err := s.authorizedRuntime(ctx, proof, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if state.grant.SealedAt.Valid {
		return "", false, ErrConflict
	}
	key, err := runtimeObjectKey(state.manifest, object)
	if err != nil {
		return "", false, err
	}
	rows, err := s.q.ListCIJobsRuntimeObjects(ctx, state.tx, dbUUID(proof.RunID))
	if err != nil {
		return "", false, storeError(err)
	}
	count, total := 0, int64(0)
	for _, row := range rows {
		if row.Name == object.Name {
			if row.Digest != object.Digest || row.Bytes != object.Bytes {
				return "", false, ErrConflict
			}
			return key, row.Retained, storeError(state.tx.Commit(ctx))
		}
		if strings.HasPrefix(row.Name, "artifacts/") {
			count++
			total += row.Bytes
		}
	}
	if object.Name != "logs.bin" && (count >= api.CIMaxArtifactsPerJob || total+object.Bytes > api.CIArtifactsMaxBytesPerJob) {
		return "", false, ErrLimit
	}
	if err := s.q.ReserveCIJobsRuntimeObject(ctx, state.tx, sqlc.ReserveCIJobsRuntimeObjectParams{AttemptID: dbUUID(proof.RunID), Name: object.Name, Digest: object.Digest, Bytes: object.Bytes}); err != nil {
		return "", false, storeError(err)
	}
	return key, false, storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) CommitJobsRuntimeObject(ctx context.Context, proof RuntimeProof, object RuntimeObject) error {
	state, _, err := s.authorizedRuntime(ctx, proof, nil)
	if err != nil {
		return err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if state.grant.SealedAt.Valid {
		return ErrConflict
	}
	rows, err := s.q.ListCIJobsRuntimeObjects(ctx, state.tx, dbUUID(proof.RunID))
	if err != nil {
		return storeError(err)
	}
	for _, row := range rows {
		if row.Name == object.Name && row.Digest == object.Digest && row.Bytes == object.Bytes {
			if err := s.q.RetainCIJobsRuntimeObject(ctx, state.tx, sqlc.RetainCIJobsRuntimeObjectParams{AttemptID: dbUUID(proof.RunID), Name: object.Name}); err != nil {
				return storeError(err)
			}
			return storeError(state.tx.Commit(ctx))
		}
	}
	return ErrConflict
}

func (s *PostgresStore) SealJobsRuntime(ctx context.Context, proof RuntimeProof, raw []byte) error {
	result, err := DecodeJobsResult(raw)
	if err != nil {
		return err
	}
	state, _, err := s.authorizedRuntime(ctx, proof, nil)
	if err != nil {
		return err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if result.Version != 1 || result.AccountID != state.manifest.AccountID || result.ExecutionID != state.manifest.ExecutionID ||
		result.AttemptID != proof.RunID || result.CommitSHA != state.manifest.CommitSHA || result.Logs.Bytes < 0 || result.Logs.Bytes > api.CILogMaxBytesPerAttempt {
		return ErrBackendProtocol
	}
	observation := Observation{Status: result.Status, Steps: result.Steps, ExitCode: result.ExitCode, MoreLogs: result.LogTruncated, Logs: make([]byte, int(result.Logs.Bytes))}
	wanted := map[string]RuntimeObject{"logs.bin": {Name: "logs.bin", Digest: result.Logs.Digest, Bytes: result.Logs.Bytes}}
	if result.Logs.Key != state.manifest.OutputPrefix+"logs.bin" {
		return ErrBackendProtocol
	}
	for _, artifact := range result.Artifacts {
		name := "artifacts/" + artifact.Path
		if artifact.Key != state.manifest.OutputPrefix+name {
			return ErrBackendProtocol
		}
		wanted[name] = RuntimeObject{Name: name, Digest: artifact.Digest, Bytes: artifact.Bytes}
		observation.Artifacts = append(observation.Artifacts, artifact.ArtifactRef)
	}
	if err := validateJobsOutput(state.manifest.Job, jobsTask{Status: result.Status, ExitCode: result.ExitCode, Stopped: true}, observation); err != nil {
		return err
	}
	if len(state.attempt.Progress) > 0 {
		progress, err := decodeJobsProgress(state.attempt.Progress, state.manifest.Job)
		if err != nil {
			return err
		}
		if int64(len(progress.Logs)) > result.Logs.Bytes || progress.LogTruncated && !result.LogTruncated {
			return ErrBackendProtocol
		}
		if err := validateSteps(progress.Steps, observation); err != nil {
			return err
		}
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if state.grant.SealedAt.Valid {
		var existing JobsResultManifest
		if json.Unmarshal(state.grant.Result, &existing) != nil {
			return ErrConflict
		}
		existingRaw, _ := json.Marshal(existing)
		if !bytes.Equal(existingRaw, canonical) {
			return ErrConflict
		}
		return storeError(state.tx.Commit(ctx))
	}
	objects, err := s.q.ListCIJobsRuntimeObjects(ctx, state.tx, dbUUID(proof.RunID))
	if err != nil {
		return storeError(err)
	}
	if len(objects) != len(wanted) {
		return ErrConflict
	}
	for _, object := range objects {
		claim, ok := wanted[object.Name]
		if !ok || !object.Retained || claim.Bytes != object.Bytes || claim.Digest != object.Digest {
			return ErrConflict
		}
	}
	if err := s.q.SealCIJobsRuntime(ctx, state.tx, sqlc.SealCIJobsRuntimeParams{AttemptID: dbUUID(proof.RunID), Result: canonical, SealedAt: timestamp(state.now)}); err != nil {
		return storeError(err)
	}
	return storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) SealedJobsResult(ctx context.Context, req JobsSourceRequest) ([]byte, error) {
	raw, err := s.q.ReadSealedCIJobsResult(ctx, s.pool, sqlc.ReadSealedCIJobsResultParams{AccountID: dbUUID(req.Key.AccountID), ExecutionID: dbUUID(req.Key.ExecutionID), AttemptID: dbUUID(req.Key.AttemptID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRuntimeAuthority
	}
	return raw, storeError(err)
}
