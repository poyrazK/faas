// Package copycontents independently compares qualified stored logical data.
// It does not prove source writer closure, original snapshot coverage, DDL/global
// equality, or stage readiness. Those require the complete clone's other proofs.
package copycontents

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type Config struct {
	Key             [32]byte `json:"-"`
	SpoolDir        string
	MaxBytes        int64
	SortMemoryBytes int
	SortDiskBytes   int64
}

func (Config) String() string               { return "private PostgreSQL contents reader configuration" }
func (c Config) GoString() string           { return c.String() }
func (Config) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }

func (c Config) readConfig() (Config, error) {
	if c.MaxBytes == 0 {
		c.MaxBytes = api.PostgresCopyArchiveMaxBytes
	}
	if c.SortMemoryBytes == 0 {
		c.SortMemoryBytes = api.PostgresCopyContentsSortMemoryMax
	}
	if c.SortDiskBytes == 0 {
		c.SortDiskBytes = api.PostgresCopyContentsSortDiskMax
	}
	if !filepath.IsAbs(c.SpoolDir) || c.MaxBytes < 1 || c.MaxBytes > api.PostgresCopyArchiveMaxBytes || c.SortMemoryBytes < 32 || c.SortMemoryBytes > api.PostgresCopyContentsSortMemoryMax || c.SortDiskBytes < 32 || c.SortDiskBytes > api.PostgresCopyContentsSortDiskMax {
		return Config{}, pgerrors.ErrInvalid
	}
	return c, nil
}

// ReadLimitsForWorker normalizes the bounded comparison limits and validates
// its private spool path. The retained manifest supplies the comparison key;
// source capture still separately requires its caller's nonzero key.
func (c Config) ReadLimitsForWorker() (int64, int, int64, error) {
	c, err := c.readConfig()
	if err != nil {
		return 0, 0, 0, err
	}
	return c.MaxBytes, c.SortMemoryBytes, c.SortDiskBytes, nil
}
func (c Config) validated() (Config, error) {
	c, err := c.readConfig()
	if err != nil || c.Key == ([32]byte{}) {
		return Config{}, pgerrors.ErrInvalid
	}
	return c, nil
}

