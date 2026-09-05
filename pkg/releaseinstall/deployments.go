package releaseinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeploymentStatus is the closed set stored in daemon_deployments.status.
type DeploymentStatus string

const (
	DeploymentInProgress DeploymentStatus = "in_progress"
	DeploymentSucceeded  DeploymentStatus = "succeeded"
	DeploymentRolledBack DeploymentStatus = "rolled_back"
	DeploymentFailed     DeploymentStatus = "failed"
)

// DeploymentKind is the operator-visible reason a daemon row was created.
type DeploymentKind string

const (
	DeploymentInstall   DeploymentKind = "install"
	DeploymentDeploy    DeploymentKind = "deploy"
	DeploymentRollback  DeploymentKind = "rollback"
	DeploymentBootstrap DeploymentKind = "bootstrap"
	DeploymentReconcile DeploymentKind = "reconcile"
)

// DeploymentRecord is the input for a new daemon deployment attempt.
// Version is the daemon's content hash; CommitSHA identifies the immutable
// platform release that supplied it.
type DeploymentRecord struct {
	Daemon     string
	Version    string
	CommitSHA  string
	SignedBy   string
	SBOMSHA256 string
	DeployedBy string
	DeployKind DeploymentKind
	Supersedes string
	Notes      map[string]any
}

// DeploymentRow is the durable operator history shape.
type DeploymentRow struct {
	ID          string           `json:"id"`
	Daemon      string           `json:"daemon"`
	Version     string           `json:"version"`
	CommitSHA   string           `json:"commit_sha"`
	SignedBy    *string          `json:"signed_by,omitempty"`
	SBOMSHA256  *string          `json:"sbom_sha256,omitempty"`
	DeployedBy  string           `json:"deployed_by"`
	DeployedAt  time.Time        `json:"deployed_at"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	DeployKind  DeploymentKind   `json:"deploy_kind"`
	Supersedes  *string          `json:"supersedes,omitempty"`
	Status      DeploymentStatus `json:"status"`
	Notes       map[string]any   `json:"notes,omitempty"`
}

// DeploymentStore is deliberately separate from Store. Existing release
// bundle consumers can keep their small fake Store while operator history is
// adopted incrementally by deploy/install paths.
type DeploymentStore interface {
	Begin(ctx context.Context, record DeploymentRecord) (string, error)
	Complete(ctx context.Context, id string, status DeploymentStatus, notes map[string]any) error
	// RecordSucceeded writes a complete release attempt atomically. It is the
	// safe convenience used by one-shot installers after their activation and
	// readiness gates have passed.
	RecordSucceeded(ctx context.Context, records []DeploymentRecord) error
	List(ctx context.Context, daemon string, limit int) ([]DeploymentRow, error)
	Get(ctx context.Context, id string) (DeploymentRow, error)
}

var (
	ErrDeploymentNotFound = errors.New("releaseinstall: deployment not found")
	ErrInvalidDeployment  = errors.New("releaseinstall: invalid deployment record")
)

type deploymentStore struct{ pool *pgxpool.Pool }

// NewDeploymentStore returns a PostgreSQL-backed operator release ledger.
func NewDeploymentStore(pool *pgxpool.Pool) DeploymentStore {
	return deploymentStore{pool: pool}
}

func (s deploymentStore) Begin(ctx context.Context, record DeploymentRecord) (string, error) {
	if err := ValidateDeploymentRecord(record); err != nil {
		return "", err
	}
	notes, err := encodeDeploymentNotes(record.Notes)
	if err != nil {
		return "", err
	}
	var supersedes any
	if strings.TrimSpace(record.Supersedes) != "" {
		supersedes = record.Supersedes
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		insert into daemon_deployments
		    (daemon, version, commit_sha, signed_by, sbom_sha256,
		     deployed_by, deploy_kind, supersedes, notes)
		values ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8, $9::jsonb)
		returning id
	`, strings.TrimSpace(record.Daemon), strings.TrimSpace(record.Version),
		record.CommitSHA, strings.TrimSpace(record.SignedBy), strings.TrimSpace(record.SBOMSHA256),
		strings.TrimSpace(record.DeployedBy), string(record.DeployKind), supersedes, string(notes)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: begin daemon deployment: %w", err)
	}
	return id, nil
}

func (s deploymentStore) Complete(ctx context.Context, id string, status DeploymentStatus, notes map[string]any) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidDeployment)
	}
	if !validDeploymentStatus(status) || status == DeploymentInProgress {
		return fmt.Errorf("%w: invalid terminal status %q", ErrInvalidDeployment, status)
	}
	body, err := encodeDeploymentNotes(notes)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `
		update daemon_deployments
		   set status = $2,
		       completed_at = now(),
		       notes = $3::jsonb
		 where id = $1
		   and status = 'in_progress'
	`, id, string(status), string(body))
	if err != nil {
		return fmt.Errorf("releaseinstall: complete daemon deployment: %w", err)
	}
	if result.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from daemon_deployments where id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("releaseinstall: check daemon deployment: %w", err)
		}
		if !exists {
			return ErrDeploymentNotFound
		}
		// A retry after the original completion is idempotent only when it
		// asks for the same terminal state; the database row remains the
		// source of truth and no timestamp is rewritten.
		var current DeploymentStatus
		if err := s.pool.QueryRow(ctx, `select status from daemon_deployments where id = $1`, id).Scan(&current); err != nil {
			return fmt.Errorf("releaseinstall: read daemon deployment status: %w", err)
		}
		if current != status {
			return fmt.Errorf("%w: deployment %s already %s", ErrInvalidDeployment, id, current)
		}
	}
	return nil
}

