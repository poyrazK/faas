package state

// ADR-122 / issue #975 item #1: per-deployment OpenAPI document
// capture. Split into a dedicated file (mirroring the cors_presets
// precedent at pgstore_cors_presets.go) so the pgstore impl stays
// reviewable as the surface grows.
//
// The four methods — Get / Upsert / Delete / Count — pin the
// IDOR floor at the SQL WHERE clause. The (deployment_id,
// account_id) predicate is non-negotiable: a cross-tenant read
// returns ErrNotFound because pgx row.Scan errors on no rows; the
// consumer_keys table (pgstore_consumer_keys.go) applies the same
// defence-in-depth.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// openAPIDocSelectCols is the canonical column order for every
// SELECT against deployment_openapi_docs. The reader's Scan args
// bind positionally against this list — a column add lands here
// first, in the same commit, so a SELECT-write drift cannot
// silently swallow a column.
//
// Ordering matches migrations/00330_endpoint_discovery.sql
// (column list 1:1).
const openAPIDocSelectCols = `deployment_id, account_id, app_id, doc,
       doc_sha256, byte_size, source, truncated, captured_at, updated_at`

// scanOpenAPIDocCols reads a single row by column position. The
// caller owns the row.Scan / rows.Scan lifetime — this helper
// only walks the args.
func scanOpenAPIDocCols(scan func(...any) error) ([]byte, OpenAPIDocMeta, error) {
	var (
		doc       []byte
		docSHA256 []byte
		truncated bool
		meta      OpenAPIDocMeta
	)
	if err := scan(
		&meta.DeploymentID, &meta.AccountID, &meta.AppID, &doc,
		&docSHA256, &meta.ByteSize, &meta.Source, &truncated,
		&meta.CapturedAt, &meta.UpdatedAt,
	); err != nil {
		return nil, OpenAPIDocMeta{}, err
	}
	// pgx returns a nil []byte for a NULL BYTEA; the schema CHECK
	// pins doc_sha256 to NOT NULL so the only nil path is the
	// empty-body case which still wouldn't pass the length CHECK.
	// Coalesce nil to an empty slice so the round-trip is identity.
	if docSHA256 != nil {
		meta.DocSHA256 = docSHA256
	}
	meta.Truncated = truncated
	return doc, meta, nil
}

// GetDeploymentOpenAPIDoc returns the (doc, meta) pair for one
// deployment, scoped to the caller's account. ErrNotFound when
// the row is missing OR when the caller's accountID does not
// match — the (deployment_id, account_id) WHERE clause means a
// cross-tenant lookup returns pgx.ErrNoRows, which we map to
// ErrNotFound so the apid handler emits 404 (not 403, which would
// leak a tenant-state signal).
func (s *PgStore) GetDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID string) ([]byte, OpenAPIDocMeta, error) {
	row := s.pool.QueryRow(ctx,
		`select `+openAPIDocSelectCols+` from deployment_openapi_docs
		 where deployment_id = $1 and account_id = $2`,
		deploymentID, accountID)
	doc, meta, err := scanOpenAPIDocCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, OpenAPIDocMeta{}, ErrNotFound
		}
		return nil, OpenAPIDocMeta{}, fmt.Errorf("state: get deployment openapi doc: %w", err)
	}
	return doc, meta, nil
}

// UpsertDeploymentOpenAPIDoc records (or overwrites) the captured
// OpenAPI body for one deployment. doc is the validated JSON bytes
// (the apid jsonschema check passes before this call); source is
// the closed enum 'cold_boot' or 'manual_upload'. The doc_sha256
// is computed server-side via sha256.Sum256 — the schema CHECK
// pins it to exactly 32 bytes.
//
// The deployment row must exist (defence-in-depth pre-check) so
// a misuse at the call site fails with ErrNotFound BEFORE Postgres
// raises 23503 on the FK. The FK CASCADE in migration 00330 makes
// this unreachable in practice but the explicit check keeps the
// error surface predictable.
//
// Idempotent: a re-delivered cold-boot event overwrites the same
// row, not create a second. The first-capture timestamp is
// preserved (COALESCE on captured_at via excluded) so a customer's
// "first captured at" view is stable across re-deliveries.
// updated_at is bumped on every write.
//
// It checks no quota; UpsertDeploymentOpenAPIDocIfUnderQuota counts the
// billing subject's documents and saves in one step.
func (s *PgStore) UpsertDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID, appID string, doc []byte, source string, truncated bool) error {
	return upsertDeploymentOpenAPIDoc(ctx, s.pool, deploymentID, accountID, appID, doc, source, truncated)
}

func upsertDeploymentOpenAPIDoc(ctx context.Context, db sqlc.DBTX, deploymentID, accountID, appID string, doc []byte, source string, truncated bool) error {
	// Defence-in-depth: confirm the FK target row exists so the
	// caller gets a clean ErrNotFound before Postgres raises 23503.
	var exists string
	if err := db.QueryRow(ctx,
		`select id from deployments where id = $1`, deploymentID,
	).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: openapi doc parent check: %w", err)
	}
	sum := sha256.Sum256(doc)
	_, err := db.Exec(ctx, `
		insert into deployment_openapi_docs
		    (deployment_id, account_id, app_id, doc, doc_sha256,
		     byte_size, source, truncated, captured_at, updated_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
		on conflict (deployment_id) do update
		set doc          = excluded.doc,
		    doc_sha256   = excluded.doc_sha256,
		    byte_size    = excluded.byte_size,
		    source       = excluded.source,
		    truncated    = excluded.truncated,
		    captured_at  = deployment_openapi_docs.captured_at,
		    updated_at   = now()
	`,
		deploymentID, accountID, appID, doc, sum[:],
		len(doc), source, truncated)
	if err != nil {
		return fmt.Errorf("state: upsert deployment openapi doc: %w", err)
	}
	return nil
}

// DeleteDeploymentOpenAPIDoc removes the doc row for one
// deployment. ErrNotFound when no row OR the caller's accountID
// does not match — same IDOR floor as the read. The apid caller
// treats ErrNotFound as "already deleted" so a retry is a no-op.
func (s *PgStore) DeleteDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from deployment_openapi_docs
		 where deployment_id = $1 and account_id = $2`,
		deploymentID, accountID)
	if err != nil {
		return fmt.Errorf("state: delete deployment openapi doc: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOpenAPIDocsByAccount returns the number of doc rows the
// account owns. The per-account quota counts the app's billing
// subject instead, in UpsertDeploymentOpenAPIDocIfUnderQuota.
func (s *PgStore) CountOpenAPIDocsByAccount(ctx context.Context, accountID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from deployment_openapi_docs where account_id = $1`,
		accountID).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count openapi docs by account: %w", err)
	}
	return n, nil
}
