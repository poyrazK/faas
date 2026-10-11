package copyarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// RestoreTarget is trusted worker input from an independently owned target.
// Its SQL identifiers and provider pins are private. Neither syntactic validity
// nor an SQL OID proves provider isolation; RestorePlacement must establish it.
type RestoreTarget struct {
	Scope                                                   copyinventory.Scope
	OwnerID, ProviderResourceID, DataResourceID, EndpointID string
	ProviderCreatedAt, EndpointCreatedAt                    time.Time
	DatabaseName, RoleName                                  string
	DatabaseOID, RoleOID                                    uint32
}

func (RestoreTarget) String() string     { return "private PostgreSQL restore target" }
func (t RestoreTarget) GoString() string { return t.String() }
func (RestoreTarget) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ PrivateRestoreTarget bool }{true})
}

// Fingerprint binds all private SQL and provider pins without persisting their
// names. It is an immutable descriptor digest, not proof of provider placement.
func (t RestoreTarget) Fingerprint() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	t.Scope.CapturePoint, t.Scope.SnapshotCreatedAt, t.Scope.CaptureCreatedAt = t.Scope.CapturePoint.UTC(), t.Scope.SnapshotCreatedAt.UTC(), t.Scope.CaptureCreatedAt.UTC()
	t.ProviderCreatedAt, t.EndpointCreatedAt = t.ProviderCreatedAt.UTC(), t.EndpointCreatedAt.UTC()
	// A private alias bypasses the intentionally redacted JSON presentation.
	// The encoded names exist only in this transient hash input.
	type privateTarget RestoreTarget
	raw, err := json.Marshal(privateTarget(t))
	if err != nil {
		return "", pgerrors.ErrInvalid
	}
	hash := sha256.Sum256(append([]byte("gregale-postgres-copy-target-v1\x00"), raw...))
	return hex.EncodeToString(hash[:]), nil
}

