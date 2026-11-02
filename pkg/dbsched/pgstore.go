package dbsched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PgStore is the production Store. Placement and wake admission lock the
// node row, so concurrent schedd replicas cannot overcommit a node.
type PgStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewPgStore wraps a pool owned by the daemon.
func NewPgStore(pool *pgxpool.Pool) (*PgStore, error) {
	if pool == nil {
		return nil, errors.New("dbsched: nil pool")
	}
	return &PgStore{pool: pool, q: sqlc.New()}, nil
}

var _ Store = (*PgStore)(nil)

func pgUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrNotFound
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func statesText(states []State) []string {
	out := make([]string, len(states))
	for i, s := range states {
		out[i] = string(s)
	}
	return out
}

func (s *PgStore) UpsertNode(ctx context.Context, node Node) error {
	err := s.q.UpsertDatabaseNode(ctx, s.pool, sqlc.UpsertDatabaseNodeParams{
		ID: node.ID, TargetUrl: node.TargetURL, AdmissionCeilingMb: int32(node.AdmissionCeilingMB),
		VolumeCapacityBytes: node.VolumeCapacityBytes, BootSlots: int32(bootSlots(node)), PostgresMajors: int32s(node.Majors), Lifecycle: node.Lifecycle,
	})
	if err != nil {
		return fmt.Errorf("dbsched: upsert node %s: %w", node.ID, err)
	}
	return nil
}

func (s *PgStore) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.q.ListDatabaseNodes(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("dbsched: list nodes: %w", err)
	}
	out := make([]Node, len(rows))
	for i, r := range rows {
		out[i] = Node{ID: r.ID, TargetURL: r.TargetUrl, AdmissionCeilingMB: int(r.AdmissionCeilingMb), VolumeCapacityBytes: r.VolumeCapacityBytes,
			BootSlots: int(r.BootSlots), Majors: ints(r.PostgresMajors), Lifecycle: r.Lifecycle}
	}
	return out, nil
}

