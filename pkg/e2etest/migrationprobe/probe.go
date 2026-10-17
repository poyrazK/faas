// Package migrationprobe implements the disposable expand/contract SQL workload.
// It never returns driver diagnostics or credential values to a guest response.
package migrationprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var ErrProbe = errors.New("migration SQL qualification failed")

type Config struct {
	RunID, RuntimeURI, ReaderURI, MigrationURI string
	Major                                      int
}
type Result struct {
	Value  string `json:"value"`
	Writes int64  `json:"writes"`
}

func FromEnv(getenv func(string) string) Config {
	major, _ := strconv.Atoi(getenv("POSTGRES_MIGRATION_MAJOR"))
	return Config{RunID: getenv("POSTGRES_MIGRATION_RUN_ID"), RuntimeURI: getenv("DATABASE_URL"), ReaderURI: getenv("READ_DATABASE_URL"), MigrationURI: getenv("MIGRATION_DATABASE_URL"), Major: major}
}
func (c Config) Enabled() bool { return c.RunID != "" }
func (c Config) ID() (pgtype.UUID, error) {
	id, err := uuid.Parse(c.RunID)
	if err != nil || id == uuid.Nil || id.String() != c.RunID || c.Major < 14 {
		return pgtype.UUID{}, ErrProbe
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
func (c Config) Marker() string {
	digest := sha256.Sum256([]byte(c.RunID + "/preserved-data"))
	return hex.EncodeToString(digest[:])
}
func (c Config) SessionName() string { return "gregale-contract:" + c.RunID }
func (c Config) migration(ctx context.Context) (*pgx.Conn, error) {
	if _, err := c.ID(); err != nil || c.MigrationURI == "" {
		return nil, ErrProbe
	}
	if managedpostgres.VerifyCredentialSQL(ctx, c.MigrationURI, managedpostgres.CredentialMigration, c.Major) != nil {
		return nil, ErrProbe
	}
	conn, err := managedpostgres.ConnectCredentialSQL(ctx, c.MigrationURI)
	if err != nil {
		return nil, ErrProbe
	}
	return conn, nil
}
func (c Config) Initialize(ctx context.Context) error {
	conn, err := c.migration(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return c.initialize(ctx, conn)
}

func (c Config) initialize(ctx context.Context, conn *pgx.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProbe
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := probesql.New()
	id, _ := c.ID()
	if q.InitializeLegacy(ctx, tx) != nil || q.SeedLegacy(ctx, tx, probesql.SeedLegacyParams{RunID: id, LegacyValue: c.Marker()}) != nil || tx.Commit(ctx) != nil {
		return ErrProbe
	}
	return nil
}
func (c Config) Expand(ctx context.Context) error {
	conn, err := c.migration(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return c.expand(ctx, conn)
}

func (c Config) expand(ctx context.Context, conn *pgx.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProbe
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := probesql.New()
	if q.ExpandColumn(ctx, tx) != nil || q.BackfillCurrent(ctx, tx) != nil || q.RequireCurrent(ctx, tx) != nil || tx.Commit(ctx) != nil {
		return ErrProbe
	}
	return nil
}

// Contract's DDL is transactional. A disconnected/interrupted execution must
// roll back; uncertainty in the scheduler receipt is never permission to retry.
func (c Config) Contract(ctx context.Context, hold bool) error {
	conn, err := c.migration(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return c.contract(ctx, conn, hold)
}

func (c Config) contract(ctx context.Context, conn *pgx.Conn, hold bool) error {
	q := probesql.New()
	if q.ContractSessionName(ctx, conn, c.SessionName()) != nil {
		return ErrProbe
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProbe
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if q.ContractLegacy(ctx, tx) != nil {
		return ErrProbe
	}
	if hold && q.HoldContractTransaction(ctx, tx) != nil {
		return ErrProbe
	}
	if tx.Commit(ctx) != nil {
		return ErrProbe
	}
	return nil
}
func denied(err error) bool { var e *pgconn.PgError; return errors.As(err, &e) && e.Code == "42501" }

// Check proves serving/manual tasks have no migration grant and cannot use
// their real runtime credential to create schema objects. Old/new code share a
// monotonically advancing counter, but read different immutable schema columns.
func (c Config) Check(ctx context.Context, current bool) (Result, error) {
	id, err := c.ID()
	if err != nil || c.RuntimeURI == "" || c.ReaderURI == "" || c.MigrationURI != "" {
		return Result{}, ErrProbe
	}
	writer, err := managedpostgres.ConnectCredentialSQL(ctx, c.RuntimeURI)
	if err != nil {
		return Result{}, ErrProbe
	}
	defer func() { _ = writer.Close(context.WithoutCancel(ctx)) }()
	q := probesql.New()
	if !denied(q.RuntimeDDL(ctx, writer)) {
		return Result{}, ErrProbe
	}
	result := Result{}
	if current {
		row, readErr := q.AdvanceCurrent(ctx, writer, id)
		err = readErr
		result = Result{Value: row.Value, Writes: row.Writes}
	} else {
		row, readErr := q.AdvanceLegacy(ctx, writer, id)
		err = readErr
		result = Result{Value: row.Value, Writes: row.Writes}
	}
	if err != nil || result.Value != c.Marker() || result.Writes <= 0 {
		return Result{}, ErrProbe
	}
	reader, err := managedpostgres.ConnectCredentialSQL(ctx, c.ReaderURI)
	if err != nil {
		return Result{}, ErrProbe
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	if !denied(q.RuntimeDDL(ctx, reader)) || !denied(q.ReaderWrite(ctx, reader, id)) {
		return Result{}, ErrProbe
	}
	var read Result
	if current {
		row, readErr := q.ReadCurrent(ctx, reader, id)
		err = readErr
		read = Result{Value: row.Value, Writes: row.Writes}
	} else {
		row, readErr := q.ReadLegacy(ctx, reader, id)
		err = readErr
		read = Result{Value: row.Value, Writes: row.Writes}
	}
	if err != nil || read.Value != result.Value || read.Writes < result.Writes {
		return Result{}, ErrProbe
	}
	return result, nil
}
