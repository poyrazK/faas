package agents

// Persisted sandbox workspaces (ADR-20261027090013 step 7). agentd reserves
// an archive's bytes against the account's SandboxSnapshotBytesPerAccount
// before vmmd writes it, completes the row with the archive's size and
// digest, and deletes rows and their archives in its sweep. apid reads the
// rows, starts sessions from ready ones and requests deletes.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Persisted workspace states.
const (
	SandboxSnapshotPending = "pending"
	SandboxSnapshotReady   = "ready"
)

// ErrSnapshotUnavailable is a snapshot that is not the account's, not ready,
// expired or being deleted.
var ErrSnapshotUnavailable = errors.New("agents: sandbox snapshot unavailable")

// SandboxSnapshotKey is where a persisted workspace's archive is kept in the
// artifact store.
func SandboxSnapshotKey(accountID, snapshotID string) string {
	return "agents/workspaces/" + accountID + "/" + snapshotID + ".tar.gz"
}

// SandboxSnapshot is one persisted workspace.
type SandboxSnapshot struct {
	ID            string
	AccountID     string
	SandboxID     string
	State         string
	StorageKey    string
	ReservedBytes int64
	// Bytes and SHA256 describe the archive once it is ready.
	Bytes             int64
	SHA256            string
	ExpiresAt         time.Time
	DeleteRequestedAt *time.Time
	CreatedAt         time.Time
	ReadyAt           *time.Time
}

// SnapshotReservation asks for room for one archive. MaxBytes is the most
// one archive may take (SandboxWorkspaceMaxMB); QuotaBytes the account's
// SandboxSnapshotBytesPerAccount.
type SnapshotReservation struct {
	SnapshotID string
	AccountID  string
	SandboxID  string
	MaxBytes   int64
	QuotaBytes int64
	ExpiresAt  time.Time
}

// ReserveSandboxSnapshot inserts a pending snapshot holding what is left of
// the account's quota, up to MaxBytes, and returns it. A full quota is a
// *LimitError on SandboxSnapshotBytesPerAccount.
func (s *Store) ReserveSandboxSnapshot(ctx context.Context, r SnapshotReservation) (SandboxSnapshot, error) {
	if !isUUID(r.SnapshotID) || !isUUID(r.SandboxID) || r.MaxBytes <= 0 || r.QuotaBytes <= 0 {
		return SandboxSnapshot{}, fmt.Errorf("reserve sandbox snapshot: invalid reservation")
	}
	key := SandboxSnapshotKey(r.AccountID, r.SnapshotID)
	var reserved int64
	err := s.locked(ctx, r.AccountID, func(tx pgx.Tx) error {
		used, err := s.q.SandboxSnapshotBytesUsed(ctx, tx, r.AccountID)
		if err != nil {
			return fmt.Errorf("sum sandbox snapshots: %w", err)
		}
		if used >= r.QuotaBytes {
			return &LimitError{Limit: "SandboxSnapshotBytesPerAccount", Max: int(r.QuotaBytes), Observed: int(used)}
		}
		reserved = min(r.MaxBytes, r.QuotaBytes-used)
		if err := s.q.InsertSandboxSnapshot(ctx, tx, sqlc.InsertSandboxSnapshotParams{SnapshotID: r.SnapshotID, AccountID: r.AccountID,
			SandboxID: r.SandboxID, StorageKey: key, ReservedBytes: reserved,
			ExpiresAt: pgtype.Timestamptz{Time: r.ExpiresAt, Valid: true}}); err != nil {
			return fmt.Errorf("insert sandbox snapshot: %w", err)
		}
		return nil
	})
	if err != nil {
		return SandboxSnapshot{}, err
	}
	return SandboxSnapshot{ID: r.SnapshotID, AccountID: r.AccountID, SandboxID: r.SandboxID, State: SandboxSnapshotPending,
		StorageKey: key, ReservedBytes: reserved, ExpiresAt: r.ExpiresAt.UTC()}, nil
}

// CompleteSandboxSnapshot marks a pending snapshot ready with its archive's
// size and digest. ErrSnapshotUnavailable: the reservation is gone (swept,
// or deleted on request) or the archive is larger than it.
func (s *Store) CompleteSandboxSnapshot(ctx context.Context, id string, bytes int64, sha256 string) error {
	n, err := s.q.CompleteSandboxSnapshot(ctx, s.pool, sqlc.CompleteSandboxSnapshotParams{Bytes: bytes, Sha256: sha256, SnapshotID: id})
	if err != nil {
		return fmt.Errorf("complete sandbox snapshot: %w", err)
	}
	if n == 0 {
		return ErrSnapshotUnavailable
	}
	return nil
}