func (s *PgStore) Loads(ctx context.Context) (map[string]NodeLoad, error) {
	usage, err := s.usage(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	return loads(usage), nil
}

func vmFromRow(r sqlc.DatabaseVm) (VM, error) {
	vm := VM{
		DatabaseID: uuid.UUID(r.DatabaseID.Bytes).String(), NodeID: r.NodeID, Major: int(r.PostgresMajor),
		VcpuCount: int(r.VcpuCount), MemMiB: int(r.MemMib), VolumeBytes: r.VolumeBytes, State: State(r.State),
		Bootstrapped: r.Bootstrapped, AlwaysOn: r.AlwaysOn, LastBackupAt: r.LastBackupAt.Time, LastError: r.LastError.String, LastActiveAt: r.LastActiveAt.Time,
		StateChangedAt: r.StateChangedAt.Time, CreatedAt: r.CreatedAt.Time,
		RestoreWindowSeconds: r.RestoreWindowSeconds, RestoreTarget: r.RestoreTarget.Time, RestoreRecoveredTo: r.RestoreRecoveredTo.Time,
		ResizePending: r.ResizePending, MoveNode: r.MoveNode.String, MoveLeaseUntil: r.MoveLeaseUntil.Time,
		Replicated: r.Replicated, StandbyNode: r.StandbyNode.String, StandbyRunning: r.StandbyRunning, StandbySynced: r.StandbySynced,
		PromotePending: r.PromotePending, DropStandbySlot: r.DropStandbySlot,
		Upgrade:           Upgrade{Operation: r.UpgradeOperation.String, From: int(r.UpgradeFrom.Int32), To: int(r.UpgradeMajor.Int32), Result: r.UpgradeResult.String},
		UpgradeLeaseUntil: r.UpgradeLeaseUntil.Time,
	}
	if r.RestoreSource.Valid {
		vm.RestoreSource = uuid.UUID(r.RestoreSource.Bytes).String()
	}
	if err := json.Unmarshal(r.MajorHistory, &vm.MajorHistory); err != nil {
		return VM{}, fmt.Errorf("dbsched: major history of %s: %w", vm.DatabaseID, err)
	}
	if len(vm.MajorHistory) == 0 {
		vm.MajorHistory = nil
	}
	return vm, nil
}

func (s *PgStore) Get(ctx context.Context, id string) (VM, error) {
	return s.get(ctx, s.pool, id)
}

func (s *PgStore) get(ctx context.Context, db sqlc.DBTX, id string) (VM, error) {
	key, err := pgUUID(id)
	if err != nil {
		return VM{}, err
	}
	row, err := s.q.GetDatabaseVM(ctx, db, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return VM{}, ErrNotFound
	}
	if err != nil {
		return VM{}, fmt.Errorf("dbsched: get %s: %w", id, err)
	}
	return vmFromRow(row)
}

func (s *PgStore) List(ctx context.Context, states ...State) ([]VM, error) {
	rows, err := s.q.ListDatabaseVMsByState(ctx, s.pool, statesText(states))
	if err != nil {
		return nil, fmt.Errorf("dbsched: list: %w", err)
	}
	out := make([]VM, len(rows))
	for i, r := range rows {
		if out[i], err = vmFromRow(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *PgStore) usage(ctx context.Context, db sqlc.DBTX) (map[string]nodeUsage, error) {
	rows, err := s.q.DatabaseNodeUsage(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("dbsched: node usage: %w", err)
	}
	usage := make(map[string]nodeUsage, len(rows))
	for _, r := range rows {
		usage[r.NodeID] = nodeUsage{volumeBytes: r.VolumeBytes, activeMB: r.ActiveMemMib + r.ActiveVms*billableMB(0), waking: r.WakingVms}
	}
	return usage, nil
}

func (s *PgStore) Place(ctx context.Context, vm VM) (VM, error) {
	key, err := pgUUID(vm.DatabaseID)
	if err != nil {
		return VM{}, err
	}
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		if existing, err := s.get(ctx, tx, vm.DatabaseID); err == nil || !errors.Is(err, ErrNotFound) {
			return existing, err
		}
		nodes, err := s.Nodes(ctx)
		if err != nil {
			return VM{}, err
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		node, ok := pickNode(nodes, usage, vm.VolumeBytes, vm.NodeID, vm.Major)
		if !ok {
			return VM{}, ErrNoCapacity
		}
		placed := []Node{node}
		if vm.Replicated {
			standby, ok := pickStandby(nodes, usage, vm, node.ID)
			if !ok {
				return VM{}, ErrNoCapacity
			}
			placed = append(placed, standby)
		}
		// Re-check under the node locks, taken in id order: another
		// replica may have placed a volume there since the read above.
		if err := s.lockNodes(ctx, tx, placed...); err != nil {
			return VM{}, err
		}
		if usage, err = s.usage(ctx, tx); err != nil {
			return VM{}, err
		}
		for _, n := range placed {
			if n.VolumeCapacityBytes-usage[n.ID].volumeBytes < vm.VolumeBytes {
				return VM{}, ErrNoCapacity
			}
		}
		params := sqlc.InsertDatabaseVMParams{
			DatabaseID: key, NodeID: node.ID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount),
			MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes, AlwaysOn: vm.AlwaysOn,
			RestoreWindowSeconds: vm.RestoreWindowSeconds, Replicated: vm.Replicated,
		}
		if len(placed) == 2 {
			params.StandbyNode = pgtype.Text{String: placed[1].ID, Valid: true}
		}
		if vm.RestoreSource != "" {
			if params.RestoreSource, err = pgUUID(vm.RestoreSource); err != nil {
				return VM{}, err
			}
			params.RestoreTarget = pgtype.Timestamptz{Time: vm.RestoreTarget, Valid: true}
		}
		if _, err := s.q.InsertDatabaseVM(ctx, tx, params); err != nil {
			return VM{}, fmt.Errorf("dbsched: insert %s: %w", vm.DatabaseID, err)
		}
		if err := s.open(ctx, tx, key, MeterStorage, vm.VolumeBytes, time.Now()); err != nil {
			return VM{}, err
		}
		return s.getSyncingStandby(ctx, tx, vm.DatabaseID)
	})
}

// getSyncingStandby reads a database inside tx and opens and closes its
// standby's ledger intervals to match it, starting a new interval when a
// rate changed. Every write that changes the standby ends with it.
func (s *PgStore) getSyncingStandby(ctx context.Context, tx pgx.Tx, id string) (VM, error) {
	vm, err := s.get(ctx, tx, id)
	if err != nil {
		return VM{}, err
	}
	key, now, want := mustUUID(id), time.Now(), standbyRates(vm)
	for _, meter := range []string{MeterStandbyCompute, MeterStandbyStorage} {
		rate, on := want[meter]
		current, err := s.q.OpenDatabaseUsageRate(ctx, tx, sqlc.OpenDatabaseUsageRateParams{DatabaseID: key, Meter: meter})
		open := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return VM{}, fmt.Errorf("dbsched: read %s usage: %w", meter, err)
		}
		if open && (!on || current != rate) {
			if err := s.close(ctx, tx, key, meter, now); err != nil {
				return VM{}, err
			}
		}
		if on && (!open || current != rate) {
			if err := s.open(ctx, tx, key, meter, rate, now); err != nil {
				return VM{}, err
			}
		}
	}
	return vm, nil
}

