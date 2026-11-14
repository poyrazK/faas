package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotPrimeHandoffStore retains the exact activation message with the
// terminal capture outcome. It never writes imaged's snapshot or traffic rows.
// Prime's separate lock serializes allocation across scheduler processes.
type SnapshotPrimeHandoffStore interface {
	AcquireSnapshotPrimeLock(context.Context, string) (func(context.Context), error)
	ListSnapshotPrimeInstances(context.Context, string, string, time.Time) ([]Instance, error)
	CompleteSnapshotPrimeHandoff(context.Context, Instance, string, string, json.RawMessage) error
	SnapshotPrimeHandoff(context.Context, string, string, string) (SnapshotPrimeHandoff, error)
}

type SnapshotPrimeHandoff struct {
	Channel string
	Payload json.RawMessage
	Fence   RuntimeAppConfigFence
}

type retainedSnapshotPrimeHandoff struct {
	Attempt        snapshotPrimeAttempt
	Handoff        SnapshotPrimeHandoff
	WakeID, NodeID string
	StartedAt      time.Time
}

// HasSnapshotPrimeAttempt distinguishes Prime's capture from ordinary parking.
func HasSnapshotPrimeAttempt(ctx context.Context) bool {
	_, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	return ok
}

func (m *MemStore) AcquireSnapshotPrimeLock(ctx context.Context, id string) (func(context.Context), error) {
	if id == "" {
		return nil, ErrInvalidArgument
	}
	return m.AcquireDeploymentActivationLock(ctx, "snapshot-prime:"+snapshotPrimeLockID(id))
}

func (s *PgStore) AcquireSnapshotPrimeLock(ctx context.Context, id string) (func(context.Context), error) {
	if id == "" || s == nil || s.pool == nil {
		return nil, ErrInvalidArgument
	}
	return s.acquireSessionAdvisoryLock(ctx, sessionAdvisoryLock{
		what: "snapshot prime lock", keyArg: snapshotPrimeLockID(id), retryWait: 50 * time.Millisecond,
		tryQuery: func(ctx context.Context, q sqlc.DBTX, key string) (bool, error) {
			return sqlc.New().TrySnapshotPrimeLock(ctx, q, key)
		},
		unlockQuery: func(ctx context.Context, q sqlc.DBTX, key string) (bool, error) {
			return sqlc.New().UnlockSnapshotPrime(ctx, q, key)
		},
	})
}

func snapshotPrimeLockID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}

func retainSnapshotPrimeHandoff(ctx context.Context, expected, actual Instance, next, channel string, payload json.RawMessage, fence RuntimeAppConfigFence) ([]byte, error) {
	attempt, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	if !ok {
		return nil, ErrInvalidArgument
	}
	if !attempt.StartedAt.IsZero() && actual.StartedAt.Before(attempt.StartedAt) {
		return nil, ErrSnapshotPrimeStale
	}
	if actual.ID != expected.ID || actual.AppID != attempt.AppID || actual.DeploymentID != attempt.DeploymentID ||
		actual.NodeID != expected.NodeID || actual.WakeID != expected.WakeID || !actual.StartedAt.Equal(expected.StartedAt) ||
		actual.State != expected.State || fence.SecretFence.DeploymentID != actual.DeploymentID {
		return nil, ErrConflict
	}
	var fields struct {
		DeploymentID     string    `json:"deployment_id"`
		SourceInstanceID string    `json:"source_instance_id"`
		SourceStartedAt  time.Time `json:"source_started_at"`
		NodeID           string    `json:"node_id"`
		StorageKey       string    `json:"storage_key"`
		InstanceID       string    `json:"instance_id"`
		NoSnapshotReason string    `json:"no_snapshot_reason"`
	}
	if json.Unmarshal(payload, &fields) != nil || fields.DeploymentID != actual.DeploymentID {
		return nil, ErrInvalidArgument
	}
	switch channel {
	case db.NotifySnapshotWritten:
		if actual.Mode == string(InstanceModeWorker) || next != string(StateParked) || actual.State != string(StateSnapshotting) || fields.SourceInstanceID != actual.ID ||
			!fields.SourceStartedAt.Equal(actual.StartedAt) || fields.NodeID != actual.NodeID || fields.StorageKey == "" {
			return nil, ErrInvalidArgument
		}
	case db.NotifyDeploymentReady:
		if actual.Mode == string(InstanceModeWorker) || next != string(StateStopped) || actual.State != string(StateRunning) || fields.InstanceID != actual.ID || fields.NoSnapshotReason != "ephemeral_secret" {
			return nil, ErrInvalidArgument
		}
	default:
		return nil, ErrInvalidArgument
	}
	return json.Marshal(retainedSnapshotPrimeHandoff{attempt, SnapshotPrimeHandoff{channel, payload, fence}, actual.WakeID, actual.NodeID, actual.StartedAt})
}

