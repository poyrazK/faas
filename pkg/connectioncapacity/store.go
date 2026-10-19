package connectioncapacity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/connectioncapacity/sqlc"
)

type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlc.New()} }

func Classify(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "PCC01":
			return ErrInsufficient
		case "PCC02":
			return ErrUnknown
		}
	}
	return err
}

func (s *Store) record(ctx context.Context, e Evidence, mapping func(pgx.Tx) error) (Snapshot, error) {
	now := time.Now()
	if !(Snapshot{Evidence: e}).Fresh(now) || e.Generation < 1 || e.Key == "" || e.Placement == "" {
		return Snapshot{}, ErrUnknown
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("capacity: begin observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.CapacityLock(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	_, err = s.q.CapacityObserve(ctx, tx, sqlc.CapacityObserveParams{ResourceKey: e.Key, Placement: e.Placement, Generation: e.Generation, ReportSha256: e.ReportSHA256,
		Connections: e.Connections, Slots: e.Slots, Senders: e.Senders, Workers: e.Workers, ObservedAt: pgtype.Timestamptz{Time: e.ObservedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: e.ExpiresAt, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrUnknown
	}
	if err != nil {
		return Snapshot{}, Classify(err)
	}
	if err = mapping(tx); err != nil {
		return Snapshot{}, Classify(err)
	}
	// Existing native work keeps its peak allocation even when observations
	// expire or the measured capacity shrinks. Recovery may expose overage;
	// it must never erase demand to make a new allocation fit.
	if err = s.q.CapacityRecoverSourceHolds(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if err = s.q.CapacityRecoverTargetHolds(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	r, readErr := s.read(ctx, tx, e.Key)
	if err = tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return r, readErr
}

func (s *Store) RecordSource(ctx context.Context, account, id string, revision int64, e Evidence) (Snapshot, error) {
	return s.record(ctx, e, func(tx pgx.Tx) error {
		return s.q.CapacityMapSource(ctx, tx, sqlc.CapacityMapSourceParams{AccountID: account, ConnectionID: id, ConnectionRevision: revision, ResourceKey: e.Key, Placement: e.Placement, Generation: e.Generation})
	})
}

type Target struct {
	AccountID, DatabaseID, BackendFingerprint, DataResourceID string
	Generation                                                int64
}

func (s *Store) RecordTarget(ctx context.Context, t Target, e Evidence) (Snapshot, error) {
	return s.record(ctx, e, func(tx pgx.Tx) error {
		return s.q.CapacityMapTarget(ctx, tx, sqlc.CapacityMapTargetParams{AccountID: t.AccountID, DatabaseID: t.DatabaseID, DatabaseGeneration: t.Generation, BackendFingerprint: t.BackendFingerprint, DataResourceID: t.DataResourceID, ResourceKey: e.Key, Placement: e.Placement, Generation: e.Generation})
	})
}

func (s *Store) read(ctx context.Context, db sqlc.DBTX, key string) (Snapshot, error) {
	r, err := s.q.CapacityRead(ctx, db, key)
	if err != nil {
		return Snapshot{}, err
	}
	unbounded, err := s.q.CapacityUnboundedConsumers(ctx, db, key)
	if err != nil {
		return Snapshot{}, err
	}
	if unbounded {
		return Snapshot{}, ErrUnknown
	}
	return Snapshot{Evidence: Evidence{Key: r.ResourceKey, Placement: r.Placement, Generation: r.Generation, ReportSHA256: r.ReportSha256,
		Limits: Limits{Connections: r.Connections, Slots: r.Slots, Senders: r.Senders, Workers: r.Workers}, ObservedAt: r.ObservedAt.Time, ExpiresAt: r.ExpiresAt.Time},
		Reserved: Limits{Connections: r.ReservedConnections, Slots: r.ReservedSlots, Senders: r.ReservedSenders, Workers: r.ReservedWorkers}}, nil
}

func (s *Store) Target(ctx context.Context, account, id string) (Target, Snapshot, error) {
	t, err := s.q.CapacityTarget(ctx, s.pool, sqlc.CapacityTargetParams{AccountID: account, DatabaseID: id})
	if err != nil {
		return Target{}, Snapshot{}, err
	}
	r, err := s.read(ctx, s.pool, t.ResourceKey)
	if err == nil && (r.Placement != t.Placement || r.Generation != t.Generation) {
		err = ErrUnknown
	}
	return Target{AccountID: account, DatabaseID: id, Generation: t.DatabaseGeneration, BackendFingerprint: t.BackendFingerprint, DataResourceID: t.DataResourceID}, r, err
}

// Pipeline returns the already reserved demand. It never allocates and never
// exposes resource keys, placements or qualification report identities.
func (s *Store) Pipeline(ctx context.Context, account, id string) (map[string]Snapshot, error) {
	holds, err := s.q.CapacityPipelineHolds(ctx, s.pool, sqlc.CapacityPipelineHoldsParams{AccountID: account, ID: id})
	if err != nil {
		return nil, err
	}
	out := map[string]Snapshot{}
	for _, hold := range holds {
		r, err := s.read(ctx, s.pool, hold.ResourceKey)
		if errors.Is(err, ErrUnknown) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if r.Placement == hold.Placement && r.Generation == hold.Generation {
			out[hold.Endpoint] = r
		}
	}
	return out, nil
}