func (s *PgStore) AdmitWake(ctx context.Context, id string, keepStandbySynced bool) (VM, error) {
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if vm.State == StateWaking || vm.State == StateRunning {
			return vm, nil
		}
		if vm.State != StateParked {
			return VM{}, ErrConflict
		}
		row, err := s.q.LockDatabaseNode(ctx, tx, vm.NodeID)
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", vm.NodeID, err)
		}
		node := Node{ID: row.ID, AdmissionCeilingMB: int(row.AdmissionCeilingMb), BootSlots: int(row.BootSlots), Lifecycle: row.Lifecycle}
		if node.Lifecycle == NodeUnavailable {
			return VM{}, ErrNodeUnavailable
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		if err := admitWakeOn(node, usage[node.ID], vm.MemMiB); err != nil {
			return VM{}, err
		}
		if err := s.transition(ctx, tx, id, []State{StateParked}, StateWaking, TransitionOptions{LastError: vm.LastError}); err != nil {
			return VM{}, err
		}
		// The primary boots asynchronous: its standby no longer holds every
		// commit once it may commit without it, unless the wake resumes it.
		if vm.StandbySynced && !keepStandbySynced {
			if _, err := s.q.MarkDatabaseStandbySynced(ctx, tx, sqlc.MarkDatabaseStandbySyncedParams{Synced: false, DatabaseID: mustUUID(vm.DatabaseID)}); err != nil {
				return VM{}, fmt.Errorf("dbsched: unsync standby of %s: %w", id, err)
			}
		}
		return s.get(ctx, tx, id)
	})
}

func (s *PgStore) Transition(ctx context.Context, id string, from []State, to State, opts TransitionOptions) error {
	_, err := inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		return VM{}, s.transition(ctx, tx, id, from, to, opts)
	})
	return err
}

func (s *PgStore) transition(ctx context.Context, db sqlc.DBTX, id string, from []State, to State, opts TransitionOptions) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.TransitionDatabaseVM(ctx, db, sqlc.TransitionDatabaseVMParams{
		ToState: string(to), LastError: pgtype.Text{String: opts.LastError, Valid: opts.LastError != ""},
		Bootstrapped: opts.Bootstrapped, DatabaseID: key, FromStates: statesText(from),
	})
	if err != nil {
		return fmt.Errorf("dbsched: transition %s to %s: %w", id, to, err)
	}
	if n == 0 {
		if _, err := s.get(ctx, db, id); err != nil {
			return err
		}
		return ErrConflict
	}
	// Compute is metered while the VM runs: from waking until it leaves
	// the active states.
	if !isActive(to) {
		return s.close(ctx, db, key, MeterCompute, time.Now())
	}
	vm, err := s.get(ctx, db, id)
	if err != nil {
		return err
	}
	return s.open(ctx, db, key, MeterCompute, computeRate(vm), time.Now())
}

