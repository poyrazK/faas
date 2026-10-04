// adr: 583
package copydatabases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func contentsFixture(t *testing.T, extra string) (*fixture, Receipt, copyinventory.DatabaseExport, *pgx.Conn, copycontents.Config) {
	t.Helper()
	f := newFixtureConfigured(t, func(f *fixture) {
		for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
			run(t.Context(), t, root, "GRANT "+pgx.Identifier{f.dataOwner}.Sanitize()+" TO "+pgx.Identifier{f.owner}.Sanitize()+" WITH INHERIT TRUE, SET TRUE")
		}
		cfg := f.source.Config().Copy()
		cfg.Database = f.ordinary
		c, e := pgx.ConnectConfig(t.Context(), cfg)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close(context.Background())
		run(t.Context(), t, c, `CREATE SCHEMA app;
   CREATE TYPE app.mood AS ENUM ('sad','ha"ppy','secret\nlabel');
   CREATE DOMAIN app.amount AS numeric(18,4);
   CREATE TYPE app.detail AS (label text, values integer[], mood app.mood);
   CREATE TABLE app.rows(id int, payload text, absent text, literal text, bits bytea, price app.amount, details app.detail, moods app.mood[], instant timestamptz, span interval, padded char(8), quantity float8);
   INSERT INTO app.rows VALUES (1,E'secret\nvalue\twith\\escapes',NULL,E'\\N',decode('000102ff','hex'),12.3456,ROW('nested',ARRAY[1,NULL,3],'sad'::app.mood),ARRAY['sad'::app.mood,'ha"ppy'::app.mood],'2026-10-01 12:34:56.123456+03','-2 days 01:02:03.4','abc',1.23456789012345);
   ALTER TABLE app.rows ADD COLUMN mood app.mood DEFAULT 'sad';
   CREATE FUNCTION app.hidden_cast(app.mood) RETURNS text LANGUAGE SQL IMMUTABLE AS 'SELECT ''constant hidden output''';
   CREATE CAST (app.mood AS text) WITH FUNCTION app.hidden_cast(app.mood);
   INSERT INTO app.rows SELECT * FROM app.rows;
   CREATE TABLE app.toasted(payload text);
   ALTER TABLE app.toasted ALTER COLUMN payload SET STORAGE EXTERNAL;
   INSERT INTO app.toasted SELECT string_agg(md5(i::text),'') FROM generate_series(1,8192) AS i;
   CREATE TABLE app.zero_columns(); INSERT INTO app.zero_columns DEFAULT VALUES; INSERT INTO app.zero_columns DEFAULT VALUES;
   CREATE TABLE app.parent(id int); CREATE TABLE app.child() INHERITS (app.parent); INSERT INTO app.parent VALUES (1); INSERT INTO app.child VALUES (2);
   CREATE TABLE app.partitioned(id int) PARTITION BY RANGE(id); CREATE TABLE app.partition_leaf PARTITION OF app.partitioned FOR VALUES FROM (0) TO (100); INSERT INTO app.partitioned VALUES (7);
   CREATE MATERIALIZED VIEW app.stored AS SELECT payload FROM app.rows;
   CREATE MATERIALIZED VIEW app.empty_stored AS SELECT id FROM app.rows WITH NO DATA;
   CREATE SEQUENCE app.sequence; SELECT nextval('app.sequence'); SELECT setval('app.sequence',123,false);
   SELECT lo_from_bytea(424242,decode(repeat('ab',2097153),'hex'));
   `)
		if extra != "" {
			run(t.Context(), t, c, extra)
		}
	})
	r := f.prepare(t, f.ordinaryOID)
	requirements, _ := f.exports.RequirementsForWorker()
	var d copyinventory.DatabaseExport
	for _, input := range requirements {
		if input.Database.OID == f.ordinaryOID {
			d = input
		}
	}
	cfg := f.source.Config().Copy()
	cfg.Database = f.ordinary
	cfg.Password = "private-fixture-password"
	cfg.RuntimeParams = map[string]string{"search_path": "app, public", "default_transaction_read_only": "on", "TimeZone": "Asia/Tokyo", "DateStyle": "SQL, DMY", "bytea_output": "escape"}
	source, e := pgx.ConnectConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = source.Close(context.Background()) })
	var key [32]byte
	key[0] = 93
	return f, r, d, source, copycontents.Config{Key: key, SpoolDir: t.TempDir(), MaxBytes: 16 << 20, SortMemoryBytes: 64, SortDiskBytes: 1 << 20}
}
func contentsSourcePlacement(f *fixture, source *pgx.Conn, d copyinventory.DatabaseExport) copycontents.SourcePlacement {
	return func(_ context.Context, c *pgx.Conn, got copyinventory.DatabaseExport) error {
		if c != source || !reflect.DeepEqual(got, d) || c.Config().Host != f.sourceRoot.Config().Host || c.Config().Host == f.targetRoot.Config().Host {
			return pgerrors.ErrConflict
		}
		return nil
	}
}
func contentsRestore(t *testing.T, f *fixture, r Receipt, d copyinventory.DatabaseExport, source *pgx.Conn) uuid.UUID {
	t.Helper()
	tool := os.Getenv("FAAS_COPY_PG_DUMP")
	if !filepath.IsAbs(tool) {
		t.Skip("explicit PostgreSQL dump client required")
	}
	identity, e := age.GenerateX25519Identity()
	if e != nil {
		t.Fatal(e)
	}
	backend := &maintenanceArchiveBackend{}
	key := "postgres-copies/" + d.Scope.OperationID + "/" + uuid.NewString() + ".age"
	archive, e := copyarchive.Upload(t.Context(), backend, key, d, []*age.X25519Identity{identity}, 32<<20, func(ctx context.Context, w io.Writer) (copyarchive.Receipt, error) {
		return copyarchive.Export(ctx, source, d, tool, identity.Recipient(), w, 16<<20)
	})
	if e != nil {
		t.Fatal("actual source archive", e)
	}
	staged, e := copyarchive.StageRetained(t.Context(), backend, key, d, []*age.X25519Identity{identity}, archive, t.TempDir(), 16<<20, 32<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer staged.Close()
	imported := uuid.New()
	closure, e := r.WithMaintenance(t.Context(), f.target, f.exports, imported, f.authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
		cfg := f.target.Config().Copy()
		cfg.Database = target.DatabaseName
		cfg.Password = "private-fixture-password"
		c, e := pgx.ConnectConfig(ctx, cfg)
		if e != nil {
			return e
		}
		defer c.Close(context.WithoutCancel(ctx))
		_, e = staged.Restore(ctx, c, target, filepath.Join(filepath.Dir(tool), "pg_restore"), verificationPlacement(f, c, target))
		return e
	})
	if e != nil || closure.ClosedAt().IsZero() {
		t.Fatal("actual target archive", e)
	}
	return imported
}
func contentsTargetMutation(ctx context.Context, t *testing.T, f *fixture, r Receipt, sql string) {
	t.Helper()
	target, _ := r.TargetForWorker()
	cfg := f.targetRoot.Config().Copy()
	cfg.Database = target.DatabaseName
	c, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(context.WithoutCancel(ctx))
	run(ctx, t, c, sql)
}