type relationContents struct {
	Shape       relationShape `json:"shape"`
	Rows, Bytes int64
	Digest      string `json:"digest"`
}
type objectContents struct {
	OID    uint32 `json:"oid"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
}
type contents struct {
	Types        []typeShape        `json:"types"`
	Relations    []relationContents `json:"relations"`
	LargeObjects []objectContents   `json:"large_objects"`
}
type privateExport copyinventory.DatabaseExport

type payload struct {
	Version int           `json:"version"`
	Source  privateExport `json:"source"`
	Key     [32]byte      `json:"key"`
	Data    contents      `json:"data"`
}
type Manifest struct {
	body        payload
	fingerprint string
}

func (Manifest) String() string     { return "private PostgreSQL stored contents manifest" }
func (m Manifest) GoString() string { return m.String() }
func (m Manifest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Captured bool }{m.fingerprint != ""})
}
func (m Manifest) Fingerprint() string { return m.fingerprint }

// SourcePlacement authenticates the original immutable capture/reader and live
// durable authority. It is checked around transaction ownership and every data
// object. Current production is never used to repair a retained source manifest.
type SourcePlacement func(context.Context, *pgx.Conn, copyinventory.DatabaseExport) error

func Capture(ctx context.Context, conn *pgx.Conn, source copyinventory.DatabaseExport, cfg Config, placement SourcePlacement) (result Manifest, err error) {
	source.Database.ACL = slices.Clone(source.Database.ACL)
	cfg, err = cfg.validated()
	if err != nil {
		return result, err
	}
	if !validSource(source) || placement == nil {
		return result, pgerrors.ErrInvalid
	}
	if !source.CapturedAllowConnections {
		return result, pgerrors.ErrUnsupported
	}
	q := sqlc.New()
	check := func(ctx context.Context, tx pgx.Tx) error {
		if e := sourceIdentity(ctx, conn, tx, q, source); e != nil {
			return e
		}
		assertion := source
		assertion.Database.ACL = slices.Clone(source.Database.ACL)
		return sanitize(ctx, placement(ctx, conn, assertion))
	}
	if err = check(ctx, nil); err != nil {
		return result, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, classify(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyContentsCleanupTimeout)
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil {
			_ = conn.Close(cleanup)
			result = Manifest{}
			err = errors.Join(err, pgerrors.ErrUnavailable)
			return
		}
		if e := check(cleanup, nil); e != nil {
			result = Manifest{}
			err = errors.Join(err, e)
		}
	}()
	if err = check(ctx, tx); err != nil {
		return result, err
	}
	data, err := readContents(ctx, tx, q, cfg, func() error { return check(ctx, tx) })
	if err != nil {
		return result, err
	}
	if err = check(ctx, tx); err != nil {
		return result, err
	}
	result.body = payload{1, privateExport(source), cfg.Key, data}
	raw, err := json.Marshal(result.body)
	if err != nil {
		return Manifest{}, pgerrors.ErrUnavailable
	}
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return Manifest{}, pgerrors.ErrQuotaExceeded
	}
	result.fingerprint = hex.EncodeToString(keyedHashSum(cfg.Key, "gregale-copy-contents-manifest-v1", raw))
	return result, nil
}

// Match attests only to actual independently compared qualified logical data.
// It is opaque and bound to the original manifest and exact target pins. The
// caller must combine it with original window closure and all other clone proofs.
type Match struct {
	manifestFingerprint, targetFingerprint string
	sourceOID, targetOID                   uint32
}

func (Match) String() string     { return "private PostgreSQL contents match" }
func (m Match) GoString() string { return m.String() }
func (m Match) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Matched bool }{m.manifestFingerprint != ""})
}
func (m Match) Matches(manifest Manifest, target copyarchive.RestoreTarget) bool {
	fp, e := target.Fingerprint()
	return e == nil && manifest.validate() == nil && m.manifestFingerprint != "" && m.manifestFingerprint == manifest.fingerprint && m.targetFingerprint == fp && m.sourceOID == manifest.body.Source.Database.OID && m.targetOID == target.DatabaseOID
}

// CompareTarget consumes an existing authenticated READ ONLY, REPEATABLE READ
// verification transaction. It never opens admission or replays restore SQL.
// Source key and catalogue are recovered from the original retained manifest.
func (m Manifest) CompareTarget(ctx context.Context, tx pgx.Tx, target copyarchive.RestoreTarget, cfg Config, placement copyarchive.RestorePlacement) (Match, error) {
	if e := m.validate(); e != nil {
		return Match{}, e
	}
	cfg.Key = m.body.Key
	cfg, e := cfg.validated()
	if e != nil {
		return Match{}, e
	}
	if tx == nil || placement == nil || target.Validate() != nil || !target.Scope.Equal(m.body.Source.Scope) || target.DatabaseName != m.body.Source.Database.Name {
		return Match{}, pgerrors.ErrConflict
	}
	q := sqlc.New()
	conn := tx.Conn()
	fp, _ := target.Fingerprint()
	check := func() error {
		if e := targetIdentity(ctx, conn, tx, q, target); e != nil {
			return e
		}
		return sanitize(ctx, placement(ctx, conn, target))
	}
	if e = check(); e != nil {
		return Match{}, e
	}
	actual, e := readContents(ctx, tx, q, cfg, check)
	if e != nil {
		return Match{}, e
	}
	if e = check(); e != nil {
		return Match{}, e
	}
	if !reflect.DeepEqual(actual, m.body.Data) {
		return Match{}, pgerrors.ErrConflict
	}
	return Match{m.fingerprint, fp, m.body.Source.Database.OID, target.DatabaseOID}, nil
}
func readContents(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, cfg Config, check func() error) (contents, error) {
	result := contents{Types: []typeShape{}, Relations: []relationContents{}, LargeObjects: []objectContents{}}
	if e := q.ConfigureCopyContentsOutput(ctx, tx); e != nil {
		return result, classify(ctx, e)
	}
	cat, e := readCatalogue(ctx, tx, q)
	if e != nil {
		return result, e
	}
	result.Types = cat.types
	var bytesRead int64
	for _, r := range cat.relations {
		if e = check(); e != nil {
			return result, e
		}
		domain, e := json.Marshal(r.Shape)
		if e != nil {
			return result, pgerrors.ErrUnavailable
		}
		sorter, e := newDigestSorter(ctx, cfg.SpoolDir, cfg.SortMemoryBytes, cfg.SortDiskBytes, cfg.Key, domain)
		if e != nil {
			return result, e
		}
		writer := newRowWriter(sorter, len(r.Shape.Columns), cfg.MaxBytes-bytesRead, cfg.Key, domain)
		if r.Shape.Kind != "m" || r.Shape.Populated {
			command, e := q.FormatCopyContentsRelation(ctx, tx, sqlc.FormatCopyContentsRelationParams{Column1: pgtype.Uint32{Uint32: r.OID, Valid: true}, Column2: r.Shape.Name.Schema, Column3: r.Shape.Name.Name, Column4: r.Shape.Kind})
			if e != nil {
				sorter.Close()
				return result, classify(ctx, e)
			}
			tag, e := tx.Conn().PgConn().CopyTo(ctx, writer, command)
			if e != nil {
				sorter.Close()
				if writer.err != nil {
					return result, writer.err
				}
				err := classify(ctx, e)
				if errors.Is(err, pgerrors.ErrUnsupported) {
					return result, unsupported("relation_read_authority", r.Shape.Name)
				}
				return result, err
			}
			if tag.RowsAffected() != sorter.rows {
				sorter.Close()
				return result, pgerrors.ErrConflict
			}
		}
		digest, e := writer.Finish()
		rows := sorter.rows
		sorter.Close()
		if e != nil {
			return result, e
		}
		bytesRead += writer.bytes
		result.Relations = append(result.Relations, relationContents{r.Shape, rows, writer.bytes, digest})
		if e = check(); e != nil {
			return result, e
		}
	}
	for _, id := range cat.largeObjects {
		if e = check(); e != nil {
			return result, e
		}
		entry, e := readLargeObject(ctx, tx, q, id, cfg.Key, cfg.MaxBytes-bytesRead, check)
		if e != nil {
			return result, e
		}
		bytesRead += entry.Bytes
		result.LargeObjects = append(result.LargeObjects, entry)
	}
	if e = check(); e != nil {
		return result, e
	}
	return result, nil
}
func readLargeObject(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, id uint32, key [32]byte, remaining int64, check func() error) (objectContents, error) {
	fd, e := q.OpenCopyContentsLargeObject(ctx, tx, pgtype.Uint32{Uint32: id, Valid: true})
	if e != nil {
		return objectContents{}, classify(ctx, e)
	}
	h := keyedHash(key, "gregale-copy-contents-large-object-v1", []byte(fmt.Sprint(id)))
	var size int64
	for {
		if e = check(); e != nil {
			return objectContents{}, e
		}
		block, e := q.ReadCopyContentsLargeObject(ctx, tx, sqlc.ReadCopyContentsLargeObjectParams{Column1: fd, Column2: api.PostgresCopyContentsReadBlockBytes})
		if e != nil {
			return objectContents{}, classify(ctx, e)
		}
		if int64(len(block)) > remaining-size {
			return objectContents{}, pgerrors.ErrQuotaExceeded
		}
		size += int64(len(block))
		_, _ = h.Write(block)
		if len(block) == 0 {
			break
		}
	}
	if _, e = q.CloseCopyContentsLargeObject(ctx, tx, fd); e != nil {
		return objectContents{}, classify(ctx, e)
	}
	return objectContents{id, size, hex.EncodeToString(h.Sum(nil))}, nil
}
func identity(ctx context.Context, conn *pgx.Conn, tx pgx.Tx, q *sqlc.Queries) (sqlc.CopyContentsIdentityRow, error) {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() {
		return sqlc.CopyContentsIdentityRow{}, pgerrors.ErrConflict
	}
	var db sqlc.DBTX = conn
	status := byte('I')
	if tx != nil {
		if tx.Conn() != conn {
			return sqlc.CopyContentsIdentityRow{}, pgerrors.ErrConflict
		}
		db = tx
		status = 'T'
	}
	if conn.PgConn().TxStatus() != status {
		return sqlc.CopyContentsIdentityRow{}, pgerrors.ErrConflict
	}
	a, e := q.CopyContentsIdentity(ctx, db)
	if e != nil {
		return a, classify(ctx, e)
	}
	if !a.ReadOnly || a.RoleName != conn.Config().User || a.SessionRole != a.RoleName || a.DatabaseName != conn.Config().Database || (tx != nil && a.Isolation != "repeatable read") {
		return a, pgerrors.ErrConflict
	}
	if a.Encoding == "SQL_ASCII" {
		return a, unsupported("encoding", name{Name: a.DatabaseName})
	}
	return a, ctx.Err()
}
func sourceIdentity(ctx context.Context, c *pgx.Conn, tx pgx.Tx, q *sqlc.Queries, d copyinventory.DatabaseExport) error {
	a, e := identity(ctx, c, tx, q)
	if e != nil {
		return e
	}
	if int(a.ServerVersion/10000) != d.Scope.PostgresMajor || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != d.Database.OID || a.DatabaseName != d.Database.Name || !a.RoleOid.Valid || a.RoleOid.Uint32 != d.AuthenticatedReaderRoleOID {
		return pgerrors.ErrConflict
	}
	return nil
}
func targetIdentity(ctx context.Context, c *pgx.Conn, tx pgx.Tx, q *sqlc.Queries, t copyarchive.RestoreTarget) error {
	a, e := identity(ctx, c, tx, q)
	if e != nil {
		return e
	}
	if int(a.ServerVersion/10000) != t.Scope.PostgresMajor || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != t.DatabaseOID || a.DatabaseName != t.DatabaseName || !a.RoleOid.Valid || a.RoleOid.Uint32 != t.RoleOID || a.RoleName != t.RoleName {
		return pgerrors.ErrConflict
	}
	return nil
}
func validSource(d copyinventory.DatabaseExport) bool {
	return d.Scope.Validate() == nil && validDigest(d.InventoryFingerprint) && d.Database.OID != 0 && d.AuthenticatedReaderRoleOID != 0 && d.Database.Name != ""
}
func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func keyedHashSum(key [32]byte, namespace string, raw []byte) []byte {
	h := keyedHash(key, namespace, raw)
	return h.Sum(nil)
}
func sanitize(ctx context.Context, e error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e == nil {
		return nil
	}
	for _, kind := range []error{pgerrors.ErrInvalid, pgerrors.ErrConflict, pgerrors.ErrUnsupported, pgerrors.ErrQuotaExceeded, pgerrors.ErrUnavailable} {
		if errors.Is(e, kind) {
			return kind
		}
	}
	return pgerrors.ErrUnavailable
}
func classify(ctx context.Context, e error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return pgerrors.ErrConflict
	}
	var pe *pgconn.PgError
	if errors.As(e, &pe) {
		switch pe.Code {
		case "42501", "0A000", "55000":
			return pgerrors.ErrUnsupported
		case "23505", "42P01":
			return pgerrors.ErrConflict
		}
	}
	return pgerrors.ErrUnavailable
}
func (m Manifest) validate() error {
	if m.body.Version != 1 || !validSource(copyinventory.DatabaseExport(m.body.Source)) || m.body.Key == ([32]byte{}) || !validDigest(m.fingerprint) {
		return pgerrors.ErrConflict
	}
	raw, e := json.Marshal(m.body)
	if e != nil {
		return pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	if !hmac.Equal(keyedHashSum(m.body.Key, "gregale-copy-contents-manifest-v1", raw), mustDigest(m.fingerprint)) {
		return pgerrors.ErrConflict
	}
	return nil
}
func mustDigest(s string) []byte { b, _ := hex.DecodeString(s); return b }