func (s *PgStore) RecordBackup(ctx context.Context, id string, at time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.RecordDatabaseVMBackup(ctx, s.pool, sqlc.RecordDatabaseVMBackupParams{At: pgtype.Timestamptz{Time: at, Valid: true}, DatabaseID: key})
	if err != nil {
		return fmt.Errorf("dbsched: record backup %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ClaimRestore(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	key, err := pgUUID(id)
	if err != nil {
		return time.Time{}, err
	}
	until, err := s.q.ClaimDatabaseVMRestore(ctx, s.pool, sqlc.ClaimDatabaseVMRestoreParams{
		LeaseSeconds: lease.Seconds(), DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: !held.IsZero()},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.Get(ctx, id); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, ErrConflict
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("dbsched: claim restore %s: %w", id, err)
	}
	return until.Time, nil
}

func (s *PgStore) RecordRestore(ctx context.Context, id string, recoveredTo time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.RecordDatabaseVMRestore(ctx, s.pool, sqlc.RecordDatabaseVMRestoreParams{RecoveredTo: pgtype.Timestamptz{Time: recoveredTo, Valid: true}, DatabaseID: key})
	if err != nil {
		return fmt.Errorf("dbsched: record restore %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) SetAlwaysOn(ctx context.Context, id string, alwaysOn bool) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.SetDatabaseVMAlwaysOn(ctx, s.pool, sqlc.SetDatabaseVMAlwaysOnParams{AlwaysOn: alwaysOn, DatabaseID: key})
	if err != nil {
		return fmt.Errorf("dbsched: set always-on %s: %w", id, err)
	}
	if n == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

func (s *PgStore) Touch(ctx context.Context, id string, at time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.TouchDatabaseVM(ctx, s.pool, sqlc.TouchDatabaseVMParams{At: pgtype.Timestamptz{Time: at, Valid: true}, DatabaseID: key})
	if err != nil {
		return fmt.Errorf("dbsched: touch %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) Delete(ctx context.Context, id string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	_, err = inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		n, err := s.q.DeleteDatabaseVM(ctx, tx, key)
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: delete %s: %w", id, err)
		}
		if n == 0 {
			if _, err := s.get(ctx, tx, id); errors.Is(err, ErrNotFound) {
				return VM{}, nil
			}
			return VM{}, ErrConflict
		}
		for _, meter := range rateMeters {
			if err := s.close(ctx, tx, key, meter, time.Now()); err != nil {
				return VM{}, err
			}
		}
		return VM{}, nil
	})
	return err
}

func (s *PgStore) open(ctx context.Context, db sqlc.DBTX, key pgtype.UUID, meter string, rate int64, at time.Time) error {
	err := s.q.OpenDatabaseUsage(ctx, db, sqlc.OpenDatabaseUsageParams{DatabaseID: key, Meter: meter, At: pgtype.Timestamptz{Time: at, Valid: true}, Rate: rate})
	if err != nil {
		return fmt.Errorf("dbsched: open %s usage: %w", meter, err)
	}
	return nil
}

func (s *PgStore) close(ctx context.Context, db sqlc.DBTX, key pgtype.UUID, meter string, at time.Time) error {
	if _, err := s.q.CloseDatabaseUsage(ctx, db, sqlc.CloseDatabaseUsageParams{At: pgtype.Timestamptz{Time: at, Valid: true}, DatabaseID: key, Meter: meter}); err != nil {
		return fmt.Errorf("dbsched: close %s usage: %w", meter, err)
	}
	return nil
}

func (s *PgStore) SampleUsage(ctx context.Context, id, meter string, rate int64, at time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	_, err = inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		current, err := s.q.OpenDatabaseUsageRate(ctx, tx, sqlc.OpenDatabaseUsageRateParams{DatabaseID: key, Meter: meter})
		switch {
		case err == nil && current == rate:
			return VM{}, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return VM{}, fmt.Errorf("dbsched: %s usage rate: %w", meter, err)
		}
		if err := s.close(ctx, tx, key, meter, at); err != nil {
			return VM{}, err
		}
		return VM{}, s.open(ctx, tx, key, meter, rate, at)
	})
	return err
}

func (s *PgStore) AddEgress(ctx context.Context, id string, bytes int64, at time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	err = s.q.AddDatabaseEgress(ctx, s.pool, sqlc.AddDatabaseEgressParams{DatabaseID: key, Hour: pgtype.Timestamptz{Time: usageHour(at), Valid: true}, Bytes: bytes})
	if err != nil {
		return fmt.Errorf("dbsched: add egress %s: %w", id, err)
	}
	return nil
}

func (s *PgStore) Usage(ctx context.Context, id string, from, to time.Time) (Usage, error) {
	key, err := pgUUID(id)
	if err != nil {
		return Usage{}, err
	}
	known, err := s.q.DatabaseHasUsage(ctx, s.pool, key)
	if err != nil {
		return Usage{}, fmt.Errorf("dbsched: usage %s: %w", id, err)
	}
	if !known {
		return Usage{}, ErrNotFound
	}
	rows, err := s.q.DatabaseUsageRows(ctx, s.pool, sqlc.DatabaseUsageRowsParams{
		DatabaseID: key, WindowFrom: pgtype.Timestamptz{Time: from, Valid: true}, WindowTo: pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return Usage{}, fmt.Errorf("dbsched: usage %s: %w", id, err)
	}
	ledger := make([]UsageRow, len(rows))
	for i, r := range rows {
		ledger[i] = UsageRow{Meter: r.Meter, Start: r.StartedAt.Time, End: r.EndedAt.Time, Rate: r.Rate}
	}
	return sumUsage(ledger, from, to, time.Now()), nil
}

func (s *PgStore) Resize(ctx context.Context, id string, shape Shape, at time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	_, err = inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if err := checkResize(vm, shape); err != nil {
			return VM{}, err
		}
		if vm.shape() == shape {
			return VM{}, nil
		}
		// The primary's node and a kept standby's, in one order.
		locked := []Node{{ID: vm.NodeID}}
		if vm.Replicated && shape.Replicated && vm.StandbyNode != "" {
			locked = append(locked, Node{ID: vm.StandbyNode})
		}
		if err := s.lockNodes(ctx, tx, locked...); err != nil {
			return VM{}, err
		}
		row, err := s.q.LockDatabaseNode(ctx, tx, vm.NodeID)
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", vm.NodeID, err)
		}
		node := Node{ID: row.ID, AdmissionCeilingMB: int(row.AdmissionCeilingMb), VolumeCapacityBytes: row.VolumeCapacityBytes, Lifecycle: row.Lifecycle}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		if err := resizeFits(node, usage[node.ID], vm, shape); err != nil {
			return VM{}, err
		}
		if len(locked) == 2 {
			row, err := s.q.LockDatabaseNode(ctx, tx, vm.StandbyNode)
			if err != nil {
				return VM{}, fmt.Errorf("dbsched: lock node %s: %w", vm.StandbyNode, err)
			}
			standby := Node{ID: row.ID, VolumeCapacityBytes: row.VolumeCapacityBytes, Lifecycle: row.Lifecycle}
			if err := standbyResizeFits(standby, usage[standby.ID], vm, shape); err != nil {
				return VM{}, err
			}
		}
		var standby string
		if shape.Replicated && !vm.Replicated {
			if standby, err = s.placeStandby(ctx, tx, vm, shape.VolumeBytes); err != nil {
				return VM{}, err
			}
		}
		next := vm
		replicationChange(&next, shape, standby)
		n, err := s.q.ResizeDatabaseVM(ctx, tx, sqlc.ResizeDatabaseVMParams{
			VcpuCount: int32(shape.VcpuCount), MemMib: int32(shape.MemMiB), VolumeBytes: shape.VolumeBytes, DatabaseID: key,
			Replicated: next.Replicated, StandbyNode: pgtype.Text{String: next.StandbyNode, Valid: next.StandbyNode != ""}, DropStandbySlot: next.DropStandbySlot,
		})
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: resize %s: %w", id, err)
		}
		if n == 0 {
			return VM{}, ErrConflict
		}
		if shape.VolumeBytes != vm.VolumeBytes {
			if err := s.close(ctx, tx, key, MeterStorage, at); err != nil {
				return VM{}, err
			}
			if err := s.open(ctx, tx, key, MeterStorage, shape.VolumeBytes, at); err != nil {
				return VM{}, err
			}
		}
		return s.getSyncingStandby(ctx, tx, id)
	})
	return err
}