func TestCopyDatabaseContentsIndependentManifestVerifiesRealArchiveAndOriginalKeyHandoff(t *testing.T) {
	f, r, d, source, cfg := contentsFixture(t, "")
	var toastKind string
	var toastBytes int64
	if e := source.QueryRow(t.Context(), "SELECT t.relkind::text,pg_relation_size(t.oid) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_class t ON t.oid=c.reltoastrelid WHERE n.nspname='app' AND c.relname='toasted'").Scan(&toastKind, &toastBytes); e != nil || toastKind != "t" || toastBytes == 0 {
		t.Fatal("large-value fixture has no physical TOAST storage", e)
	}
	manifest, e := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
	if e != nil {
		t.Fatal("independent stored contents capture", e)
	}
	originalGUC := source.Config().RuntimeParams
	var zone, date, bytea string
	if e = source.QueryRow(t.Context(), "SELECT current_setting('TimeZone'),current_setting('DateStyle'),current_setting('bytea_output')").Scan(&zone, &date, &bytea); e != nil || zone != originalGUC["TimeZone"] || date != "SQL, DMY" || bytea != "escape" {
		t.Fatal("capture persisted output settings", e)
	}
	original, _ := age.GenerateX25519Identity()
	rotated, _ := age.GenerateX25519Identity()
	sealed, e := copycontents.Seal(original.Recipient(), manifest)
	if e != nil {
		t.Fatal(e)
	}
	recovered, e := copycontents.Open([]*age.X25519Identity{rotated, original}, d, sealed)
	if e != nil || recovered.Fingerprint() != manifest.Fingerprint() {
		t.Fatal("original manifest handoff", e)
	}
	if _, e = copycontents.Open([]*age.X25519Identity{rotated}, d, sealed); !errors.Is(e, pgerrors.ErrUnavailable) {
		t.Fatal("rotated key adopted original ciphertext", e)
	}
	for _, mode := range []string{"scope", "source_oid", "source_role", "fingerprint", "ciphertext", "recipient", "inventory"} {
		expected, input := d, sealed
		input.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
		switch mode {
		case "scope":
			expected.Scope.OperationID = uuid.NewString()
		case "source_oid":
			expected.Database.OID++
		case "source_role":
			expected.AuthenticatedReaderRoleOID++
		case "fingerprint":
			input.Fingerprint = strings.Repeat("f", 64)
		case "ciphertext":
			input.Ciphertext[len(input.Ciphertext)/2] ^= 1
		case "recipient":
			input.KeyID = rotated.Recipient().String()
		case "inventory":
			expected.InventoryFingerprint = strings.Repeat("e", 64)
		}
		if actual, err := copycontents.Open([]*age.X25519Identity{original, rotated}, expected, input); !reflect.DeepEqual(actual, copycontents.Manifest{}) || err == nil {
			t.Fatal("substituted original data manifest accepted", mode, err)
		}
	}
	imported := contentsRestore(t, f, r, d, source)
	var match copycontents.Match
	closure, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize, func(ctx context.Context, access VerificationTarget) error {
		target, _ := access.TargetForWorker()
		c := maintenanceChild(ctx, t, f, target)
		defer c.Close(context.WithoutCancel(ctx))
		return access.WithReadOnly(ctx, c, verificationPlacement(f, c, target), func(ctx context.Context, tx pgx.Tx) error {
			var e error
			targetConfig := cfg
			targetConfig.Key[0] = 17 // The retained original key controls comparison.
			match, e = recovered.CompareTarget(ctx, tx, target, targetConfig, verificationPlacement(f, c, target))
			if e != nil {
				return e
			}
			if !match.Matches(manifest, target) {
				return pgerrors.ErrConflict
			}
			other := target
			other.DatabaseOID++
			if match.Matches(manifest, other) {
				t.Fatal("data match rebound target")
			}
			return nil
		})
	})
	if e != nil || closure.ClosedAt().IsZero() {
		t.Fatal("independent real contents verification", e)
	}
	assertMaintenanceOriginal(t, f, r)
	for _, value := range []any{manifest, recovered, sealed, match, cfg} {
		display := fmtDisplay(value)
		for _, private := range []string{f.owner, "secret", f.ordinary, d.Scope.OperationID} {
			if strings.Contains(display, private) {
				t.Fatal("data proof leaked private contents")
			}
		}
	}
}
func fmtDisplay(v any) string { return fmt.Sprintf("%v %#v", v, v) }

