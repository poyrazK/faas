package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ InstanceApplicationStandardBootStore = (*PgStore)(nil)
var _ ComputeNodeRuntimeIdentityStore = (*PgStore)(nil)

func (s *PgStore) RegisterComputeNodeRuntimeIdentity(ctx context.Context, identity runtimeadmission.Identity) error {
	if err := identity.Validate(); err != nil {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().RegisterComputeNodeRuntimeIdentity(ctx, s.pool, sqlc.RegisterComputeNodeRuntimeIdentityParams{NodeID: mustPgUUID(identity.NodeID), Incarnation: mustPgUUID(identity.Incarnation), ProtocolVersion: int16(identity.ProtocolVersion)})
	if err != nil {
		return fmt.Errorf("register native process: %w", mapErr(err))
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func lockStandardNativeBoot(ctx context.Context, tx pgx.Tx, id, expectedState string) (nativeBootLockedInputs, error) {
	raw, err := sqlc.New().LockInstanceApplicationStandardBoot(ctx, tx, sqlc.LockInstanceApplicationStandardBootParams{InstanceID: mustPgUUID(id), ExpectedState: expectedState})
	if err != nil {
		return nativeBootLockedInputs{}, mapErr(err)
	}
	var input nativeBootLockedInputs
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, err
	}
	err = lockStandardNativeArtifactInputs(ctx, tx, &input, id, false)
	return input, err
}

func decodeStandardNativeBoot(row sqlc.GetInstanceApplicationStandardBootRow) (instanceStandardBoot, error) {
	boot := instanceStandardBoot{ExpectedState: row.ExpectedState, ReceivedAt: row.ReceivedAt.Time}
	if err := json.Unmarshal(row.Binding, &boot.Binding); err != nil {
		return boot, err
	}
	if len(row.Receipt) > 0 {
		var receipt runtimeadmission.Receipt
		if err := json.Unmarshal(row.Receipt, &receipt); err != nil {
			return boot, err
		}
		boot.Receipt = &receipt
	}
	return boot, nil
}

func (s *PgStore) IssueInstanceApplicationStandardBoot(ctx context.Context, expectedState string, binding runtimeadmission.Binding) (runtimeadmission.Binding, error) {
	if expectedState != string(StateWaking) && expectedState != string(StateColdBooting) {
		return runtimeadmission.Binding{}, ErrInvalidArgument
	}
	if binding.Validate(time.Now()) != nil {
		return runtimeadmission.Binding{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	input, err := lockStandardNativeBoot(ctx, tx, binding.InstanceID, expectedState)
	if err != nil {
		return runtimeadmission.Binding{}, fmt.Errorf("lock native boot inputs: %w", err)
	}
	now := time.Unix(0, input.ClockUnixNano).UTC()
	if err := validateStandardBootBinding(binding, input.capture, input.Incarnation, input.ProtocolVersion, now); err != nil {
		return runtimeadmission.Binding{}, err
	}
	if err := checkStandardSnapshotRestoreTx(ctx, tx, binding, input.capture, now, nil); err != nil {
		return runtimeadmission.Binding{}, err
	}
	q := sqlc.New()
	row, err := q.GetInstanceApplicationStandardBoot(ctx, tx, mustPgUUID(binding.Token))
	if err == nil {
		old, decodeErr := decodeStandardNativeBoot(row)
		if decodeErr != nil {
			return runtimeadmission.Binding{}, decodeErr
		}
		candidate := binding
		candidate.IssuedAtUnixNano, candidate.ExpiresAtUnixNano = old.Binding.IssuedAtUnixNano, old.Binding.ExpiresAtUnixNano
		if candidate != old.Binding || old.ExpectedState != expectedState || old.Binding.Validate(now) != nil || !standardNativeGrantWithinArtifactLease(old.Binding.ExpiresAtUnixNano, input.artifactDeadline()) {
			return runtimeadmission.Binding{}, ErrConflict
		}
		return old.Binding, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return runtimeadmission.Binding{}, mapErr(err)
	}
	expires, err := standardNativeGrantExpiry(now, input.artifactDeadline())
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = now.UnixNano(), expires.UnixNano()
	raw, err := json.Marshal(binding)
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	if err := q.InsertInstanceApplicationStandardBoot(ctx, tx, sqlc.InsertInstanceApplicationStandardBootParams{Token: mustPgUUID(binding.Token), InstanceID: mustPgUUID(binding.InstanceID), ExpectedState: expectedState, Binding: raw}); err != nil {
		return runtimeadmission.Binding{}, fmt.Errorf("save native boot grant: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return runtimeadmission.Binding{}, mapErr(err)
	}
	return binding, nil
}

func (s *PgStore) PublishInstanceApplicationStandardRuntime(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt) (Instance, error) {
	return s.publishStandardRuntimeWithConfig(ctx, expectedState, next, receipt, "", nil)
}

func (s *PgStore) PublishInstanceApplicationStandardRuntimeWithConfig(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt, wakeID string, inputs RuntimeConfigInputs) (Instance, error) {
	return s.publishStandardRuntimeWithConfig(ctx, expectedState, next, receipt, wakeID, &inputs)
}

func (s *PgStore) publishStandardRuntimeWithConfig(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt, wakeID string, inputs *RuntimeConfigInputs) (Instance, error) {
	if !receipt.SnapshotResumeEvidence.IsZero() || !standardRuntimeReceiptTarget(next, receipt) {
		return Instance{}, ErrInvalidArgument
	}
	if receipt.Check(receipt.Binding, time.Now()) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if inputs != nil {
		if err := validateRuntimeConfigInputs(*inputs); err != nil {
			return Instance{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	boot, err := prepareStandardRuntimePublication(ctx, tx, expectedState, receipt)
	if err != nil {
		return Instance{}, err
	}
	if err := recordStandardBootReceipt(ctx, tx, boot, receipt); err != nil {
		return Instance{}, err
	}
	ins, err := publishStandardBootRuntime(ctx, tx, expectedState, next, boot, receipt)
	if err != nil {
		return Instance{}, err
	}
	if inputs != nil {
		if err := recordInstanceRuntimeConfigReceipt(ctx, tx, ins.ID, wakeID, *inputs); err != nil {
			return Instance{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, mapErr(err)
	}
	return ins, nil
}

func prepareStandardRuntimePublication(ctx context.Context, tx pgx.Tx, expectedState string, receipt runtimeadmission.Receipt) (instanceStandardBoot, error) {
	input, err := lockStandardNativeBoot(ctx, tx, receipt.Binding.InstanceID, expectedState)
	if err != nil {
		return instanceStandardBoot{}, fmt.Errorf("lock native publication inputs: %w", err)
	}
	now := time.Unix(0, input.ClockUnixNano).UTC()
	if err := validateStandardBootBinding(receipt.Binding, input.capture, input.Incarnation, input.ProtocolVersion, now); err != nil {
		return instanceStandardBoot{}, err
	}
	if err := checkStandardSnapshotRestoreTx(ctx, tx, receipt.Binding, input.capture, now, &receipt); err != nil {
		return instanceStandardBoot{}, err
	}
	if !standardNativeGrantWithinArtifactLease(receipt.Binding.ExpiresAtUnixNano, input.artifactDeadline()) {
		return instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
	}
	q := sqlc.New()
	row, err := q.GetInstanceApplicationStandardBoot(ctx, tx, mustPgUUID(receipt.Binding.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
		}
		return instanceStandardBoot{}, fmt.Errorf("read native boot grant: %w", mapErr(err))
	}
	boot, err := decodeStandardNativeBoot(row)
	if err != nil {
		return instanceStandardBoot{}, err
	}
	if boot.ExpectedState != expectedState || boot.Binding != receipt.Binding || receipt.Check(boot.Binding, now) != nil {
		return instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
	}
	if boot.Receipt != nil && !boot.Receipt.Equal(receipt) {
		return instanceStandardBoot{}, ErrConflict
	}
	return boot, nil
}

func recordStandardBootReceipt(ctx context.Context, tx pgx.Tx, boot instanceStandardBoot, receipt runtimeadmission.Receipt) error {
	q := sqlc.New()
	if boot.Receipt == nil {
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		count, err := q.RecordInstanceApplicationStandardReceipt(ctx, tx, sqlc.RecordInstanceApplicationStandardReceiptParams{Token: mustPgUUID(boot.Binding.Token), Receipt: raw})
		if err != nil {
			return fmt.Errorf("save native receipt: %w", mapErr(err))
		}
		if count != 1 {
			return ErrConflict
		}
	}
	return nil
}

func publishStandardBootRuntime(ctx context.Context, tx pgx.Tx, expectedState string, next State, boot instanceStandardBoot, receipt runtimeadmission.Receipt) (Instance, error) {
	q := sqlc.New()
	ip, _ := netip.ParseAddr(receipt.HostIP) // Receipt.Check already validates it.
	count, err := q.PublishInstanceApplicationStandardRuntime(ctx, tx, sqlc.PublishInstanceApplicationStandardRuntimeParams{Token: mustPgUUID(boot.Binding.Token), InstanceID: mustPgUUID(boot.Binding.InstanceID), ExpectedState: expectedState, NextState: string(next), Netns: receipt.Netns, HostIp: ip, GuestUid: receipt.LeaseUID})
	if err != nil {
		return Instance{}, fmt.Errorf("publish native runtime: %w", mapErr(err))
	}
	if count != 1 {
		return Instance{}, ErrConflict
	}
	actual, err := q.GetPublishedApplicationStandardInstance(ctx, tx, mustPgUUID(boot.Binding.InstanceID))
	if err != nil {
		return Instance{}, mapErr(err)
	}
	return standardPublishedInstance(actual), nil
}

func standardPublishedInstance(row sqlc.GetPublishedApplicationStandardInstanceRow) Instance {
	ins := Instance{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID), State: row.State, Netns: row.Netns, GuestUID: int(row.GuestUid), HostIP: row.HostIp, RAMMB: int(row.RamMb), StartedAt: row.StartedAt.Time, LastRequestAt: row.LastRequestAt.Time, ParkedAt: row.ParkedAt.Time, TailCount: int(row.TailCount), Mode: row.Mode, RequestCount: row.RequestCount}
	if row.FrameworkReadyAt.Valid {
		value := row.FrameworkReadyAt.Time
		ins.FrameworkReadyAt = &value
	}
	return ins
}