// placeStandby picks a standby node for vm with a volume of volumeBytes
// and locks it, rechecking its volume capacity under the lock.
func (s *PgStore) placeStandby(ctx context.Context, tx pgx.Tx, vm VM, volumeBytes int64) (string, error) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return "", err
	}
	usage, err := s.usage(ctx, tx)
	if err != nil {
		return "", err
	}
	sized := vm
	sized.VolumeBytes = volumeBytes
	standby, ok := pickStandby(nodes, usage, sized, vm.NodeID)
	if !ok {
		return "", ErrNoCapacity
	}
	if err := s.lockNodes(ctx, tx, standby); err != nil {
		return "", err
	}
	if usage, err = s.usage(ctx, tx); err != nil {
		return "", err
	}
	if standby.VolumeCapacityBytes-usage[standby.ID].volumeBytes < volumeBytes {
		return "", ErrNoCapacity
	}
	return standby.ID, nil
}

func (s *PgStore) ClearStandbySlotDrop(ctx context.Context, id string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.ClearDatabaseStandbySlotDrop(ctx, s.pool, key)
	return s.changed(ctx, s.pool, id, n, err)
}

func (s *PgStore) FinishResize(ctx context.Context, id string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	if _, err := s.q.FinishDatabaseVMResize(ctx, s.pool, key); err != nil {
		return fmt.Errorf("dbsched: finish resize %s: %w", id, err)
	}
	return nil
}