func decodeSnapshotPrimeHandoff(ctx context.Context, raw []byte, actual Instance) (SnapshotPrimeHandoff, error) {
	var value retainedSnapshotPrimeHandoff
	if err := json.Unmarshal(raw, &value); err != nil {
		return SnapshotPrimeHandoff{}, err
	}
	attempt, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	expected := value.Attempt
	if !ok || attempt.AppID != expected.AppID || attempt.DeploymentID != expected.DeploymentID || attempt.RootfsKey != expected.RootfsKey || attempt.RootfsPath != expected.RootfsPath ||
		attempt.Stage != expected.Stage || attempt.Status != expected.Status || !attempt.StartedAt.Equal(expected.StartedAt) ||
		actual.WakeID != value.WakeID || actual.NodeID != value.NodeID || !actual.StartedAt.Equal(value.StartedAt) {
		return SnapshotPrimeHandoff{}, ErrNotFound
	}
	return value.Handoff, nil
}

func (m *MemStore) CompleteSnapshotPrimeHandoff(ctx context.Context, expected Instance, next, channel string, payload json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !HasSnapshotPrimeAttempt(ctx) {
		return ErrInvalidArgument
	}
	if err := m.checkSnapshotPrimeAttemptLocked(ctx, expected.AppID, expected.DeploymentID); err != nil {
		return err
	}
	i := m.instances[expected.ID]
	proof, ok := m.runtimeInstanceConfigProofs[i.ID]
	if !ok || proof.WakeID != i.WakeID || proof.NodeID != i.NodeID {
		return ErrConflict
	}
	raw, err := retainSnapshotPrimeHandoff(ctx, expected, i, next, channel, payload, proof.Fence)
	if err != nil {
		return err
	}
	after := i
	after.State = next
	if next == string(StateParked) {
		after.ParkedAt = time.Now().UTC()
	} else {
		now := time.Now().UTC()
		after.TerminalAt = &now
	}
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, i, after); err != nil {
		return err
	}
	if err := m.requireInstanceLayerArtifactsLocked(i.DeploymentID, next); err != nil {
		return err
	}
	if err := validateWorkerInstanceMutation(i, next, i.Mode); err != nil {
		return err
	}
	proof.PrimeHandoff = raw
	m.runtimeInstanceConfigProofs[i.ID] = proof
	m.instances[i.ID] = after
	return nil
}

