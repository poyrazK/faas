package copydatabases

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func ensureJournal(ctx context.Context, conn *pgx.Conn, q *sqlc.Queries, p Plan, check, authorizeOnly func() error) ([]sqlc.GregaleCopyDatabasesDatabase, error) {
	present, err := q.CopyDatabaseSchemaExists(ctx, conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !present {
		if _, err = verifyCatalogue(ctx, conn, p, nil, 0); err != nil {
			return nil, err
		}
	}
	if err = check(); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return nil, classify(ctx, err)
	}
	defer rollback(ctx, tx)
	if !present {
		for _, install := range []func(context.Context, sqlc.DBTX) error{q.InstallCopyDatabaseSchema, q.InstallCopyDatabasePlan, q.InstallCopyDatabaseReceipts} {
			if err = install(ctx, tx); err != nil {
				return nil, classify(ctx, err)
			}
		}
		raw, err := p.PrivatePayloadForSealing()
		if err != nil {
			return nil, err
		}
		if err = q.InsertCopyDatabasePlanBody(ctx, tx, raw); err != nil {
			return nil, classify(ctx, err)
		}
		for _, d := range p.body.Databases {
			id, state := d.CreateTargetOID, "reserved"
			if d.ExistingTargetOID != 0 {
				id, state = d.ExistingTargetOID, "existing"
			}
			if err = q.ReserveCopyDatabase(ctx, tx, sqlc.ReserveCopyDatabaseParams{SourceOid: oid(d.SourceOID), TargetOid: oid(id), ReceiptState: state}); err != nil {
				return nil, classify(ctx, err)
			}
		}
	}
	rows, err := readJournal(ctx, tx, q, p)
	if err != nil {
		return nil, err
	}
	// Identity checks requiring an idle connection precede transactions. The
	// transaction boundary needs a fresh lease/dispatch check, not nested SQL.
	if err = authorizeOnly(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, classify(ctx, err)
	}
	if err = check(); err != nil {
		return nil, err
	}
	return rows, nil
}

func readJournal(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, p Plan) ([]sqlc.GregaleCopyDatabasesDatabase, error) {
	private, err := q.PrivateCopyDatabaseJournal(ctx, db)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !private {
		return nil, pgerrors.ErrConflict
	}
	raw, err := q.CopyDatabasePlanBody(ctx, db)
	if err != nil {
		return nil, classify(ctx, err)
	}
	var body payload
	if json.Unmarshal(raw, &body) != nil || !samePlanBody(body, p.body) {
		return nil, pgerrors.ErrConflict
	}
	rows, err := q.CopyDatabaseReceipts(ctx, db)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if len(rows) != len(p.body.Databases) {
		return nil, pgerrors.ErrConflict
	}
	seen := map[uint32]bool{}
	for _, r := range rows {
		if !r.SourceOid.Valid || !r.TargetOid.Valid || seen[r.SourceOid.Uint32] {
			return nil, pgerrors.ErrConflict
		}
		seen[r.SourceOid.Uint32] = true
		d, existing, err := p.creationDatabase(r.SourceOid.Uint32)
		if err != nil || d.OID != r.TargetOid.Uint32 {
			return nil, pgerrors.ErrConflict
		}
		valid := false
		switch r.State {
		case "existing":
			valid = existing && !r.ClaimedAt.Valid && r.CreatedAt.Valid
		case "reserved":
			valid = !existing && !r.ClaimedAt.Valid && !r.CreatedAt.Valid
		case "creating":
			valid = !existing && r.ClaimedAt.Valid && !r.CreatedAt.Valid
		case "created":
			valid = !existing && r.ClaimedAt.Valid && r.CreatedAt.Valid && !r.CreatedAt.Time.Before(r.ClaimedAt.Time)
		}
		if !valid || (r.ClaimedAt.Valid && (r.ClaimedAt.InfinityModifier != 0 || r.ClaimedAt.Time.IsZero())) || (r.CreatedAt.Valid && (r.CreatedAt.InfinityModifier != 0 || r.CreatedAt.Time.IsZero())) {
			return nil, pgerrors.ErrConflict
		}
	}
	return rows, nil
}

// JSONB rewrites key ordering/whitespace in an embedded private proof. Compare
// its complete JSON value, retaining exact numbers, and every typed plan field.
func samePlanBody(a, b payload) bool {
	var proofA, proofB any
	for _, v := range []struct {
		raw []byte
		out *any
	}{{a.RoleProof, &proofA}, {b.RoleProof, &proofB}} {
		d := json.NewDecoder(bytes.NewReader(v.raw))
		d.UseNumber()
		if d.Decode(v.out) != nil {
			return false
		}
	}
	if !reflect.DeepEqual(proofA, proofB) {
		return false
	}
	a.RoleProof, b.RoleProof = nil, nil
	return reflect.DeepEqual(a, b)
}

func receiptRow(rows []sqlc.GregaleCopyDatabasesDatabase, id uint32) sqlc.GregaleCopyDatabasesDatabase {
	for _, r := range rows {
		if r.SourceOid.Uint32 == id {
			return r
		}
	}
	return sqlc.GregaleCopyDatabasesDatabase{}
}

func verifyCatalogue(ctx context.Context, conn *pgx.Conn, p Plan, rows []sqlc.GregaleCopyDatabasesDatabase, selected uint32) (bool, error) {
	t := p.body.Target
	cfg := copyinventory.Config{PostgresMajor: t.Scope.PostgresMajor, DatabaseName: t.DatabaseName, DatabaseOID: t.DatabaseOID, RoleName: t.RoleName, RoleOID: t.RoleOID}
	if _, err := rand.Read(cfg.FingerprintKey[:]); err != nil {
		return false, pgerrors.ErrUnavailable
	}
	i, err := copyinventory.Read(ctx, conn, cfg)
	if err != nil {
		return false, err
	}
	c, err := i.DatabaseCatalogueForWorker()
	if err != nil {
		return false, err
	}
	// Catalogue fingerprints use a fresh private key here; compare the complete
	// authenticated semantic catalogue against retained baseline and owned claims.
	expected := slices.Clone(p.body.Baseline.Databases)
	present := false
	for _, r := range rows {
		d, existing, _ := p.creationDatabase(r.SourceOid.Uint32)
		found := false
		for _, current := range c.Databases {
			if current.OID == d.OID {
				found = true
			}
		}
		if r.SourceOid.Uint32 == selected {
			present = found
		}
		if !existing && (r.State == "created" || (r.State == "creating" && found)) {
			expected = append(expected, d)
		}
	}
	slices.SortFunc(expected, func(a, b copyinventory.Database) int { return compare(a.OID, b.OID) })
	observed := payload{Baseline: privateCatalogue(c)}
	canonical(&observed)
	if !reflect.DeepEqual(expected, observed.Baseline.Databases) || !reflect.DeepEqual(p.body.Baseline.Settings, observed.Baseline.Settings) || !reflect.DeepEqual(p.body.Baseline.Tablespaces, observed.Baseline.Tablespaces) {
		return false, pgerrors.ErrConflict
	}
	return present, nil
}

func transition(ctx context.Context, conn *pgx.Conn, q *sqlc.Queries, id uint32, complete bool, check, authorizeOnly func() error) error {
	if err := check(); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	var count int64
	if complete {
		count, err = q.CompleteCopyDatabase(ctx, tx, oid(id))
	} else {
		count, err = q.ClaimCopyDatabase(ctx, tx, oid(id))
	}
	if err != nil {
		return classify(ctx, err)
	}
	if count != 1 {
		return pgerrors.ErrConflict
	}
	if err = authorizeOnly(); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return check()
}