func (s *PgStore) PruneUsage(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.q.PruneDatabaseUsage(ctx, s.pool, pgtype.Timestamptz{Time: before, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("dbsched: prune usage: %w", err)
	}
	return n, nil
}

func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) (VM, error)) (VM, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return VM{}, fmt.Errorf("dbsched: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	vm, err := fn(tx)
	if err != nil {
		return VM{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return VM{}, fmt.Errorf("dbsched: commit: %w", err)
	}
	return vm, nil
}

func (s *PgStore) BeginMove(ctx context.Context, id, target string, lease time.Duration) (VM, error) {
	key, err := pgUUID(id)
	if err != nil {
		return VM{}, err
	}
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if vm.State != StateParked || vm.ResizePending {
			return VM{}, ErrConflict
		}
		nodes, err := s.Nodes(ctx)
		if err != nil {
			return VM{}, err
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		node, err := moveTarget(nodes, usage, vm, target)
		if err != nil {
			return VM{}, err
		}
		// Re-check under the node lock, as Place does.
		if _, err := s.q.LockDatabaseNode(ctx, tx, node.ID); err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", node.ID, err)
		}
		if usage, err = s.usage(ctx, tx); err != nil {
			return VM{}, err
		}
		if node.VolumeCapacityBytes-usage[node.ID].volumeBytes < vm.VolumeBytes {
			return VM{}, ErrNoCapacity
		}
		n, err := s.q.BeginDatabaseVMMove(ctx, tx, sqlc.BeginDatabaseVMMoveParams{DatabaseID: key, MoveNode: node.ID, LeaseSeconds: lease.Seconds()})
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: begin move of %s: %w", id, err)
		}
		if n == 0 {
			return VM{}, ErrConflict
		}
		return s.get(ctx, tx, id)
	})
}

func (s *PgStore) RenewMove(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	key, err := pgUUID(id)
	if err != nil {
		return time.Time{}, err
	}
	next, err := s.q.RenewDatabaseVMMove(ctx, s.pool, sqlc.RenewDatabaseVMMoveParams{
		DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: true}, LeaseSeconds: lease.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrConflict
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("dbsched: renew move of %s: %w", id, err)
	}
	return next.Time, nil
}

func (s *PgStore) FinishMove(ctx context.Context, id string, held time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.FinishDatabaseVMMove(ctx, s.pool, sqlc.FinishDatabaseVMMoveParams{DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: true}})
	if err != nil {
		return fmt.Errorf("dbsched: finish move of %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) AbortMove(ctx context.Context, id string, held time.Time, lastError string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.AbortDatabaseVMMove(ctx, s.pool, sqlc.AbortDatabaseVMMoveParams{
		DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: !held.IsZero()},
		LastError: pgtype.Text{String: lastError, Valid: lastError != ""},
	})
	if err != nil {
		return fmt.Errorf("dbsched: abort move of %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) BeginUpgrade(ctx context.Context, id string, major int, operation string, lease time.Duration) (VM, error) {
	key, err := pgUUID(id)
	if err != nil {
		return VM{}, err
	}
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if vm.State != StateParked || !vm.Bootstrapped || vm.ResizePending || vm.PromotePending {
			return VM{}, ErrConflict
		}
		row, err := s.q.LockDatabaseNode(ctx, tx, vm.NodeID)
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", vm.NodeID, err)
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		node := Node{ID: row.ID, AdmissionCeilingMB: int(row.AdmissionCeilingMb), Lifecycle: row.Lifecycle}
		if err := admitUpgradeOn(node, usage[node.ID], vm, major); err != nil {
			return VM{}, err
		}
		n, err := s.q.BeginDatabaseVMUpgrade(ctx, tx, sqlc.BeginDatabaseVMUpgradeParams{
			DatabaseID: key, UpgradeMajor: int32(major), UpgradeOperation: operation, LeaseSeconds: lease.Seconds(), //nolint:gosec // majors are 14-18
		})
		if err := s.changed(ctx, tx, id, n, err); err != nil {
			return VM{}, err
		}
		return s.get(ctx, tx, id)
	})
}

func (s *PgStore) ClaimUpgrade(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	key, err := pgUUID(id)
	if err != nil {
		return time.Time{}, err
	}
	next, err := s.q.ClaimDatabaseVMUpgrade(ctx, s.pool, sqlc.ClaimDatabaseVMUpgradeParams{
		DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: !held.IsZero()}, LeaseSeconds: lease.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrConflict
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("dbsched: claim upgrade of %s: %w", id, err)
	}
	return next.Time, nil
}