// DeleteSandboxSnapshot removes a snapshot's row; its archive is the
// caller's to delete first.
func (s *Store) DeleteSandboxSnapshot(ctx context.Context, id string) error {
	if _, err := s.q.DeleteSandboxSnapshot(ctx, s.pool, id); err != nil {
		return fmt.Errorf("delete sandbox snapshot: %w", err)
	}
	return nil
}

// SweepableSandboxSnapshot is a snapshot agentd's sweep deletes.
type SweepableSandboxSnapshot struct {
	ID         string
	StorageKey string
}

// SweepableSandboxSnapshots lists up to limit snapshots past their
// retention, asked to be deleted, or pending for longer than pendingTTL.
func (s *Store) SweepableSandboxSnapshots(ctx context.Context, pendingTTL time.Duration, limit int) ([]SweepableSandboxSnapshot, error) {
	rows, err := s.q.ListSweepableSandboxSnapshots(ctx, s.pool, sqlc.ListSweepableSandboxSnapshotsParams{
		PendingTtlSeconds: pendingTTL.Seconds(), MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list sweepable sandbox snapshots: %w", err)
	}
	out := make([]SweepableSandboxSnapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, SweepableSandboxSnapshot{ID: r.ID, StorageKey: r.StorageKey})
	}
	return out, nil
}

// SandboxSnapshot reads one snapshot of the account.
func (s *Store) SandboxSnapshot(ctx context.Context, accountID, id string) (SandboxSnapshot, error) {
	if !isUUID(id) {
		return SandboxSnapshot{}, ErrNotFound
	}
	row, err := s.q.GetSandboxSnapshot(ctx, s.pool, sqlc.GetSandboxSnapshotParams{SnapshotID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxSnapshot{}, ErrNotFound
	}
	if err != nil {
		return SandboxSnapshot{}, fmt.Errorf("read sandbox snapshot: %w", err)
	}
	return snapshotFrom(sqlc.ListSandboxSnapshotsRow(row)), nil
}

// SandboxSnapshotFilter pages a snapshot listing backwards from Before.
type SandboxSnapshotFilter struct {
	Before *SandboxSnapshot
	Limit  int
}

// SandboxSnapshots lists the account's snapshots, newest first, without
// those being deleted.
func (s *Store) SandboxSnapshots(ctx context.Context, accountID string, f SandboxSnapshotFilter) ([]SandboxSnapshot, error) {
	params := sqlc.ListSandboxSnapshotsParams{AccountID: accountID, MaxRows: int32(f.Limit)}
	if b := f.Before; b != nil {
		params.BeforeCreatedAt = pgtype.Timestamptz{Time: b.CreatedAt, Valid: true}
		params.BeforeID = optText(b.ID)
	}
	rows, err := s.q.ListSandboxSnapshots(ctx, s.pool, params)
	if err != nil {
		return nil, fmt.Errorf("list sandbox snapshots: %w", err)
	}
	out := make([]SandboxSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotFrom(row))
	}
	return out, nil
}

// RequestSandboxSnapshotDelete asks agentd to delete a snapshot; it stops
// counting as usable at once. ErrNotFound: the account has no such snapshot.
func (s *Store) RequestSandboxSnapshotDelete(ctx context.Context, accountID, id string) error {
	if !isUUID(id) {
		return ErrNotFound
	}
	n, err := s.q.RequestSandboxSnapshotDelete(ctx, s.pool, sqlc.RequestSandboxSnapshotDeleteParams{SnapshotID: id, AccountID: accountID})
	if err != nil {
		return fmt.Errorf("request sandbox snapshot delete: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// usableSnapshot checks that a session may start from the snapshot: the
// account's, ready, not being deleted and not past its retention.
func (s *Store) usableSnapshot(ctx context.Context, tx pgx.Tx, accountID, id string) error {
	if !isUUID(id) {
		return ErrSnapshotUnavailable
	}
	row, err := s.q.GetSandboxSnapshot(ctx, tx, sqlc.GetSandboxSnapshotParams{SnapshotID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSnapshotUnavailable
	}
	if err != nil {
		return fmt.Errorf("read sandbox snapshot: %w", err)
	}
	if row.State != SandboxSnapshotReady || row.DeleteRequestedAt.Valid || !row.ExpiresAt.Time.After(time.Now()) {
		return ErrSnapshotUnavailable
	}
	return nil
}

func snapshotFrom(row sqlc.ListSandboxSnapshotsRow) SandboxSnapshot {
	return SandboxSnapshot{ID: row.ID, AccountID: row.AccountID, SandboxID: row.SandboxID, State: row.State, StorageKey: row.StorageKey,
		ReservedBytes: row.ReservedBytes, Bytes: row.Bytes, SHA256: row.Sha256, ExpiresAt: row.ExpiresAt.Time.UTC(),
		DeleteRequestedAt: optTime(row.DeleteRequestedAt), CreatedAt: row.CreatedAt.Time.UTC(), ReadyAt: optTime(row.ReadyAt)}
}
