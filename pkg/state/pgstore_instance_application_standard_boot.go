package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ InstanceApplicationStandardBootStore = (*PgStore)(nil)
var _ ComputeNodeRuntimeIdentityStore = (*PgStore)(nil)

func (s *PgStore) RegisterComputeNodeRuntimeIdentity(ctx context.Context, identity runtimeadmission.Identity) error {
	if err := identity.Validate(); err != nil {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().RegisterComputeNodeRuntimeIdentity(ctx, s.pool, sqlc.RegisterComputeNodeRuntimeIdentityParams{NodeID: mustPgUUID(identity.NodeID), Incarnation: mustPgUUID(identity.Incarnation)})
	if err != nil {
		return fmt.Errorf("register native process: %w", mapErr(err))
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func lockStandardNativeBoot(ctx context.Context, db sqlc.DBTX, id, expectedState string) (InstanceApplicationStandardAdmission, string, time.Time, error) {
	raw, err := sqlc.New().LockInstanceApplicationStandardBoot(ctx, db, sqlc.LockInstanceApplicationStandardBootParams{InstanceID: mustPgUUID(id), ExpectedState: expectedState})
	if err != nil {
		return InstanceApplicationStandardAdmission{}, "", time.Time{}, mapErr(err)
	}
	var input nativeBootLockedInputs
	if err := json.Unmarshal(raw, &input); err != nil {
		return InstanceApplicationStandardAdmission{}, "", time.Time{}, err
	}
	now := time.Unix(0, input.ClockUnixNano).UTC()
	capture, err := decodeInstanceStandardAdmission(id, input.Snapshot, now)
	capture.NodeID, capture.NativeInputHash = input.NodeID, input.CapturedInputHash
	return capture, input.Incarnation, now, err
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
	defer tx.Rollback(ctx)
	capture, incarnation, now, err := lockStandardNativeBoot(ctx, tx, binding.InstanceID, expectedState)
	if err != nil {
		return runtimeadmission.Binding{}, fmt.Errorf("lock native boot inputs: %w", err)
	}
	if err := validateStandardBootBinding(binding, capture, incarnation, now); err != nil {
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
		if candidate != old.Binding || old.ExpectedState != expectedState || old.Binding.Validate(now) != nil {
			return runtimeadmission.Binding{}, ErrConflict
		}
		return old.Binding, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return runtimeadmission.Binding{}, mapErr(err)
	}
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
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
	if !standardRuntimeReceiptTarget(next, receipt) {
		return Instance{}, ErrInvalidArgument
	}
	if err := receipt.Check(receipt.Binding, time.Now()); err != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback(ctx)
	capture, incarnation, now, err := lockStandardNativeBoot(ctx, tx, receipt.Binding.InstanceID, expectedState)
	if err != nil {
		return Instance{}, fmt.Errorf("lock native publication inputs: %w", err)
	}
	if err := validateStandardBootBinding(receipt.Binding, capture, incarnation, now); err != nil {
		return Instance{}, err
	}
	q := sqlc.New()
	row, err := q.GetInstanceApplicationStandardBoot(ctx, tx, mustPgUUID(receipt.Binding.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Instance{}, ErrApplicationStandardRuntimeStale
		}
		return Instance{}, fmt.Errorf("read native boot grant: %w", mapErr(err))
	}
	boot, err := decodeStandardNativeBoot(row)
	if err != nil {
		return Instance{}, err
	}
	if boot.ExpectedState != expectedState || boot.Binding != receipt.Binding || receipt.Check(boot.Binding, now) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if boot.Receipt != nil && *boot.Receipt != receipt {
		return Instance{}, ErrConflict
	}
	if boot.Receipt == nil {
		raw, err := json.Marshal(receipt)
		if err != nil {
			return Instance{}, err
		}
		count, err := q.RecordInstanceApplicationStandardReceipt(ctx, tx, sqlc.RecordInstanceApplicationStandardReceiptParams{Token: mustPgUUID(boot.Binding.Token), Receipt: raw})
		if err != nil {
			return Instance{}, fmt.Errorf("save native receipt: %w", mapErr(err))
		}
		if count != 1 {
			return Instance{}, ErrConflict
		}
	}
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
	ins := standardPublishedInstance(actual)
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, mapErr(err)
	}
	return ins, nil
}

func standardPublishedInstance(row sqlc.GetPublishedApplicationStandardInstanceRow) Instance {
	ins := Instance{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID), State: row.State, Netns: row.Netns, GuestUID: int(row.GuestUid), HostIP: row.HostIp, RAMMB: int(row.RamMb), StartedAt: row.StartedAt.Time, LastRequestAt: row.LastRequestAt.Time, ParkedAt: row.ParkedAt.Time, TailCount: int(row.TailCount), Mode: row.Mode, RequestCount: row.RequestCount}
	if row.FrameworkReadyAt.Valid {
		value := row.FrameworkReadyAt.Time
		ins.FrameworkReadyAt = &value
	}
	return ins
}