func (s *PgStore) FinishUpgrade(ctx context.Context, id string, held time.Time, to State, until, backupAt time.Time) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	if held.IsZero() || (to != StateParked && to != StateRunning) {
		return ErrConflict
	}
	_, err = inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		n, err := s.q.FinishDatabaseVMUpgrade(ctx, tx, sqlc.FinishDatabaseVMUpgradeParams{
			DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: true}, ToState: string(to),
			Until: pgtype.Timestamptz{Time: until, Valid: true}, BackupAt: pgtype.Timestamptz{Time: backupAt, Valid: !backupAt.IsZero()},
		})
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: finish upgrade of %s: %w", id, err)
		}
		if n == 0 {
			return VM{}, ErrConflict
		}
		if to != StateRunning {
			return VM{}, nil
		}
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		return VM{}, s.open(ctx, tx, key, MeterCompute, computeRate(vm), time.Now())
	})
	return err
}

func (s *PgStore) AbortUpgrade(ctx context.Context, id string, held time.Time, to State, lastError string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	if to != StateParked && to != StateFailed {
		return ErrConflict
	}
	n, err := s.q.AbortDatabaseVMUpgrade(ctx, s.pool, sqlc.AbortDatabaseVMUpgradeParams{
		DatabaseID: key, Held: pgtype.Timestamptz{Time: held, Valid: !held.IsZero()}, ToState: string(to),
		UpgradeResult: upgradeResultFor(to), LastError: pgtype.Text{String: lastError, Valid: lastError != ""},
	})
	if err != nil {
		return fmt.Errorf("dbsched: abort upgrade of %s: %w", id, err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func int32s(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v) //nolint:gosec // majors are 14-18
	}
	return out
}

func ints(in []int32) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

// lockNodes locks node rows in id order, so concurrent placements cannot
// deadlock.
func (s *PgStore) lockNodes(ctx context.Context, tx pgx.Tx, nodes ...Node) error {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := s.q.LockDatabaseNode(ctx, tx, id); err != nil {
			return fmt.Errorf("dbsched: lock node %s: %w", id, err)
		}
	}
	return nil
}

// mustUUID converts an id that came from a row.
func mustUUID(id string) pgtype.UUID {
	key, _ := pgUUID(id)
	return key
}

// changed reports an update that matched no row: ErrNotFound for a missing
// database, else ErrConflict.
func (s *PgStore) changed(ctx context.Context, db sqlc.DBTX, id string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("dbsched: update %s: %w", id, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := s.get(ctx, db, id); err != nil {
		return err
	}
	return ErrConflict
}

func (s *PgStore) ClaimStandby(ctx context.Context, id string) (VM, error) {
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if vm.State != StateRunning || vm.StandbyNode == "" || vm.StandbyRunning {
			return VM{}, ErrConflict
		}
		row, err := s.q.LockDatabaseNode(ctx, tx, vm.StandbyNode)
		if err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", vm.StandbyNode, err)
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		node := Node{ID: row.ID, AdmissionCeilingMB: int(row.AdmissionCeilingMb), Lifecycle: row.Lifecycle}
		if err := claimStandbyOn(node, usage[node.ID], vm.MemMiB); err != nil {
			return VM{}, err
		}
		n, err := s.q.ClaimDatabaseStandby(ctx, tx, mustUUID(vm.DatabaseID))
		if err := s.changed(ctx, tx, id, n, err); err != nil {
			return VM{}, err
		}
		return s.getSyncingStandby(ctx, tx, id)
	})
}

func (s *PgStore) ReleaseStandby(ctx context.Context, id string, keepSynced bool) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	_, err = inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		n, err := s.q.ReleaseDatabaseStandby(ctx, tx, sqlc.ReleaseDatabaseStandbyParams{KeepSynced: keepSynced, DatabaseID: key})
		if err := s.changed(ctx, tx, id, n, err); err != nil {
			return VM{}, err
		}
		return s.getSyncingStandby(ctx, tx, id)
	})
	return err
}

func (s *PgStore) MarkStandbySynced(ctx context.Context, id string, synced bool) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.MarkDatabaseStandbySynced(ctx, s.pool, sqlc.MarkDatabaseStandbySyncedParams{Synced: synced, DatabaseID: key})
	return s.changed(ctx, s.pool, id, n, err)
}

