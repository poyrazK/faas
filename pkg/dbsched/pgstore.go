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
	return VM{
		DatabaseID: uuid.UUID(r.DatabaseID.Bytes).String(), NodeID: r.NodeID, Major: int(r.PostgresMajor),
		VcpuCount: int(r.VcpuCount), MemMiB: int(r.MemMib), VolumeBytes: r.VolumeBytes, State: State(r.State),
		Bootstrapped: r.Bootstrapped, LastError: r.LastError.String, LastActiveAt: r.LastActiveAt.Time,
		StateChangedAt: r.StateChangedAt.Time, CreatedAt: r.CreatedAt.Time,
	}
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
		node, ok := pickNode(nodes, usage, vm.VolumeBytes)
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
		if _, err := s.q.InsertDatabaseVM(ctx, tx, sqlc.InsertDatabaseVMParams{
			DatabaseID: key, NodeID: node.ID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount),
			MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes,
		}); err != nil {
			return VM{}, fmt.Errorf("dbsched: insert %s: %w", vm.DatabaseID, err)
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
	return s.transition(ctx, s.pool, id, from, to, opts)
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
	n, err := s.q.DeleteDatabaseVM(ctx, s.pool, key)
	if err != nil {
		return fmt.Errorf("dbsched: delete %s: %w", id, err)
	}
	if n == 0 {
		if _, err := s.Get(ctx, id); errors.Is(err, ErrNotFound) {
			return nil
		}
		return ErrConflict
	}
	return nil
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