// Validate checks descriptor shape and rejects source/target identity aliases.
// A valid descriptor still needs independent provider placement authorization.
func (t RestoreTarget) Validate() error {
	if t.Scope.Validate() != nil || t.DatabaseOID == 0 || t.RoleOID == 0 || !sqlName(t.DatabaseName) || !sqlName(t.RoleName) {
		return pgerrors.ErrInvalid
	}
	id, err := uuid.Parse(t.OwnerID)
	if err != nil || id == uuid.Nil || id.String() != t.OwnerID {
		return pgerrors.ErrInvalid
	}
	for _, source := range []string{t.Scope.OperationID, t.Scope.AccountID, t.Scope.ProjectID, t.Scope.SourceDatabaseID, t.Scope.CaptureDatabaseID} {
		if t.OwnerID == source {
			return pgerrors.ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, pin := range []string{t.ProviderResourceID, t.DataResourceID, t.EndpointID} {
		if !opaquePin(pin) || seen[pin] {
			return pgerrors.ErrInvalid
		}
		seen[pin] = true
		for _, source := range []string{t.Scope.SourceProviderResourceID, t.Scope.SourceDataResourceID, t.Scope.CaptureProviderResourceID, t.Scope.ProviderSnapshotID} {
			if pin == source {
				return pgerrors.ErrInvalid
			}
		}
	}
	for _, at := range []time.Time{t.ProviderCreatedAt, t.EndpointCreatedAt} {
		if at.IsZero() || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 || at.Before(t.Scope.CaptureCreatedAt) || at.After(time.Now()) {
			return pgerrors.ErrInvalid
		}
	}
	if t.EndpointCreatedAt.Before(t.ProviderCreatedAt) {
		return pgerrors.ErrInvalid
	}
	return nil
}

func sqlName(value string) bool {
	return value != "" && len(value) <= 63 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func opaquePin(value string) bool {
	if value == "" || len(value) > 255 || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// RestorePlacement synchronously authenticates the exact target provider,
// endpoint/connection host, creation times and current durable import authority.
// The caller must pin independently observed metadata and reserve dispatch
// before invoking Restore. The callback runs before and after the SQL write.
type RestorePlacement func(context.Context, *pgx.Conn, RestoreTarget) error

// RestoreExecution records only successful command execution and identity
// checks against an authenticated input. It does not attest to cluster globals,
// database configuration, dataset equivalence or stage readiness. A post-write
// error may follow a committed transaction: callers retain uncertain ownership
// and independently recover it, rather than redispatching the same restore.
type RestoreExecution struct {
	Input  Receipt
	Target RestoreTarget
}

func (RestoreExecution) String() string     { return "private PostgreSQL restore execution" }
func (r RestoreExecution) GoString() string { return r.String() }
func (RestoreExecution) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ PrivateRestoreExecution bool }{true})
}

// Restore executes one custom archive in one transaction, preserving ownership
// and ACL entries. No --create/--clean, filter, owner or ACL suppression is used.
// Database-level state and roles require separate original-inventory handling.
// This private primitive is not installed in the clone coordinator.
func (s *StagedArchive) Restore(ctx context.Context, conn *pgx.Conn, target RestoreTarget, pgRestore string, placement RestorePlacement) (RestoreExecution, error) {
	if s == nil || placement == nil || !filepath.IsAbs(pgRestore) || target.Validate() != nil {
		return RestoreExecution{}, pgerrors.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil || s.attempted || !target.Scope.Equal(s.requirement.Scope) || !validRetainedReceipt(s.requirement, s.receipt) {
		return RestoreExecution{}, pgerrors.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return RestoreExecution{}, err
	}
	if err := authenticateRestoreTarget(ctx, conn, target); err != nil {
		return RestoreExecution{}, err
	}
	if err := checkClientTool(ctx, pgRestore, "pg_restore", target.Scope.PostgresMajor); err != nil {
		return RestoreExecution{}, err
	}
	if err := checkRestorePlacement(ctx, conn, target, placement); err != nil {
		return RestoreExecution{}, err
	}
	if err := authenticateRestoreTarget(ctx, conn, target); err != nil {
		return RestoreExecution{}, err
	}
	dsn, env, err := restoreConnection(conn.Config(), target.Scope.PostgresMajor)
	if err != nil {
		return RestoreExecution{}, err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return RestoreExecution{}, pgerrors.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return RestoreExecution{}, err
	}
	// A process failure, cancellation or lost reply cannot establish absence of
	// commit. Durable authority belongs to the caller; this object also prevents
	// repeating an attempted write within the process.
	s.attempted = true
	cmd := exec.CommandContext(ctx, pgRestore, "--format=custom", "--exit-on-error", "--single-transaction", "--no-password", "--dbname="+dsn)
	cmd.Env, cmd.Stdin = env, s.file
	stdout, stderr := &diagnosticWriter{}, &diagnosticWriter{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return RestoreExecution{}, ctx.Err()
		}
		return RestoreExecution{}, pgerrors.ErrUnavailable
	}
	if stdout.bytes != 0 || stderr.bytes != 0 {
		return RestoreExecution{}, pgerrors.ErrUnsupported
	}
	if err := checkRestorePlacement(ctx, conn, target, placement); err != nil {
		return RestoreExecution{}, err
	}
	if err := authenticateRestoreTarget(ctx, conn, target); err != nil {
		return RestoreExecution{}, err
	}
	return RestoreExecution{Input: s.receipt, Target: target}, nil
}

func authenticateRestoreTarget(ctx context.Context, conn *pgx.Conn, t RestoreTarget) error {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
		return pgerrors.ErrConflict
	}
	a, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return pgerrors.ErrUnavailable
	}
	if int(a.ServerVersion/10000) != t.Scope.PostgresMajor || a.DatabaseName != t.DatabaseName || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != t.DatabaseOID ||
		!a.RoleOid.Valid || a.RoleOid.Uint32 != t.RoleOID || a.RoleName != t.RoleName || a.SessionRole != t.RoleName || conn.Config().User != t.RoleName || a.ReadOnly {
		return pgerrors.ErrConflict
	}
	return nil
}

func checkRestorePlacement(ctx context.Context, conn *pgx.Conn, t RestoreTarget, placement RestorePlacement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := placement(ctx, conn, t)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	for _, kind := range []error{pgerrors.ErrInvalid, pgerrors.ErrConflict, pgerrors.ErrUnsupported, pgerrors.ErrNotFound, pgerrors.ErrQuotaExceeded, pgerrors.ErrUsageStale} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return pgerrors.ErrUnavailable
}

func restoreConnection(cfg *pgx.ConnConfig, major int) (string, []string, error) {
	dsn, env, err := dumpConnection(cfg, major)
	if err != nil {
		return "", nil, err
	}
	for n := range env {
		if strings.HasPrefix(env[n], "PGAPPNAME=") {
			env[n] = "PGAPPNAME=gregale-stage-copy-import"
		}
		if strings.HasPrefix(env[n], "PGOPTIONS=") {
			env[n] = "PGOPTIONS=-c default_transaction_read_only=off -c search_path=pg_catalog"
		}
	}
	return dsn, env, nil
}
