// Package releasehistory keeps the platform's release history: one row per
// deploy or rollback on a control-plane host (issue #574). deployctl writes it
// as the schema owner while it holds the host's deploy lock; gregalectl
// release history reads it.
package releasehistory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/releasehistory/sqlc"
)

// Activation kinds.
const (
	KindDeploy   = "deploy"
	KindRollback = "rollback"
)

// Activation statuses. A deploy that failed after it changed the host and
// unwound to the previous release is rolled_back; interrupted is an activation
// whose deployctl stopped before it recorded an outcome.
const (
	StatusInProgress  = "in_progress"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	StatusRolledBack  = "rolled_back"
	StatusInterrupted = "interrupted"
)

// Longest detail and actor the table keeps, in bytes. Longer values are cut.
const (
	MaxDetail = 2000
	MaxActor  = 200
)

// Activation is one deploy or rollback on a host.
type Activation struct {
	Host              string
	Kind              string
	ReleaseID         string
	CommitSHA         string // empty when the release's manifest was unreadable
	PreviousReleaseID string // empty on a host's first activation
	Services          []string
	Actor             string
	StartedAt         time.Time
}

// Record is an activation as the history holds it.
type Record struct {
	ID string
	Activation
	Status     string
	Detail     string
	FinishedAt *time.Time
}

// DB is a connection or pool the history can open a transaction on.
type DB interface {
	sqlc.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store reads and writes the history.
type Store struct {
	db DB
	q  *sqlc.Queries
}

// NewStore returns a Store over db.
func NewStore(db DB) *Store {
	return &Store{db: db, q: sqlc.New()}
}

// Start records a as in progress and returns its id. Any activation the host
// still has in progress is marked interrupted first: the caller holds the
// deploy lock, so no other activation on the host is running.
func (s *Store) Start(ctx context.Context, a Activation) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("releasehistory: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.q.InterruptInProgressActivations(ctx, tx, sqlc.InterruptInProgressActivationsParams{
		FinishedAt: timestamptz(a.StartedAt),
		Host:       a.Host,
	}); err != nil {
		return "", fmt.Errorf("releasehistory: interrupt in-progress activations: %w", err)
	}
	id, err := s.q.StartActivation(ctx, tx, sqlc.StartActivationParams{
		Host:              a.Host,
		Kind:              a.Kind,
		ReleaseID:         a.ReleaseID,
		CommitSha:         optionalText(a.CommitSHA),
		PreviousReleaseID: optionalText(a.PreviousReleaseID),
		Services:          a.Services,
		Actor:             truncate(a.Actor, MaxActor),
		StartedAt:         timestamptz(a.StartedAt),
	})
	if err != nil {
		return "", fmt.Errorf("releasehistory: start activation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("releasehistory: commit: %w", err)
	}
	return id.String(), nil
}

// Finish records how the activation ended. With an empty id (its start could
// not be recorded) it inserts the whole activation instead.
func (s *Store) Finish(ctx context.Context, id string, a Activation, status, detail string, finishedAt time.Time) error {
	if !finalStatus(status) {
		return fmt.Errorf("releasehistory: %q is not a final status", status)
	}
	detail = truncate(detail, MaxDetail)
	if id == "" {
		_, err := s.q.InsertFinishedActivation(ctx, s.db, sqlc.InsertFinishedActivationParams{
			Host:              a.Host,
			Kind:              a.Kind,
			ReleaseID:         a.ReleaseID,
			CommitSha:         optionalText(a.CommitSHA),
			PreviousReleaseID: optionalText(a.PreviousReleaseID),
			Services:          a.Services,
			Actor:             truncate(a.Actor, MaxActor),
			Status:            status,
			Detail:            detail,
			StartedAt:         timestamptz(a.StartedAt),
			FinishedAt:        timestamptz(finishedAt),
		})
		if err != nil {
			return fmt.Errorf("releasehistory: insert finished activation: %w", err)
		}
		return nil
	}
	var uid pgtype.UUID
	if err := uid.Scan(id); err != nil {
		return fmt.Errorf("releasehistory: activation id %q: %w", id, err)
	}
	n, err := s.q.FinishActivation(ctx, s.db, sqlc.FinishActivationParams{
		Status:     status,
		Detail:     detail,
		FinishedAt: timestamptz(finishedAt),
		ID:         uid,
	})
	if err != nil {
		return fmt.Errorf("releasehistory: finish activation: %w", err)
	}
	if n == 0 {
		return ErrNotInProgress
	}
	return nil
}

// ErrNotInProgress is a Finish whose activation is no longer in progress:
// a later activation on the host marked it interrupted.
var ErrNotInProgress = errors.New("releasehistory: activation is no longer in progress")

// List returns the newest activations first, on every host or on host.
func (s *Store) List(ctx context.Context, host string, limit int) ([]Record, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("releasehistory: limit %d is outside 1..1000", limit)
	}
	rows, err := s.q.ListActivations(ctx, s.db, sqlc.ListActivationsParams{
		Host:     optionalText(host),
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("releasehistory: list activations: %w", err)
	}
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		record := Record{
			ID: row.ID.String(),
			Activation: Activation{
				Host:              row.Host,
				Kind:              row.Kind,
				ReleaseID:         row.ReleaseID,
				CommitSHA:         row.CommitSha.String,
				PreviousReleaseID: row.PreviousReleaseID.String,
				Services:          row.Services,
				Actor:             row.Actor,
				StartedAt:         row.StartedAt.Time,
			},
			Status: row.Status,
			Detail: row.Detail,
		}
		if row.FinishedAt.Valid {
			finished := row.FinishedAt.Time
			record.FinishedAt = &finished
		}
		records = append(records, record)
	}
	return records, nil
}

func finalStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusRolledBack:
		return true
	}
	return false
}

func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// truncate makes s storable as text (valid UTF-8, no NUL) and cuts it to at
// most n bytes without splitting a character.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
