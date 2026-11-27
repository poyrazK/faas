// Package postgresexport owns the job lease and private artifact publication
// for customer-managed logical database exports.
package postgresexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/storage"
)

type Source interface {
	ExportSource(context.Context, string, string) (managedpostgres.Database, error)
	ExportDatabase(context.Context, string, string, int64, io.Writer) (pgexport.Source, error)
}

type Service struct {
	pool       *pgxpool.Pool
	artifacts  storage.StorageBackend
	source     Source
	recipient  func() *age.X25519Recipient
	identities func() []*age.X25519Identity
}

func New(pool *pgxpool.Pool, artifacts storage.StorageBackend, source Source, recipient func() *age.X25519Recipient, identities func() []*age.X25519Identity) *Service {
	if pool == nil || artifacts == nil || source == nil || recipient == nil || identities == nil {
		return nil
	}
	return &Service{pool: pool, artifacts: artifacts, source: source, recipient: recipient, identities: identities}
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (s *Service) Start(ctx context.Context, owner, database, id string) (api.ManagedPostgresExport, error) {
	if !validID(id) || !validID(database) {
		return api.ManagedPostgresExport{}, managedpostgres.ErrInvalid
	}
	if s.recipient() == nil {
		return api.ManagedPostgresExport{}, managedpostgres.ErrUnavailable
	}
	source, err := s.source.ExportSource(ctx, owner, database)
	if err != nil {
		return api.ManagedPostgresExport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ManagedPostgresExport{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('native-postgres-export:' || $1 || ':' || $2, 0))`, owner, source.OrgID); err != nil {
		return api.ManagedPostgresExport{}, err
	}
	job, _, err := read(ctx, tx, owner, source.OrgID, database, id)
	if err == nil {
		return job, tx.Commit(ctx)
	}
	if !errors.Is(err, managedpostgres.ErrNotFound) {
		return api.ManagedPostgresExport{}, err
	}
	res, err := tx.Exec(ctx, `INSERT INTO native_postgres_exports (id, database_id, owner_id, org_id, generation, state, expires_at)
		SELECT $1, $2, $3, $4, $5, 'queued', clock_timestamp() + interval '3 hours'
		WHERE (SELECT count(*) FROM native_postgres_exports WHERE owner_id=$3 AND org_id=$4 AND expires_at > clock_timestamp()) < 4
		ON CONFLICT DO NOTHING`, id, database, owner, source.OrgID, source.ObservedGeneration)
	if err != nil {
		return api.ManagedPostgresExport{}, err
	}
	if res.RowsAffected() != 1 {
		return api.ManagedPostgresExport{}, managedpostgres.ErrConflict
	}
	out, _, err := read(ctx, tx, owner, source.OrgID, database, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

type rowReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func read(ctx context.Context, db rowReader, owner, org, database, id string) (api.ManagedPostgresExport, string, error) {
	var result api.ManagedPostgresExport
	var artifactToken string
	var raw []byte
	err := db.QueryRow(ctx, `SELECT id::text, state, expires_at, manifest, coalesce(artifact_token::text, '') FROM native_postgres_exports
		WHERE id=$1 AND database_id=$2 AND owner_id=$3 AND org_id=$4 AND expires_at > clock_timestamp()`, id, database, owner, org).
		Scan(&result.ID, &result.State, &result.ExpiresAt, &raw, &artifactToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, "", managedpostgres.ErrNotFound
	}
	if err == nil && raw != nil {
		err = json.Unmarshal(raw, &result.Manifest)
	}
	return result, artifactToken, err
}

func (s *Service) Status(ctx context.Context, owner, org, database, id string) (api.ManagedPostgresExport, error) {
	if !validID(id) || !validID(database) {
		return api.ManagedPostgresExport{}, managedpostgres.ErrInvalid
	}
	job, _, err := read(ctx, s.pool, owner, org, database, id)
	return job, err
}

func (s *Service) Open(ctx context.Context, owner, org, database, id string) (api.ManagedPostgresExportManifest, io.ReadCloser, error) {
	if !validID(id) || !validID(database) {
		return api.ManagedPostgresExportManifest{}, nil, managedpostgres.ErrInvalid
	}
	job, token, err := read(ctx, s.pool, owner, org, database, id)
	if err != nil {
		return api.ManagedPostgresExportManifest{}, nil, err
	}
	if job.Manifest == nil || job.State != "ready" || !validID(token) {
		return api.ManagedPostgresExportManifest{}, nil, managedpostgres.ErrConflict
	}
	body, err := s.artifacts.Get(ctx, storageKey(id, token))
	if err != nil {
		return api.ManagedPostgresExportManifest{}, nil, managedpostgres.ErrUnavailable
	}
	var identities []age.Identity
	for _, id := range s.identities() {
		if id != nil {
			identities = append(identities, id)
		}
	}
	plain, err := age.Decrypt(body, identities...)
	if err != nil {
		_ = body.Close()
		return api.ManagedPostgresExportManifest{}, nil, managedpostgres.ErrUnavailable
	}
	return *job.Manifest, decryptedExport{Reader: plain, Closer: body}, nil
}

type decryptedExport struct {
	io.Reader
	io.Closer
}

func storageKey(id, token string) string { return "postgres-exports/" + id + "/" + token + ".dump.age" }

type claim struct {
	id, owner, org, database, token string
	generation                      int64
}

func (s *Service) claim(ctx context.Context) (claim, error) {
	job := claim{token: uuid.NewString()}
	err := s.pool.QueryRow(ctx, `WITH claimed AS (
		UPDATE native_postgres_exports SET state='running', lease_token=$1,
			lease_until=clock_timestamp() + interval '32 minutes'
			WHERE id=(SELECT id FROM native_postgres_exports
				WHERE expires_at > clock_timestamp() + interval '32 minutes'
				AND (state='queued' OR (state='running' AND lease_until <= clock_timestamp()))
				ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id, owner_id, org_id, database_id, generation, lease_until
		), reserved AS (
			INSERT INTO native_postgres_export_objects (export_id, token, expires_at)
			SELECT id, $1, lease_until + interval '1 hour' FROM claimed
			RETURNING export_id
		)
		SELECT id::text, owner_id, org_id, database_id::text, generation FROM claimed JOIN reserved ON reserved.export_id=claimed.id`, job.token).
		Scan(&job.id, &job.owner, &job.org, &job.database, &job.generation)
	return job, err
}

func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.expire(ctx); err != nil && ctx.Err() == nil {
			log.Warn("PostgreSQL export expiry failed", "err", err)
		}
		job, err := s.claim(ctx)
		switch {
		case err == nil:
			if err := s.run(ctx, job); err != nil && ctx.Err() == nil {
				log.Warn("PostgreSQL export failed", "export_id", job.id, "err", err)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil:
			log.Warn("PostgreSQL export claim failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) run(ctx context.Context, job claim) error {
	exportCtx, cancel := context.WithTimeout(managedpostgres.WithOwnership(ctx, job.org, job.owner), pgexport.Timeout)
	defer cancel()
	manifest, captureErr := s.publish(exportCtx, job)
	if ctx.Err() != nil {
		// Retain the durable lease on shutdown. Another worker can retry it.
		return ctx.Err()
	}
	settle, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	if captureErr != nil {
		// Failed output is never downloadable. If object deletion fails, the
		// expiry pass retries its cleanup while the database row still exists.
		_ = s.artifacts.Delete(settle, storageKey(job.id, job.token))
		_, err := s.pool.Exec(settle, `UPDATE native_postgres_exports SET state='failed',
			lease_token=NULL, lease_until=NULL, expires_at=clock_timestamp() + interval '1 hour'
			WHERE id=$1 AND lease_token=$2 AND lease_until > clock_timestamp()`, job.id, job.token)
		return errors.Join(captureErr, err)
	}
	result, err := s.pool.Exec(settle, `WITH ready AS (
		UPDATE native_postgres_exports SET state='ready', manifest=$3, artifact_token=$2,
			lease_token=NULL, lease_until=NULL, expires_at=clock_timestamp() + interval '1 hour'
			WHERE id=$1 AND lease_token=$2 AND lease_until > clock_timestamp()
			RETURNING expires_at
		)
		UPDATE native_postgres_export_objects SET expires_at=ready.expires_at FROM ready WHERE token=$2`, job.id, job.token, manifest)
	if err == nil && result.RowsAffected() != 1 {
		return managedpostgres.ErrConflict
	}
	return err
}

func (s *Service) publish(ctx context.Context, job claim) (api.ManagedPostgresExportManifest, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	key := s.recipient()
	if key == nil {
		return api.ManagedPostgresExportManifest{}, managedpostgres.ErrUnavailable
	}
	reader, writer := io.Pipe()
	type result struct {
		manifest api.ManagedPostgresExportManifest
		err      error
	}
	done := make(chan result, 1)
	go func() {
		encrypted, err := age.Encrypt(writer, key)
		if err != nil {
			_ = writer.CloseWithError(err)
			done <- result{err: err}
			return
		}
		hash := sha256.New()
		dump := &dumpWriter{out: &pgexport.BoundedWriter{Writer: io.MultiWriter(encrypted, hash)}}
		source, err := s.source.ExportDatabase(ctx, job.owner, job.database, job.generation, dump)
		if err == nil && (!source.Valid() || !bytes.Equal(dump.prefix, []byte("PGDMP"))) {
			err = pgexport.ErrIncomplete
		}
		manifest := api.ManagedPostgresExportManifest{PostgresMajor: source.Major, CapturedAt: source.CapturedAt, DatabaseBytes: source.DatabaseBytes, VectorSchema: source.VectorSchema,
			SizeBytes: dump.out.Bytes, SHA256: hex.EncodeToString(hash.Sum(nil)),
			Extensions: make([]api.ManagedPostgresExtensionVersion, 0, len(source.Extensions))}
		for _, extension := range source.Extensions {
			manifest.Extensions = append(manifest.Extensions, api.ManagedPostgresExtensionVersion{Name: extension.Name, Version: extension.Version})
		}
		if err == nil {
			err = encrypted.Close()
			for _, identity := range s.identities() {
				if identity != nil && identity.Recipient().String() == key.String() {
					manifest.RestoreReceipt = manifest.ArchiveManifest().Receipt(identity.String())
					break
				}
			}
		}
		_ = writer.CloseWithError(err)
		done <- result{manifest, err}
	}()
	storageErr := s.artifacts.Put(ctx, storageKey(job.id, job.token), reader)
	// Unblock the producer if the artifact backend returns early.
	_ = reader.Close()
	if storageErr != nil {
		cancel()
	}
	export := <-done
	return export.manifest, errors.Join(storageErr, export.err)
}

type dumpWriter struct {
	out    *pgexport.BoundedWriter
	prefix []byte
}

func (w *dumpWriter) Write(p []byte) (int, error) {
	if len(w.prefix) < 5 {
		w.prefix = append(w.prefix, p[:min(len(p), 5-len(w.prefix))]...)
		if len(w.prefix) == 5 && string(w.prefix) != "PGDMP" {
			return 0, pgexport.ErrIncomplete
		}
	}
	return w.out.Write(p)
}

func (s *Service) expire(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `SELECT export_id::text, token::text FROM native_postgres_export_objects
		WHERE expires_at <= clock_timestamp() ORDER BY expires_at LIMIT 16 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type object struct{ id, token string }
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (object, error) {
		var item object
		err := row.Scan(&item.id, &item.token)
		return item, err
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.artifacts.Delete(ctx, storageKey(item.id, item.token)); err != nil && !storage.IsNotFound(err) {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM native_postgres_export_objects WHERE token=$1", item.token); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM native_postgres_exports WHERE id IN (
		SELECT id FROM native_postgres_exports WHERE expires_at <= clock_timestamp()
		AND (lease_until IS NULL OR lease_until <= clock_timestamp()) LIMIT 16 FOR UPDATE SKIP LOCKED)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
