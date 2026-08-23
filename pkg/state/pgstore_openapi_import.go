package state

// ADR-126 / issue #975 item #2: per-app OpenAPI document import.
// Split into a dedicated file (mirroring the
// pgstore_endpoint_discovery.go precedent from item #1) so the
// pgstore impl stays reviewable as the surface grows.
//
// The four methods — Get / Upsert / Delete / Count — pin the
// IDOR floor at the SQL WHERE clause. The (app_id, account_id)
// predicate is non-negotiable: a cross-tenant read returns
// ErrNotFound because pgx row.Scan errors on no rows; the
// deployment-keyed methods from item #1 apply the same
// defence-in-depth.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// appOpenAPIDocSelectCols is the canonical column order for every
// SELECT against app_openapi_docs. The reader's Scan args bind
// positionally against this list — a column add lands here first,
// in the same commit, so a SELECT-write drift cannot silently
// swallow a column.
//
// Ordering matches migrations/00383_openapi_import.sql (column
// list 1:1).
const appOpenAPIDocSelectCols = `app_id, account_id, doc,
       doc_sha256, byte_size, endpoint_count, source,
       openapi_version, captured_at, updated_at`

// scanAppOpenAPIDocCols reads a single row by column position.
// The caller owns the row.Scan / rows.Scan lifetime — this helper
// only walks the args.
func scanAppOpenAPIDocCols(scan func(...any) error) ([]byte, AppOpenAPIDocMeta, error) {
	var (
		doc          []byte
		docSHA256    []byte
		source       string
		openapiVer   string
		endpointCt   int
		meta         AppOpenAPIDocMeta
	)
	if err := scan(
		&meta.AppID, &meta.AccountID, &doc, &docSHA256,
		&meta.ByteSize, &endpointCt, &source, &openapiVer,
		&meta.CapturedAt, &meta.UpdatedAt,
	); err != nil {
		return nil, AppOpenAPIDocMeta{}, err
	}
	// pgx returns a nil []byte for a NULL BYTEA; the schema CHECK
	// pins doc_sha256 to NOT NULL so the only nil path is the
	// empty-body case which still wouldn't pass the length CHECK.
	// Coalesce nil to an empty slice so the round-trip is identity.
	if docSHA256 != nil {
		meta.DocSHA256 = docSHA256
	}
	meta.Source = source
	meta.OpenAPIVersion = openapiVer
	meta.EndpointCount = endpointCt
	return doc, meta, nil
}

// GetAppOpenAPIDoc returns the (doc, meta) pair for one app,
// scoped to the caller's account. ErrNotFound when the row is
// missing OR when the caller's accountID does not match — the
// (app_id, account_id) WHERE clause means a cross-tenant lookup
// returns pgx.ErrNoRows, which we map to ErrNotFound so the apid
// handler emits 404 (not 403, which would leak a tenant-state
// signal).
func (s *PgStore) GetAppOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error) {
	row := s.pool.QueryRow(ctx,
		`select `+appOpenAPIDocSelectCols+` from app_openapi_docs
		 where app_id = $1 and account_id = $2`,
		appID, accountID)
	doc, meta, err := scanAppOpenAPIDocCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, AppOpenAPIDocMeta{}, ErrNotFound
		}
		return nil, AppOpenAPIDocMeta{}, fmt.Errorf("state: get app openapi doc: %w", err)
	}
	return doc, meta, nil
}

// UpsertAppOpenAPIDoc records (or overwrites) the imported
// OpenAPI body for one app. doc is the meta-schema-validated JSON
// bytes (the apid openapiimport.ValidateImport check passes before
// this call); endpointCount is the pre-computed paths.* operation
// count (the apid computes it after a successful ValidateImport);
// openapiVersion is one of ValidOpenAPIVersions (closed enum,
// SQL CHECK-enforced). The doc_sha256 is computed server-side via
// sha256.Sum256 — the schema CHECK pins it to exactly 32 bytes.
//
// The app row must exist (defence-in-depth pre-check) so a misuse
// at the call site fails with ErrNotFound BEFORE Postgres raises
// 23503 on the FK. The FK CASCADE in migration 00383 makes this
// unreachable in practice but the explicit check keeps the error
// surface predictable.
//
// Idempotent: a re-delivered import overwrites the same row, not
// creates a second one. The first-import timestamp is preserved
// (COALESCE on captured_at via excluded) so a customer's "first
// imported at" view is stable across re-deliveries. updated_at is
// bumped on every write.
//
// The per-account quota gate is upstream — the apid calls
// CountOpenAPIImportsByAccount before this call. The SQL CHECK
// on byte_size (1..262144) and endpoint_count (0..50) is the
// floor that backs the abuse-surface cap.
func (s *PgStore) UpsertAppOpenAPIDoc(ctx context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string) error {
	// Defence-in-depth: confirm the FK target row exists AND belongs
	// to the caller's account before the INSERT fires. The apid
	// handler validates ownership at the loadApp boundary, but the
	// store layer must enforce the IDOR floor too — a future caller
	// (test harness, admin tool, internal API) that bypasses the
	// handler would otherwise be able to write a row into a
	// foreign tenant's app_id and then read it back via
	// GetAppOpenAPIDoc (the WHERE clause matches because the
	// account_id they passed is the one they wrote).
	var exists string
	if err := s.pool.QueryRow(ctx,
		`select id from apps where id = $1 and account_id = $2`, appID, accountID,
	).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: openapi import parent check: %w", err)
	}
	sum := sha256.Sum256(doc)
	_, err := s.pool.Exec(ctx, `
		insert into app_openapi_docs
		    (app_id, account_id, doc, doc_sha256, byte_size,
		     endpoint_count, source, openapi_version, captured_at, updated_at)
		values ($1, $2, $3, $4, $5, $6, 'manual_import', $7, now(), now())
		on conflict (app_id) do update
		set doc             = excluded.doc,
		    doc_sha256      = excluded.doc_sha256,
		    byte_size       = excluded.byte_size,
		    endpoint_count  = excluded.endpoint_count,
		    source          = excluded.source,
		    openapi_version = excluded.openapi_version,
		    captured_at     = app_openapi_docs.captured_at,
		    updated_at      = now()
	`,
		appID, accountID, doc, sum[:],
		len(doc), endpointCount, openapiVersion)
	if err != nil {
		return fmt.Errorf("state: upsert app openapi doc: %w", err)
	}
	return nil
}

// DeleteAppOpenAPIDoc removes the import row for one app.
// ErrNotFound when no row OR the caller's accountID does not
// match — same IDOR floor as the read. The apid caller treats
// ErrNotFound as "already deleted" so a retry is a no-op
// (idempotent 204).
func (s *PgStore) DeleteAppOpenAPIDoc(ctx context.Context, appID, accountID string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_openapi_docs
		 where app_id = $1 and account_id = $2`,
		appID, accountID)
	if err != nil {
		return fmt.Errorf("state: delete app openapi doc: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOpenAPIImportsByAccount returns the number of import rows
// the account owns. Drives the per-account quota gate
// (api.Plan.OpenAPIImportsPerAccount). The count is computed
// server-side via a SELECT COUNT(*) so the apid doesn't load the
// full body slice.
func (s *PgStore) CountOpenAPIImportsByAccount(ctx context.Context, accountID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from app_openapi_docs where account_id = $1`,
		accountID).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count openapi imports by account: %w", err)
	}
	return n, nil
}