func (s *PgStore) ReplaceStandby(ctx context.Context, id string, avoid ...string) (VM, error) {
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if !vm.Replicated || vm.StandbyRunning || vm.State == StateMoving || vm.State == StateDeleting {
			return VM{}, ErrConflict
		}
		nodes, err := s.Nodes(ctx)
		if err != nil {
			return VM{}, err
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		standby, ok := pickStandby(nodes, usage, vm, append([]string{vm.NodeID, vm.StandbyNode}, avoid...)...)
		if !ok {
			return VM{}, ErrNoCapacity
		}
		if err := s.lockNodes(ctx, tx, standby); err != nil {
			return VM{}, err
		}
		if usage, err = s.usage(ctx, tx); err != nil {
			return VM{}, err
		}
		if standby.VolumeCapacityBytes-usage[standby.ID].volumeBytes < vm.VolumeBytes {
			return VM{}, ErrNoCapacity
		}
		n, err := s.q.ReplaceDatabaseStandby(ctx, tx, sqlc.ReplaceDatabaseStandbyParams{StandbyNode: pgtype.Text{String: standby.ID, Valid: true}, DatabaseID: mustUUID(vm.DatabaseID)})
		if err := s.changed(ctx, tx, id, n, err); err != nil {
			return VM{}, err
		}
		return s.getSyncingStandby(ctx, tx, id)
	})
}

func (s *PgStore) Failover(ctx context.Context, id string) (VM, error) {
	return inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.get(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		if !canFailover(vm) {
			return VM{}, ErrConflict
		}
		n, err := s.q.FailoverDatabaseVM(ctx, tx, mustUUID(vm.DatabaseID))
		if err := s.changed(ctx, tx, id, n, err); err != nil {
			return VM{}, err
		}
		after, err := s.getSyncingStandby(ctx, tx, id)
		if err != nil {
			return VM{}, err
		}
		key, now := mustUUID(id), time.Now()
		switch {
		case isActive(after.State) && !isActive(vm.State):
			err = s.open(ctx, tx, key, MeterCompute, computeRate(after), now)
		case !isActive(after.State) && isActive(vm.State):
			err = s.close(ctx, tx, key, MeterCompute, now)
		}
		return after, err
	})
}

func (s *PgStore) FinishPromotion(ctx context.Context, id string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	n, err := s.q.FinishDatabasePromotion(ctx, s.pool, key)
	return s.changed(ctx, s.pool, id, n, err)
}

func (s *PgStore) StartDrill(ctx context.Context, d Drill) (bool, error) {
	id, err := pgUUID(d.ID)
	if err != nil {
		return false, fmt.Errorf("%w: drill id", ErrInvalid)
	}
	source, err := pgUUID(d.SourceID)
	if err != nil {
		return false, fmt.Errorf("%w: drill source", ErrInvalid)
	}
	n, err := s.q.StartDatabaseRestoreDrill(ctx, s.pool, sqlc.StartDatabaseRestoreDrillParams{
		DrillID: id, NodeID: d.NodeID, SourceID: source, TargetAt: pgtype.Timestamptz{Time: d.TargetAt, Valid: true},
	})
	return n == 1, err
}

func (s *PgStore) RunningDrills(ctx context.Context) ([]Drill, error) {
	rows, err := s.q.RunningDatabaseRestoreDrills(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]Drill, 0, len(rows))
	for _, r := range rows {
		out = append(out, Drill{ID: uuid.UUID(r.DrillID.Bytes).String(), NodeID: r.NodeID, SourceID: uuid.UUID(r.SourceID.Bytes).String(),
			TargetAt: r.TargetAt.Time, StartedAt: r.StartedAt.Time, Result: DrillResult(r.Result)})
	}
	return out, nil
}

func (s *PgStore) FinishDrill(ctx context.Context, id string, result DrillResult, recoveredTo time.Time, errorCode string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	_, err = s.q.FinishDatabaseRestoreDrill(ctx, s.pool, sqlc.FinishDatabaseRestoreDrillParams{
		Result: string(result), RecoveredTo: pgtype.Timestamptz{Time: recoveredTo, Valid: !recoveredTo.IsZero()},
		ErrorCode: pgtype.Text{String: errorCode, Valid: errorCode != ""}, DrillID: key,
	})
	return err
}

func (s *PgStore) DrillsByNode(ctx context.Context) (map[string]DrillSummary, error) {
	rows, err := s.q.DatabaseRestoreDrillsByNode(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make(map[string]DrillSummary, len(rows))
	for _, r := range rows {
		sum := DrillSummary{LastStarted: r.LastStarted.Time}
		if r.LastOk.Time.After(time.Unix(0, 0)) {
			sum.LastOK = r.LastOk.Time
		}
		out[r.NodeID] = sum
	}
	return out, nil
}

func (s *PgStore) PruneDrills(ctx context.Context, before time.Time) (int64, error) {
	return s.q.PruneDatabaseRestoreDrills(ctx, s.pool, pgtype.Timestamptz{Time: before, Valid: true})
}
