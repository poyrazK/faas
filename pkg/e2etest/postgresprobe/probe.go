// Package postgresprobe is the disposable SQL workload used by deployed-app
// acceptance. It never prints connection URLs, credentials or driver errors.
package postgresprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	probesql "github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery/sqlc"
)

var ErrProbe = errors.New("PostgreSQL acceptance probe failed")

func probeFailure(stage string) error { return fmt.Errorf("%w: %s", ErrProbe, stage) }

// DiagnosticStage accepts only fixed probe diagnostics. Raw task output and
// database errors must never be copied into acceptance failure reports.
func DiagnosticStage(output string) string {
	text := strings.TrimSpace(output)
	for _, stage := range []string{
		"configuration_major", "configuration_command", "migration_configuration",
		"migration_secret_missing", "migration_credential_invalid", "migration_sql_unavailable",
		"migration_privileges", "migration_verification", "migration_connect",
		"migration_ddl", "migration_seed", "migration_readback",
	} {
		if text == ErrProbe.Error()+": "+stage {
			return stage
		}
	}
	return "unclassified"
}

// Mode accepts only the fixture commands. The release adapter is installed at
// /bin/sh, but deliberately cannot evaluate arbitrary shell source.
func Mode(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", ErrProbe
	}
	if filepath.Base(argv[0]) == "sh" {
		if len(argv) == 3 && argv[1] == "-lc" && argv[2] == "/postgres-probe migrate" {
			return "migrate", nil
		}
		return "", ErrProbe
	}
	if len(argv) == 1 {
		return "serve", nil
	}
	if len(argv) == 2 && (argv[1] == "migrate" || argv[1] == "check-runtime") {
		return argv[1], nil
	}
	return "", ErrProbe
}

type Config struct {
	RunID, RuntimeURI, ReaderURI, MigrationURI string
	PostgresMajor                              int
}

type Result struct {
	Marker       string `json:"marker"`
	Counter      int64  `json:"counter"`
	RuntimeProof string `json:"runtime_proof"`
	ReaderProof  string `json:"reader_proof"`
}

// Proof identifies the private credential actually received by the process.
// The run salt prevents linking evidence across acceptance runs.
func Proof(run, uri string) string {
	hash := sha256.Sum256([]byte(run + "/credential/" + uri))
	return hex.EncodeToString(hash[:])
}

func (c Config) key() (pgtype.UUID, error) {
	id, err := uuid.Parse(c.RunID)
	if err != nil || id == uuid.Nil || id.String() != c.RunID || c.PostgresMajor < 14 {
		return pgtype.UUID{}, ErrProbe
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func (c Config) marker() string {
	hash := sha256.Sum256([]byte(c.RunID + "/workload"))
	return hex.EncodeToString(hash[:])
}

// Migrate is invoked only by the normal deployment release command. Retrying
// never resets the counter or replaces an existing marker.
func (c Config) Migrate(ctx context.Context) error {
	id, err := c.key()
	if err != nil {
		return probeFailure("migration_configuration")
	}
	if c.MigrationURI == "" {
		return probeFailure("migration_secret_missing")
	}
	if err := managedpostgres.VerifyCredentialSQL(ctx, c.MigrationURI, managedpostgres.CredentialMigration, c.PostgresMajor); err != nil {
		switch {
		case errors.Is(err, managedpostgres.ErrInvalid):
			return probeFailure("migration_credential_invalid")
		case errors.Is(err, managedpostgres.ErrUnavailable):
			return probeFailure("migration_sql_unavailable")
		case errors.Is(err, managedpostgres.ErrConflict):
			return probeFailure("migration_privileges")
		default:
			return probeFailure("migration_verification")
		}
	}
	conn, err := managedpostgres.ConnectCredentialSQL(ctx, c.MigrationURI)
	if err != nil {
		return probeFailure("migration_connect")
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	// Static fixture DDL, installed exclusively in the disposable customer DB.
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.gregale_durable_qualification_probe (id uuid PRIMARY KEY, marker text NOT NULL, counter bigint NOT NULL DEFAULT 0)`); err != nil {
		return probeFailure("migration_ddl")
	}
	queries := probesql.New()
	if err := queries.SeedQualificationMarker(ctx, conn, probesql.SeedQualificationMarkerParams{ID: id, Marker: c.marker()}); err != nil {
		return probeFailure("migration_seed")
	}
	row, err := queries.ReadQualificationMarker(ctx, conn, id)
	if err != nil || row.Marker != c.marker() {
		return probeFailure("migration_readback")
	}
	return nil
}

// Check uses fresh TLS sessions on every request. It must not repair a missing
// table or row: a successful request proves data survived deployment/rotation.
func (c Config) Check(ctx context.Context) (Result, error) {
	id, err := c.key()
	if err != nil || c.RuntimeURI == "" || c.ReaderURI == "" || c.MigrationURI != "" {
		return Result{}, ErrProbe
	}
	if managedpostgres.VerifyCredentialSQL(ctx, c.RuntimeURI, managedpostgres.CredentialReadWrite, c.PostgresMajor) != nil || managedpostgres.VerifyCredentialSQL(ctx, c.ReaderURI, managedpostgres.CredentialReadOnly, c.PostgresMajor) != nil {
		return Result{}, ErrProbe
	}
	writer, err := managedpostgres.ConnectCredentialSQL(ctx, c.RuntimeURI)
	if err != nil {
		return Result{}, ErrProbe
	}
	defer func() { _ = writer.Close(context.WithoutCancel(ctx)) }()
	reader, err := managedpostgres.ConnectCredentialSQL(ctx, c.ReaderURI)
	if err != nil {
		return Result{}, ErrProbe
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	queries := probesql.New()
	before, err := queries.ReadQualificationMarker(ctx, reader, id)
	if err != nil || before.Marker != c.marker() {
		return Result{}, ErrProbe
	}
	after, err := queries.AdvanceQualificationMarker(ctx, writer, id)
	if err != nil || after <= before.Counter {
		return Result{}, ErrProbe
	}
	readback, err := queries.ReadQualificationMarker(ctx, reader, id)
	if err != nil || readback.Marker != c.marker() || readback.Counter < after {
		return Result{}, ErrProbe
	}
	return Result{Marker: c.marker(), Counter: after, RuntimeProof: Proof(c.RunID, c.RuntimeURI), ReaderProof: Proof(c.RunID, c.ReaderURI)}, nil
}

// Handler keeps readiness side-effect free. A migration secret in a serving
// process is a hard failure, including health probes and restored snapshots.
func (c Config) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := c.key(); err != nil || c.RuntimeURI == "" || c.ReaderURI == "" || c.MigrationURI != "" {
			http.Error(w, ErrProbe.Error(), http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/probe" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := c.Check(ctx)
		if err != nil {
			http.Error(w, ErrProbe.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			return
		}
	})
}

func FromEnv(getenv func(string) string, major int) Config {
	return Config{RunID: strings.TrimSpace(getenv("POSTGRES_PROBE_RUN_ID")), RuntimeURI: getenv("DATABASE_URL"), ReaderURI: getenv("READ_DATABASE_URL"), MigrationURI: getenv("MIGRATION_DATABASE_URL"), PostgresMajor: major}
}