func (s *PgStore) CompleteSnapshotPrimeHandoff(ctx context.Context, expected Instance, next, channel string, payload json.RawMessage) error {
	if !HasSnapshotPrimeAttempt(ctx) {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	owner, err := q.ReadDeploymentTrafficOwner(ctx, s.pool, mustPgUUID(expected.DeploymentID))
	if err != nil {
		return mapErr(err)
	}
	if uuidString(owner.AppID) != expected.AppID {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Parent ordering matches runtime publication: app, deployment, instance.
	if _, err := q.LockSnapshotPublicationApp(ctx, tx, sqlc.LockSnapshotPublicationAppParams{AccountID: owner.AccountID, AppID: owner.AppID}); err != nil {
		return mapErr(err)
	}
	if err := checkSnapshotPrimeAttemptTx(ctx, tx, expected.AppID, expected.DeploymentID); err != nil {
		return err
	}
	row, err := q.LockPrimeRecoveryWorker(ctx, tx, mustPgUUID(expected.ID))
	if err != nil {
		return mapErr(err)
	}
	i := Instance{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, NodeID: row.NodeID, WakeID: row.WakeID, State: row.State, Mode: row.Mode}
	if row.StartedAt.Valid {
		i.StartedAt = row.StartedAt.Time
	}
	proof, err := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{AccountID: mustPgUUID(row.AccountID), AppID: mustPgUUID(i.AppID), InstanceID: mustPgUUID(i.ID)})
	if err != nil {
		return mapErr(err)
	}
	fence := RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: proof.DeploymentID, EnvironmentID: proof.EnvironmentID, Scope: proof.Scope, Fingerprint: proof.SecretFingerprint}, Fingerprint: proof.ConfigFingerprint}
	raw, err := retainSnapshotPrimeHandoff(ctx, expected, i, next, channel, payload, fence)
	if err != nil {
		return err
	}
	n, err := q.SaveSnapshotPrimeHandoff(ctx, tx, sqlc.SaveSnapshotPrimeHandoffParams{InstanceID: mustPgUUID(i.ID), WakeID: mustPgUUID(i.WakeID), NodeID: mustPgUUID(i.NodeID), DeploymentID: mustPgUUID(i.DeploymentID), Handoff: raw})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	n, err = q.UpdateInstanceStateIf(ctx, tx, sqlc.UpdateInstanceStateIfParams{InstanceID: mustPgUUID(i.ID), ExpectedState: expected.State, NextState: next})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (m *MemStore) SnapshotPrimeHandoff(ctx context.Context, account, app, id string) (SnapshotPrimeHandoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SnapshotPrimeHandoff{}, err
	}
	i, ok := m.instances[id]
	owner := m.apps[app]
	proof := m.runtimeInstanceConfigProofs[id]
	if !ok || i.AppID != app || owner.AccountID != account || owner.Status == AppDeleted || proof.WakeID != i.WakeID || proof.NodeID != i.NodeID || len(proof.PrimeHandoff) == 0 {
		return SnapshotPrimeHandoff{}, ErrNotFound
	}
	return decodeSnapshotPrimeHandoff(ctx, proof.PrimeHandoff, i)
}

func (s *PgStore) SnapshotPrimeHandoff(ctx context.Context, account, app, id string) (SnapshotPrimeHandoff, error) {
	row, err := sqlc.New().ReadSnapshotPrimeHandoff(ctx, s.pool, sqlc.ReadSnapshotPrimeHandoffParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), InstanceID: mustPgUUID(id)})
	if err != nil {
		return SnapshotPrimeHandoff{}, mapErr(err)
	}
	return decodeSnapshotPrimeHandoff(ctx, row.PrimeActivationHandoff, Instance{WakeID: row.WakeID, NodeID: row.NodeID, StartedAt: row.StartedAt.Time})
}

func (m *MemStore) ListSnapshotPrimeInstances(ctx context.Context, app, deployment string, since time.Time) ([]Instance, error) {
	rows, err := m.ListInstancesForApp(ctx, app)
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, i := range rows {
		if i.DeploymentID == deployment && !i.StartedAt.Before(since) && (State(i.State).CountsForRAM() || i.State == string(StateParked) || i.State == string(StateStopped)) {
			out = append(out, i)
		}
	}
	return out, nil
}

func (s *PgStore) ListSnapshotPrimeInstances(ctx context.Context, app, deployment string, since time.Time) ([]Instance, error) {
	ids, err := sqlc.New().ListSnapshotPrimeInstances(ctx, s.pool, sqlc.ListSnapshotPrimeInstancesParams{AppID: mustPgUUID(app), DeploymentID: mustPgUUID(deployment), PreparedAt: pgTimestamptzUTC(since)})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []Instance
	for _, id := range ids {
		i, err := s.InstanceByID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

var _ SnapshotPrimeHandoffStore = (*MemStore)(nil)
var _ SnapshotPrimeHandoffStore = (*PgStore)(nil)
