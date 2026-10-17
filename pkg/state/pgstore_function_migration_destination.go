package state

// adr: 1577

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FunctionMigrationDestinationStore = (*PgStore)(nil)

func decodeFunctionDestination(row sqlc.FunctionMigrationDestination) (FunctionMigrationDestination, error) {
	d := FunctionMigrationDestination{Phase: row.Phase}
	if err := decodeStrictFunctionDestination(row.GrantData, &d.Grant); err != nil {
		return d, err
	}
	if len(row.Receipt) > 0 {
		var r runtimeadmission.Receipt
		if err := decodeStrictFunctionDestination(row.Receipt, &r); err != nil {
			return d, err
		}
		d.Receipt = &r
	}
	if d.Grant.Validate(time.Unix(0, d.Grant.Binding.IssuedAtUnixNano)) != nil || d.Grant.Binding.Token != pgUUIDString(row.Token) ||
		d.Grant.Binding.InstanceID != pgUUIDString(row.InstanceID) || d.Grant.Source.LeaseToken != row.LeaseToken || d.Grant.Binding.NodeID != pgUUIDString(row.NodeID) || d.Grant.WakeID != pgUUIDString(row.WakeID) {
		return d, ErrApplicationStandardRuntimeStale
	}
	return d, nil
}
func decodeStrictFunctionDestination(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || !errors.Is(dec.Decode(new(any)), io.EOF) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
func (s *PgStore) GetFunctionMigrationDestination(ctx context.Context, id, token string) (FunctionMigrationDestination, error) {
	if u, err := uuid.Parse(id); err != nil || u == uuid.Nil || u.String() != id {
		return FunctionMigrationDestination{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetFunctionMigrationDestination(ctx, s.pool, sqlc.GetFunctionMigrationDestinationParams{InstanceID: mustPgUUID(id), LeaseToken: token})
	if err != nil {
		return FunctionMigrationDestination{}, mapErr(err)
	}
	return decodeFunctionDestination(row)
}
func (s *PgStore) RecordFunctionMigrationCapture(ctx context.Context, g runtimeadmission.FunctionMigrationGrant, c runtimeadmission.SnapshotCapture) error {
	if g.Validate(time.Now()) != nil || checkFunctionMigrationCaptureResult(g, c, time.Now()) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	_, err := retryRuntimeAdmissionBusy(ctx, func() (struct{}, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return struct{}{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		l, err := lockFunctionMigrationAdmission(ctx, tx, g.Parent.Binding.InstanceID, g.LeaseToken, string(StateRunning))
		if err != nil {
			return struct{}{}, err
		}
		if err := checkLockedFunctionMigrationGrant(l, g, false); err != nil {
			return struct{}{}, err
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return struct{}{}, err
		}
		count, err := sqlc.New().RecordFunctionMigrationCapture(ctx, tx, sqlc.RecordFunctionMigrationCaptureParams{InstanceID: mustPgUUID(g.Parent.Binding.InstanceID), LeaseToken: g.LeaseToken, CaptureToken: mustPgUUID(g.CaptureToken), Capture: raw})
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		if count != 1 {
			return struct{}{}, ErrConflict
		}
		return struct{}{}, mapErr(tx.Commit(ctx))
	})
	return err
}
func lockedFunctionDestinationSource(l functionMigrationLocked) (runtimeadmission.FunctionMigrationGrant, runtimeadmission.SnapshotCapture, error) {
	g, err := decodeFunctionMigrationGrant(l.row.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
	var c runtimeadmission.SnapshotCapture
	if err != nil || decodeStrictFunctionDestination(l.row.CaptureResult, &c) != nil || !g.Parent.Equal(l.input.Parent) ||
		g.SourceStartedAtUnixNano != l.input.SourceStartedAtUnixNano || g.LeaseCreatedAtUnixNano != l.lease.CreatedAt.UnixNano() ||
		g.MemoryKey != l.lease.MemStorageKey || g.VMStateKey != l.lease.VMStateStorageKey || l.source.CaptureToken != g.CaptureToken || checkFunctionMigrationCaptureResult(g, c, time.Unix(0, l.input.ClockUnixNano)) != nil {
		return g, c, ErrApplicationStandardRuntimeStale
	}
	memory, err := standardSnapshotRestoreMemoryBytes(l.input.capture.inputs)
	if err != nil || memory != c.Memory.Bytes {
		return g, c, ErrApplicationStandardRuntimeStale
	}
	return g, c, functionDestinationOwner(l.owner, l.source, l.lease)
}
func checkFunctionDestinationTx(ctx context.Context, tx pgx.Tx, l functionMigrationLocked, d FunctionMigrationDestination) error {
	g := d.Grant
	now := time.Unix(0, l.input.ClockUnixNano)
	capture, result, err := lockedFunctionDestinationSource(l)
	if err != nil {
		return err
	}
	if d.Phase != FunctionDestinationIssued && d.Phase != FunctionDestinationReady || g.Validate(now) != nil || !g.Source.Equal(capture) || !g.Restore.Capture.Equal(result) ||
		g.Binding.ExpiresAtUnixNano > l.lease.LeaseExpiresAt.UnixNano() || !standardNativeGrantWithinArtifactLease(g.Binding.ExpiresAtUnixNano, l.input.artifactDeadline()) {
		return ErrApplicationStandardRuntimeStale
	}
	identity, err := sqlc.New().GetFunctionMigrationDestinationIdentity(ctx, tx, sqlc.GetFunctionMigrationDestinationIdentityParams{InstanceID: mustPgUUID(g.Binding.InstanceID), NodeID: mustPgUUID(g.Binding.NodeID)})
	if err != nil {
		return mapErr(err)
	}
	if g.Binding.Incarnation != pgUUIDString(identity.VmmdIncarnation) || g.Binding.ProtocolVersion != uint32(identity.VmmdAdmissionProtocol) || g.Binding.CapturedInputHash != identity.InputHash {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
func lockFunctionDestination(ctx context.Context, tx pgx.Tx, id, token string) (functionMigrationLocked, FunctionMigrationDestination, error) {
	l, err := lockFunctionMigrationAdmission(ctx, tx, id, token, string(StateMigrating))
	if err != nil {
		return l, FunctionMigrationDestination{}, err
	}
	row, err := sqlc.New().LockFunctionMigrationDestination(ctx, tx, sqlc.LockFunctionMigrationDestinationParams{InstanceID: mustPgUUID(id), LeaseToken: token})
	if err != nil {
		return l, FunctionMigrationDestination{}, mapErr(err)
	}
	d, err := decodeFunctionDestination(row)
	return l, d, err
}
func (s *PgStore) IssueFunctionMigrationDestination(ctx context.Context, req FunctionMigrationDestinationRequest) (runtimeadmission.FunctionMigrationDestinationGrant, error) {
	if req.Identity.Validate() != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, ErrInvalidArgument
	}
	return retryRuntimeAdmissionBusy(ctx, func() (runtimeadmission.FunctionMigrationDestinationGrant, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		l, err := lockFunctionMigrationAdmission(ctx, tx, req.InstanceID, req.LeaseToken, string(StateMigrating))
		if err != nil {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, err
		}
		capture, result, err := lockedFunctionDestinationSource(l)
		if err != nil {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, err
		}
		q := sqlc.New()
		identity, err := q.GetFunctionMigrationDestinationIdentity(ctx, tx, sqlc.GetFunctionMigrationDestinationIdentityParams{InstanceID: mustPgUUID(req.InstanceID), NodeID: mustPgUUID(req.Identity.NodeID)})
		if err != nil {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, mapErr(err)
		}
		if req.Identity.Incarnation != pgUUIDString(identity.VmmdIncarnation) || req.Identity.ProtocolVersion != uint32(identity.VmmdAdmissionProtocol) {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, ErrApplicationStandardRuntimeStale
		}
		g, err := newFunctionMigrationDestination(req, capture, result, l.lease, identity.InputHash, time.Unix(0, l.input.ClockUnixNano), l.input.artifactDeadline())
		if err != nil {
			return g, err
		}
		row, err := q.LockFunctionMigrationDestination(ctx, tx, sqlc.LockFunctionMigrationDestinationParams{InstanceID: mustPgUUID(req.InstanceID), LeaseToken: req.LeaseToken})
		if err == nil {
			old, err := decodeFunctionDestination(row)
			if err != nil {
				return g, err
			}
			g.Binding.Token = old.Grant.Binding.Token
			g.Binding.IssuedAtUnixNano, g.Binding.ExpiresAtUnixNano = old.Grant.Binding.IssuedAtUnixNano, old.Grant.Binding.ExpiresAtUnixNano
			if !g.Equal(old.Grant) {
				return g, ErrConflict
			}
			if err := checkFunctionDestinationTx(ctx, tx, l, old); err != nil {
				return g, err
			}
			return old.Grant.Clone(), mapErr(tx.Commit(ctx))
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return g, mapErr(err)
		}
		raw, err := json.Marshal(g)
		if err != nil {
			return g, err
		}
		err = q.InsertFunctionMigrationDestination(ctx, tx, sqlc.InsertFunctionMigrationDestinationParams{Token: mustPgUUID(g.Binding.Token), InstanceID: mustPgUUID(req.InstanceID), LeaseToken: req.LeaseToken, NodeID: mustPgUUID(g.Binding.NodeID), WakeID: mustPgUUID(g.WakeID), GrantData: raw})
		if err != nil {
			return g, mapErr(err)
		}
		return g, mapErr(tx.Commit(ctx))
	})
}
func (s *PgStore) CheckFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	return s.withFunctionDestination(ctx, g, nil)
}
func (s *PgStore) ReadyFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, r runtimeadmission.Receipt) error {
	if g.CheckReceipt(r, time.Now()) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	return s.withFunctionDestination(ctx, g, func(ctx context.Context, tx pgx.Tx, d FunctionMigrationDestination) error {
		if d.Receipt != nil {
			if !d.Receipt.Equal(r) {
				return ErrConflict
			}
			return nil
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		count, err := sqlc.New().SetFunctionMigrationDestination(ctx, tx, sqlc.SetFunctionMigrationDestinationParams{Token: mustPgUUID(g.Binding.Token), ExpectedPhase: FunctionDestinationIssued, Phase: FunctionDestinationReady, Receipt: raw})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *PgStore) withFunctionDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, apply func(context.Context, pgx.Tx, FunctionMigrationDestination) error) error {
	if g.Validate(time.Now()) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	_, err := retryRuntimeAdmissionBusy(ctx, func() (struct{}, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return struct{}{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		l, d, err := lockFunctionDestination(ctx, tx, g.Binding.InstanceID, g.Source.LeaseToken)
		if err != nil {
			return struct{}{}, err
		}
		if !d.Grant.Equal(g) {
			return struct{}{}, ErrConflict
		}
		if err := checkFunctionDestinationTx(ctx, tx, l, d); err != nil {
			return struct{}{}, err
		}
		if apply != nil {
			if err := apply(ctx, tx, d); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, mapErr(tx.Commit(ctx))
	})
	return err
}
func (s *PgStore) functionDestinationRetirement(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, retire bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(g.Binding.InstanceID))
	if err != nil {
		return mapErr(err)
	}
	row, err := q.LockFunctionMigrationDestination(ctx, tx, sqlc.LockFunctionMigrationDestinationParams{InstanceID: owner.ID, LeaseToken: g.Source.LeaseToken})
	if err != nil {
		return mapErr(err)
	}
	d, err := decodeFunctionDestination(row)
	if err != nil {
		return err
	}
	if d.Phase == FunctionDestinationRetired && d.Grant.Equal(g) {
		return nil
	}
	source, err := q.GetFunctionMigrationSource(ctx, tx, sqlc.GetFunctionMigrationSourceParams{InstanceID: owner.ID, LeaseToken: g.Source.LeaseToken})
	if err != nil {
		return mapErr(err)
	}
	ins := Instance{ID: g.Binding.InstanceID, NodeID: pgUUIDString(owner.NodeID), WakeID: pgUUIDString(owner.WakeID), State: owner.State, LeaseToken: owner.LeaseToken.String}
	if err := functionDestinationRetirement(ins, functionMigrationSource(source), d, g); err != nil {
		return err
	}
	if retire && d.Phase != FunctionDestinationRetired {
		count, err := q.SetFunctionMigrationDestination(ctx, tx, sqlc.SetFunctionMigrationDestinationParams{Token: row.Token, ExpectedPhase: d.Phase, Phase: FunctionDestinationRetired, Receipt: row.Receipt})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrConflict
		}
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) CheckFunctionMigrationDestinationRetirement(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	return s.functionDestinationRetirement(ctx, g, false)
}
func (s *PgStore) RetireFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	return s.functionDestinationRetirement(ctx, g, true)
}
func (s *PgStore) CheckFunctionMigrationDestinationCommitted(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	input, err := lockStandardSnapshotCapture(ctx, tx, g.Binding.InstanceID, string(StateRunning))
	if err != nil {
		return err
	}
	row, err := sqlc.New().GetFunctionMigrationDestination(ctx, tx, sqlc.GetFunctionMigrationDestinationParams{InstanceID: mustPgUUID(g.Binding.InstanceID), LeaseToken: g.Source.LeaseToken})
	if err != nil {
		return mapErr(err)
	}
	d, err := decodeFunctionDestination(row)
	if err != nil {
		return err
	}
	owner, err := sqlc.New().LockInstanceMigrationCommit(ctx, tx, mustPgUUID(g.Binding.InstanceID))
	if err != nil {
		return mapErr(err)
	}
	if !d.Grant.Equal(g) || d.Phase != FunctionDestinationCommitted || d.Receipt == nil || !d.Receipt.Equal(input.Parent) || pgUUIDString(owner.WakeID) != g.WakeID || pgUUIDString(owner.NodeID) != g.Binding.NodeID {
		return ErrConflict
	}
	return nil
}

func functionDestinationRetiredTx(ctx context.Context, tx pgx.Tx, id, token string) error {
	row, err := sqlc.New().GetFunctionMigrationDestination(ctx, tx, sqlc.GetFunctionMigrationDestinationParams{InstanceID: mustPgUUID(id), LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	if row.Phase != FunctionDestinationRetired {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) migrateFunctionDestination(ctx context.Context, id, from, to, token string, input RuntimeConfigMigration) error {
	_, err := retryRuntimeAdmissionBusy(ctx, func() (struct{}, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return struct{}{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := reserveNodeForMigration(ctx, tx, id, to); err != nil {
			return struct{}{}, err
		}
		l, d, err := lockFunctionDestination(ctx, tx, id, token)
		if err != nil {
			return struct{}{}, err
		}
		if d.Grant.Source.Parent.Binding.NodeID != from || d.Grant.Binding.NodeID != to {
			return struct{}{}, ErrConflict
		}
		if err := checkFunctionDestinationTx(ctx, tx, l, d); err != nil {
			return struct{}{}, err
		}
		if err := checkFunctionDestinationRuntime(input, d); err != nil {
			return struct{}{}, err
		}
		fresh, err := readRuntimeConfigInputsFresh(ctx, tx, d.Grant.Binding.AppID, *input.Inputs)
		if err != nil {
			return struct{}{}, err
		}
		if !fresh {
			return struct{}{}, ErrApplicationStandardRuntimeStale
		}
		raw, err := json.Marshal(d.Receipt)
		if err != nil {
			return struct{}{}, err
		}
		q := sqlc.New()
		count, err := q.SetFunctionMigrationDestination(ctx, tx, sqlc.SetFunctionMigrationDestinationParams{Token: mustPgUUID(d.Grant.Binding.Token), ExpectedPhase: FunctionDestinationReady, Phase: FunctionDestinationCommitted, Receipt: raw})
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		if count != 1 {
			return struct{}{}, ErrConflict
		}
		row, err := q.MigrateInstanceRuntimeConfig(ctx, tx, sqlc.MigrateInstanceRuntimeConfigParams{InstanceID: mustPgUUID(id), FromNodeID: mustPgUUID(from), ToNodeID: mustPgUUID(to), LeaseToken: token,
			ExpectedWakeID: mustPgUUID(input.ExpectedWakeID), CheckSourceWake: true, WakeID: mustPgUUID(input.WakeID), Scope: input.Inputs.Scope, HasInputs: true, Netns: input.Netns, HostIp: input.HostIP, GuestUid: int32(input.GuestUID)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, ErrConflict
			}
			return struct{}{}, mapErr(err)
		}
		if err := q.ClearInstanceRuntimeConfigReceipt(ctx, tx, row.ID); err != nil {
			return struct{}{}, mapErr(err)
		}
		if err := recordInstanceRuntimeConfigReceipt(ctx, tx, id, input.WakeID, *input.Inputs); err != nil {
			return struct{}{}, err
		}
		if err := q.StampRuntimeMigrationApp(ctx, tx, row.AppID); err != nil {
			return struct{}{}, mapErr(err)
		}
		return struct{}{}, mapErr(tx.Commit(ctx))
	})
	return err
}

func (s *PgStore) GetFunctionMigrationCapture(ctx context.Context, id, token string) (runtimeadmission.SnapshotRestoreEvidence, error) {
	row, err := sqlc.New().GetFunctionMigrationSource(ctx, s.pool, sqlc.GetFunctionMigrationSourceParams{InstanceID: mustPgUUID(id), LeaseToken: token})
	if err != nil {
		return runtimeadmission.SnapshotRestoreEvidence{}, mapErr(err)
	}
	g, err := decodeFunctionMigrationGrant(row.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
	var c runtimeadmission.SnapshotCapture
	if err != nil || decodeStrictFunctionDestination(row.CaptureResult, &c) != nil || checkFunctionMigrationCaptureResult(g, c, time.Now()) != nil {
		return runtimeadmission.SnapshotRestoreEvidence{}, ErrApplicationStandardRuntimeStale
	}
	return runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: g.CaptureToken, FCVersion: g.FCVersion, Capture: c.Clone()}, nil
}
