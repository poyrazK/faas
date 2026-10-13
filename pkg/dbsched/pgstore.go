package dbsched

import (
	"context"
	"errors"
	"fmt"
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
		VolumeCapacityBytes: node.VolumeCapacityBytes, Lifecycle: node.Lifecycle,
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
		out[i] = Node{ID: r.ID, TargetURL: r.TargetUrl, AdmissionCeilingMB: int(r.AdmissionCeilingMb), VolumeCapacityBytes: r.VolumeCapacityBytes, Lifecycle: r.Lifecycle}
	}
	return out, nil
}

func vmFromRow(r sqlc.DatabaseVm) VM {
	vm := VM{
		DatabaseID: uuid.UUID(r.DatabaseID.Bytes).String(), NodeID: r.NodeID, Major: int(r.PostgresMajor),
		VcpuCount: int(r.VcpuCount), MemMiB: int(r.MemMib), VolumeBytes: r.VolumeBytes, State: State(r.State),
		Bootstrapped: r.Bootstrapped, AlwaysOn: r.AlwaysOn, LastBackupAt: r.LastBackupAt.Time, LastError: r.LastError.String, LastActiveAt: r.LastActiveAt.Time,
		StateChangedAt: r.StateChangedAt.Time, CreatedAt: r.CreatedAt.Time,
		RestoreWindowSeconds: r.RestoreWindowSeconds, RestoreTarget: r.RestoreTarget.Time, RestoreRecoveredTo: r.RestoreRecoveredTo.Time,
		ResizePending: r.ResizePending,
	}
	if r.RestoreSource.Valid {
		vm.RestoreSource = uuid.UUID(r.RestoreSource.Bytes).String()
	}
	return vm
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
	return vmFromRow(row), nil
}

func (s *PgStore) List(ctx context.Context, states ...State) ([]VM, error) {
	rows, err := s.q.ListDatabaseVMsByState(ctx, s.pool, statesText(states))
	if err != nil {
		return nil, fmt.Errorf("dbsched: list: %w", err)
	}
	out := make([]VM, len(rows))
	for i, r := range rows {
		out[i] = vmFromRow(r)
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
		usage[r.NodeID] = nodeUsage{volumeBytes: r.VolumeBytes, activeMB: r.ActiveMemMib + r.ActiveVms*billableMB(0)}
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
		node, ok := pickNode(nodes, usage, vm.VolumeBytes, vm.NodeID)
		if !ok {
			return VM{}, ErrNoCapacity
		}
		// Re-check under the node lock: another replica may have placed a
		// volume there since the read above.
		if _, err := s.q.LockDatabaseNode(ctx, tx, node.ID); err != nil {
			return VM{}, fmt.Errorf("dbsched: lock node %s: %w", node.ID, err)
		}
		if usage, err = s.usage(ctx, tx); err != nil {
			return VM{}, err
		}
		if node.VolumeCapacityBytes-usage[node.ID].volumeBytes < vm.VolumeBytes {
			return VM{}, ErrNoCapacity
		}
		params := sqlc.InsertDatabaseVMParams{
			DatabaseID: key, NodeID: node.ID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount),
			MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes, AlwaysOn: vm.AlwaysOn,
			RestoreWindowSeconds: vm.RestoreWindowSeconds,
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
		return s.get(ctx, tx, vm.DatabaseID)
	})
}

func (s *PgStore) AdmitWake(ctx context.Context, id string) (VM, error) {
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
		node := Node{ID: row.ID, AdmissionCeilingMB: int(row.AdmissionCeilingMb), Lifecycle: row.Lifecycle}
		if node.Lifecycle == NodeUnavailable {
			return VM{}, ErrNodeUnavailable
		}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		if !admits(node, usage[node.ID], vm.MemMiB) {
			return VM{}, ErrNoCapacity
		}
		if err := s.transition(ctx, tx, id, []State{StateParked}, StateWaking, TransitionOptions{LastError: vm.LastError}); err != nil {
			return VM{}, err
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
		for _, meter := range []string{MeterCompute, MeterStorage, MeterHistory} {
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
		n, err := s.q.ResizeDatabaseVM(ctx, tx, sqlc.ResizeDatabaseVMParams{
			VcpuCount: int32(shape.VcpuCount), MemMib: int32(shape.MemMiB), VolumeBytes: shape.VolumeBytes, DatabaseID: key,
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
		return VM{}, nil
	})
	return err
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