func TestCopyDatabaseContentsDetectsChangedRowsDuplicatesSequenceAndLargeObjects(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE app.rows SET payload='changed' WHERE id=1",
		"UPDATE app.rows SET mood='ha\"ppy'",
		"UPDATE app.toasted SET payload=left(payload,length(payload)-1)||'!'",
		"DELETE FROM app.rows WHERE ctid=(SELECT ctid FROM app.rows LIMIT 1)",
		"TRUNCATE app.zero_columns",
		"INSERT INTO app.child VALUES (3)",
		"INSERT INTO app.partitioned VALUES (8)",
		"REFRESH MATERIALIZED VIEW app.empty_stored",
		"SELECT setval('app.sequence',123,true)",
		"SELECT lo_put(424242,1048576,decode('ff','hex'))",
		"SELECT lo_unlink(424242)",
	} {
		t.Run(mutation, func(t *testing.T) {
			f, r, d, source, cfg := contentsFixture(t, "")
			manifest, e := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
			if e != nil {
				t.Fatal(e)
			}
			imported := contentsRestore(t, f, r, d, source)
			closure, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize, func(ctx context.Context, access VerificationTarget) error {
				contentsTargetMutation(ctx, t, f, r, mutation)
				target, _ := access.TargetForWorker()
				c := maintenanceChild(ctx, t, f, target)
				defer c.Close(context.WithoutCancel(ctx))
				return access.WithReadOnly(ctx, c, verificationPlacement(f, c, target), func(ctx context.Context, tx pgx.Tx) error {
					match, e := manifest.CompareTarget(ctx, tx, target, cfg, verificationPlacement(f, c, target))
					if match != (copycontents.Match{}) || !errors.Is(e, pgerrors.ErrConflict) {
						t.Fatal("changed data matched", e)
					}
					return nil
				})
			})
			if e != nil || closure.ClosedAt().IsZero() {
				t.Fatal("mismatch inspection/closure", e)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}
func TestCopyDatabaseContentsDetectsStoredMaterializedRowsOmittedByDump(t *testing.T) {
	f, r, d, source, cfg := contentsFixture(t, "UPDATE app.rows SET payload='changed after materialized capture'")
	manifest, e := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
	if e != nil {
		t.Fatal(e)
	}
	imported := contentsRestore(t, f, r, d, source)
	_, e = r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize, func(ctx context.Context, access VerificationTarget) error {
		target, _ := access.TargetForWorker()
		c := maintenanceChild(ctx, t, f, target)
		defer c.Close(context.WithoutCancel(ctx))
		return access.WithReadOnly(ctx, c, verificationPlacement(f, c, target), func(ctx context.Context, tx pgx.Tx) error {
			match, e := manifest.CompareTarget(ctx, tx, target, cfg, verificationPlacement(f, c, target))
			if match != (copycontents.Match{}) || !errors.Is(e, pgerrors.ErrConflict) {
				t.Fatal("dump refresh was mistaken for copied materialized rows", e)
			}
			return nil
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	assertMaintenanceOriginal(t, f, r)
}
func TestCopyDatabaseContentsRejectsPolicyFilteredRowsAndForeignTablesBeforeData(t *testing.T) {
	for _, mode := range []string{"rls", "foreign", "type_output", "oid_reference"} {
		t.Run(mode, func(t *testing.T) {
			extra := "ALTER TABLE app.rows ENABLE ROW LEVEL SECURITY; ALTER TABLE app.rows FORCE ROW LEVEL SECURITY; CREATE POLICY limited ON app.rows USING(false)"
			// Extension setup uses the fixture administrator. No FDW data is read.
			if mode != "rls" {
				extra = ""
			}
			f, _, d, source, cfg := contentsFixture(t, extra)
			if mode == "foreign" || mode == "type_output" {
				rootCfg := f.sourceRoot.Config().Copy()
				rootCfg.Database = f.ordinary
				c, e := pgx.ConnectConfig(t.Context(), rootCfg)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "foreign" {
					run(t.Context(), t, c, "CREATE EXTENSION postgres_fdw; CREATE SERVER external FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'invalid.example',dbname 'external'); CREATE FOREIGN TABLE app.external_rows(id int) SERVER external")
				} else {
					run(t.Context(), t, c, "CREATE EXTENSION hstore WITH SCHEMA app; CREATE TABLE app.unqualified(value app.hstore)")
				}
				_ = c.Close(context.Background())
			}
			if mode == "oid_reference" {
				cfg := source.Config().Copy()
				cfg.RuntimeParams["default_transaction_read_only"] = "off"
				c, e := pgx.ConnectConfig(t.Context(), cfg)
				if e != nil {
					t.Fatal(e)
				}
				run(t.Context(), t, c, "CREATE TABLE app.unqualified(value regproc)")
				_ = c.Close(context.Background())
			}
			manifest, e := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
			if !reflect.DeepEqual(manifest, copycontents.Manifest{}) {
				t.Fatal("unsupported coverage minted manifest")
			}
			if !errors.Is(e, pgerrors.ErrUnsupported) {
				t.Fatal("partial/external data accepted", e)
			}
			var coverage *copycontents.CoverageError
			if !errors.As(e, &coverage) || coverage.Reason() == "" {
				t.Fatal("coverage blocker is unnamed", e)
			}
			if strings.Contains(fmtDisplay(coverage), "unqualified") || strings.Contains(fmtDisplay(coverage), "external_rows") {
				t.Fatal("coverage error leaked SQL identity")
			}
			if source.PgConn().TxStatus() != 'I' {
				t.Fatal("failed capture retained transaction")
			}
		})
	}
}

func TestCopyDatabaseContentsCaptureBudgetsIdentityAndPlacementFailures(t *testing.T) {
	f, _, d, source, cfg := contentsFixture(t, "")
	for _, mode := range []string{"zero_key", "relative_spool", "negative_budget", "bytes", "disk", "source_oid", "source_role", "wrong_source", "readonly", "precheck", "postcheck", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			borrowed, e := pgx.ConnectConfig(t.Context(), source.Config().Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer borrowed.Close(context.Background())
			input, conn, config := d, borrowed, cfg
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			placement := contentsSourcePlacement(f, borrowed, d)
			calls := 0
			place := func(ctx context.Context, c *pgx.Conn, got copyinventory.DatabaseExport) error {
				calls++
				if (mode == "precheck" && calls == 1) || (mode == "postcheck" && c.PgConn().TxStatus() == 'I' && calls > 1) {
					return fmt.Errorf("private-provider-token: %w", pgerrors.ErrConflict)
				}
				if mode == "cancelled" && calls > 1 {
					cancel()
					return ctx.Err()
				}
				return placement(ctx, c, got)
			}
			switch mode {
			case "zero_key":
				config.Key = [32]byte{}
			case "relative_spool":
				config.SpoolDir = "relative"
			case "negative_budget":
				config.MaxBytes = -1
			case "bytes":
				config.MaxBytes = 1
			case "disk":
				config.SortDiskBytes = 32
			case "source_oid":
				input.Database.OID++
			case "source_role":
				input.AuthenticatedReaderRoleOID++
			case "wrong_source":
				conn = f.sourceRoot
			case "readonly":
				_, err := borrowed.Exec(ctx, "SET default_transaction_read_only=off")
				if err != nil {
					t.Fatal(err)
				}
			}
			actual, err := copycontents.Capture(ctx, conn, input, config, place)
			if !reflect.DeepEqual(actual, copycontents.Manifest{}) || err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("invalid/partial capture returned data manifest", err)
			}
			if filepath.IsAbs(config.SpoolDir) {
				entries, e := os.ReadDir(config.SpoolDir)
				if e != nil || len(entries) != 0 {
					t.Fatal("failed capture leaked spools", e)
				}
			}
			if !conn.IsClosed() && conn.PgConn().TxStatus() != 'I' {
				t.Fatal("failed capture retained transaction")
			}
		})
	}
}