func (s deploymentStore) RecordSucceeded(ctx context.Context, records []DeploymentRecord) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: empty release", ErrInvalidDeployment)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("releaseinstall: begin deployment ledger transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, record := range records {
		if err := ValidateDeploymentRecord(record); err != nil {
			return err
		}
		notes, err := encodeDeploymentNotes(record.Notes)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into daemon_deployments
			    (daemon, version, commit_sha, signed_by, sbom_sha256,
			     deployed_by, deployed_at, completed_at, deploy_kind,
			     supersedes, status, notes)
			values ($1, $2, $3, $4, $5, $6, now(), now(), $7, $8, 'succeeded', $9::jsonb)
		`, strings.TrimSpace(record.Daemon), strings.TrimSpace(record.Version),
			record.CommitSHA, strings.TrimSpace(record.SignedBy), strings.TrimSpace(record.SBOMSHA256),
			strings.TrimSpace(record.DeployedBy), string(record.DeployKind), nullableUUID(record.Supersedes), string(notes)); err != nil {
			return fmt.Errorf("releaseinstall: record daemon deployment: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("releaseinstall: commit deployment ledger transaction: %w", err)
	}
	return nil
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func (s deploymentStore) List(ctx context.Context, daemon string, limit int) ([]DeploymentRow, error) {
	limit = NormalizeDeploymentHistoryLimit(limit)
	query := `
		select id, daemon, version, commit_sha, signed_by, sbom_sha256,
		       deployed_by, deployed_at, completed_at, deploy_kind,
		       supersedes, status, notes
		  from daemon_deployments
		 where ($1 = '' or daemon = $1)
		 order by deployed_at desc
		 limit $2
	`
	rows, err := s.pool.Query(ctx, query, strings.TrimSpace(daemon), limit)
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: list daemon deployments: %w", err)
	}
	defer rows.Close()
	result := make([]DeploymentRow, 0, limit)
	for rows.Next() {
		row, err := scanDeploymentRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("releaseinstall: scan daemon deployment: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("releaseinstall: iterate daemon deployments: %w", err)
	}
	return result, nil
}

func (s deploymentStore) Get(ctx context.Context, id string) (DeploymentRow, error) {
	row := s.pool.QueryRow(ctx, `
		select id, daemon, version, commit_sha, signed_by, sbom_sha256,
		       deployed_by, deployed_at, completed_at, deploy_kind,
		       supersedes, status, notes
		  from daemon_deployments
		 where id = $1
	`, id)
	result, err := scanDeploymentRow(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRow{}, ErrDeploymentNotFound
	}
	if err != nil {
		return DeploymentRow{}, fmt.Errorf("releaseinstall: get daemon deployment: %w", err)
	}
	return result, nil
}

type deploymentScanner func(...any) error

func scanDeploymentRow(scan deploymentScanner) (DeploymentRow, error) {
	var (
		row        DeploymentRow
		kind       string
		status     string
		notesBytes []byte
	)
	if err := scan(&row.ID, &row.Daemon, &row.Version, &row.CommitSHA, &row.SignedBy,
		&row.SBOMSHA256, &row.DeployedBy, &row.DeployedAt, &row.CompletedAt,
		&kind, &row.Supersedes, &status, &notesBytes); err != nil {
		return DeploymentRow{}, err
	}
	row.DeployKind = DeploymentKind(kind)
	row.Status = DeploymentStatus(status)
	if len(notesBytes) == 0 {
		row.Notes = map[string]any{}
	} else if err := json.Unmarshal(notesBytes, &row.Notes); err != nil {
		return DeploymentRow{}, fmt.Errorf("decode notes: %w", err)
	}
	return row, nil
}

func ValidateDeploymentRecord(record DeploymentRecord) error {
	if strings.TrimSpace(record.Daemon) == "" || strings.TrimSpace(record.Version) == "" ||
		!ValidGitSHA(record.CommitSHA) || strings.TrimSpace(record.DeployedBy) == "" ||
		!validDeploymentKind(record.DeployKind) {
		return fmt.Errorf("%w: daemon, version, commit_sha, deployed_by, and deploy_kind are required", ErrInvalidDeployment)
	}
	if record.SBOMSHA256 != "" && !validManifestHash(record.SBOMSHA256) {
		return fmt.Errorf("%w: sbom_sha256 must be sha256:<64hex>", ErrInvalidDeployment)
	}
	return nil
}

func NormalizeDeploymentHistoryLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func validDeploymentStatus(status DeploymentStatus) bool {
	switch status {
	case DeploymentInProgress, DeploymentSucceeded, DeploymentRolledBack, DeploymentFailed:
		return true
	default:
		return false
	}
}

func validDeploymentKind(kind DeploymentKind) bool {
	switch kind {
	case DeploymentInstall, DeploymentDeploy, DeploymentRollback, DeploymentBootstrap, DeploymentReconcile:
		return true
	default:
		return false
	}
}

func encodeDeploymentNotes(notes map[string]any) ([]byte, error) {
	if notes == nil {
		notes = map[string]any{}
	}
	body, err := json.Marshal(notes)
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: encode deployment notes: %w", err)
	}
	return body, nil
}
