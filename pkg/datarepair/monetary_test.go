package datarepair

import (
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func monetaryEngine(t *testing.T, e *Engine, locale string) *Engine {
	t.Helper()
	cfg := e.Pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["lc_monetary"] = locale
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "22023" {
			t.Skipf("PostgreSQL monetary locale %s is unavailable", locale)
		}
		t.Fatal(err)
	}
	return &Engine{Pool: pool, CheckSQL: e.CheckSQL, Checks: e.Checks, Rehearser: e.Rehearser}
}

func assertMonetaryLocale(t *testing.T, e *Engine, locale string) {
	t.Helper()
	var actual string
	if err := e.Pool.QueryRow(t.Context(), `SHOW lc_monetary`).Scan(&actual); err != nil || actual != locale {
		t.Fatalf("monetary setting leaked: got %q, want %q: %v", actual, locale, err)
	}
}

func TestRepairMoneyAcrossConnectionLocales(t *testing.T) {
	for _, locale := range []string{"C", "de_DE.utf8", "ja_JP.utf8"} {
		t.Run(locale, func(t *testing.T) {
			for _, tc := range []struct{ name, setup, datatype, before, after, observed string }{
				{"money", "", "money", "1234.56::numeric::money", "9876.54::numeric::money", "value"},
				{"domain", "CREATE DOMAIN monetary_value AS money;", "monetary_value", "1234.56::numeric::money", "9876.54::numeric::money", "value::money"},
				{"composite", "CREATE TYPE monetary_value AS (balance money);", "monetary_value", "ROW(1234.56::numeric::money)", "ROW(9876.54::numeric::money)", "(value).balance"},
				{"range", "CREATE TYPE monetary_range AS RANGE(subtype=money,multirange_type_name=monetary_multirange);", "monetary_range", "monetary_range(1234.56::numeric::money,20000::numeric::money,'[)')", "monetary_range(9876.54::numeric::money,20000::numeric::money,'[)')", "lower(value)"},
				{"multirange", "CREATE TYPE monetary_range AS RANGE(subtype=money,multirange_type_name=monetary_multirange);", "monetary_multirange", "monetary_multirange(monetary_range(1234.56::numeric::money,20000::numeric::money,'[)'))", "monetary_multirange(monetary_range(9876.54::numeric::money,20000::numeric::money,'[)'))", "lower(value)"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					e, scope, _ := fixture(t, 1)
					writer := monetaryEngine(t, e, locale)
					if _, err := e.Pool.Exec(t.Context(), tc.setup+`CREATE TABLE monetary_products(id int PRIMARY KEY,value `+tc.datatype+`); SELECT gregale_repair.enroll('monetary_products',ARRAY['value']); INSERT INTO monetary_products(id,value) VALUES(1,`+tc.before+`)`); err != nil {
						t.Fatal(err)
					}
					var original string
					if err := e.Pool.QueryRow(t.Context(), `SELECT encode(cash_send(`+tc.observed+`),'hex') FROM monetary_products`).Scan(&original); err != nil {
						t.Fatal(err)
					}
					source := captureValidation(t, writer, scope, "monetary_products", `UPDATE monetary_products SET value=`+tc.after)
					assertMonetaryLocale(t, writer, locale)
					p := approved(t, e, scope, source)
					report, err := writer.Validate(t.Context(), scope, p.ID, p.Digest)
					if err != nil || !report.Matches {
						t.Fatalf("cross-locale validation: %+v %v", report, err)
					}
					child, err := writer.Replace(t.Context(), scope, p.ID, p.Digest, uuid.NewString())
					if err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Rehearse(t.Context(), scope, child.ID, child.Digest); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Approve(t.Context(), scope, child.ID, child.Digest, "money-reviewer"); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Apply(t.Context(), scope, child.ID, child.Digest, uuid.NewString(), "money-repair"); err != nil {
						t.Fatal(err)
					}
					var restored string
					var version int
					if err := writer.Pool.QueryRow(t.Context(), `SELECT encode(cash_send(`+tc.observed+`),'hex'),gregale_row_version FROM monetary_products`).Scan(&restored, &version); err != nil || restored != original || version != 3 {
						t.Fatalf("native money changed: got %s/%d, want %s/3: %v", restored, version, original, err)
					}
					if r, err := e.Verify(t.Context(), scope, child.ID, child.Digest); err != nil || !r.Available || r.Unchanged != 1 {
						t.Fatalf("cross-locale verification: %+v %v", r, err)
					}
					assertMonetaryLocale(t, writer, locale)
				})
			}
		})
	}
}

func TestRepairMoneyKeysAndCatalogRendering(t *testing.T) {
	e, writer, options, role := captureTestSetup(t)
	e.CheckSQL = "SELECT 1=1"
	sourceWriter := monetaryEngine(t, e, "de_DE.utf8")
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE monetary_keys(id money PRIMARY KEY,value text,balance money DEFAULT '1'::money CHECK(balance>='0'::money)); SELECT gregale_repair.enroll('monetary_keys',ARRAY['value']); INSERT INTO monetary_keys(id,value) VALUES('1234.56','original'); GRANT SELECT,UPDATE ON monetary_keys TO `+role); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, sourceWriter, options.Scope, "monetary_keys", `UPDATE monetary_keys SET value='bad'`)
	p := approved(t, sourceWriter, options.Scope, source)
	observer := monetaryEngine(t, writer, "de_DE.utf8")
	options.Table = "public.monetary_keys"
	if r, err := observer.CheckCapture(t.Context(), options); err != nil || r.Status != "ready" {
		t.Fatalf("cross-locale writer readiness: %+v %v", r, err)
	}
	if r, err := sourceWriter.Doctor(t.Context(), options.Scope, options.Table); err != nil || r.Status != "ready" {
		t.Fatalf("cross-locale doctor: %+v %v", r, err)
	}
	if _, err := e.Apply(t.Context(), options.Scope, p.ID, p.Digest, uuid.NewString(), "money-key-repair"); err != nil {
		t.Fatal(err)
	}
	if r, err := sourceWriter.Verify(t.Context(), options.Scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("money-key verification: %+v %v", r, err)
	}
	assertMonetaryLocale(t, sourceWriter, "de_DE.utf8")
}

// Install the exact preceding row producer and its original monetary behavior.
// The fixtures keep historical proof genuine rather than editing a sealed row.
func installV3MonetaryContract(t *testing.T, e *Engine) {
	t.Helper()
	begin := strings.Replace(embeddedFunction(retentionContract, "begin_operation")[0], ",true),4);", ",true),3);", 1)
	contract := strings.ReplaceAll(captureContract, "__CURRENT_ROW_CONTRACT__", "true")
	contract = strings.ReplaceAll(contract, " OR e.fingerprint_monetary IS NULL", "")
	parts := []string{previousV3RowContractSchema(), contract, begin, strings.ReplaceAll(valueComparisonSchema, " SET lc_monetary = 'C'", "")}
	for _, name := range []string{"create_plan", "apply_plan"} {
		part := strings.ReplaceAll(embeddedFunction(retentionContract, name)[0], " SET lc_monetary = 'C'", "")
		parts = append(parts, strings.NewReplacer("__UNPROVEN_OPERATION__", "false", "__UNPROVEN_PLAN__", "false").Replace(part))
	}
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(strings.Join(parts, "\n"))); err != nil {
		t.Fatal(err)
	}
}

func TestRepairMoneyUpgradePreservesEvidenceAndReceipts(t *testing.T) {
	for _, key := range []bool{false, true} {
		name := "field"
		if key {
			name = "key"
		}
		t.Run(name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			installV3MonetaryContract(t, e)
			setup := `CREATE TABLE monetary_history(id int PRIMARY KEY,value money); SELECT gregale_repair.enroll('monetary_history',ARRAY['value']); INSERT INTO monetary_history(id,value) VALUES(1,'1234.56')`
			update := `UPDATE monetary_history SET value='9876.54'`
			if key {
				setup = `CREATE TABLE monetary_history(id money PRIMARY KEY,value text); SELECT gregale_repair.enroll('monetary_history',ARRAY['value']); INSERT INTO monetary_history(id,value) VALUES('1234.56','original')`
				update = `UPDATE monetary_history SET value='bad'`
			}
			if _, err := e.Pool.Exec(t.Context(), setup); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, scope, "monetary_history", update)
			pending := approved(t, e, scope, source)
			committed := approved(t, e, scope, source)
			repairID := uuid.NewString()
			if _, err := e.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "legacy-money"); err != nil {
				t.Fatal(err)
			}
			before := validationState(t, e, pending.ID)
			if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE gregale_repair.enrollments DROP COLUMN fingerprint_monetary`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
					t.Fatal(err)
				}
			}
			if validationState(t, e, pending.ID) != before {
				t.Fatal("upgrade rewrote historical monetary evidence")
			}
			if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("legacy money authorized planning: %v", err)
			}
			if _, err := e.Replace(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("legacy money authorized replacement: %v", err)
			}
			if _, err := e.Apply(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("legacy money authorized apply: %v", err)
			}
			if r, err := e.Validate(t.Context(), scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("legacy money validated: %+v %v", r, err)
			}
			if r, err := e.Verify(t.Context(), scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("legacy money verified: %+v %v", r, err)
			}
			if receipt, err := e.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "receipt-replay"); err != nil || receipt.AppliedAt == nil {
				t.Fatalf("historical receipt recovery blocked: %+v %v", receipt, err)
			}
			update = strings.ReplaceAll(update, "9876.54", "2222.22")
			update = strings.ReplaceAll(update, "'bad'", "'human'")
			fresh := captureValidation(t, e, scope, "monetary_history", update)
			p := approved(t, e, scope, fresh)
			if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "canonical-money"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRepairMonetaryFingerprintUpgradeRequiresMatchingLocale(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	installV3MonetaryContract(t, e)
	legacy := monetaryEngine(t, e, "de_DE.utf8")
	if _, err := legacy.Pool.Exec(t.Context(), `CREATE TABLE monetary_defaults(id int PRIMARY KEY,value text,balance money DEFAULT '1'::money); SELECT gregale_repair.enroll('monetary_defaults',ARRAY['value']); INSERT INTO monetary_defaults(id,value) VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, legacy, scope, "monetary_defaults", `UPDATE monetary_defaults SET value='bad'`)
	p := approved(t, legacy, scope, source)
	before := validationState(t, e, p.ID)
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE gregale_repair.enrollments DROP COLUMN fingerprint_monetary`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	var unknown bool
	if err := e.Pool.QueryRow(t.Context(), `SELECT fingerprint_monetary IS NULL FROM gregale_repair.enrollments WHERE relation='monetary_defaults'::regclass`).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("unmatched monetary rendering was certified: %v %v", unknown, err)
	}
	if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown monetary rendering authorized a plan: %v", err)
	}
	if _, err := legacy.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("monetary upgrade rewrote historical evidence")
	}
	if r, err := e.Validate(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("safe legacy text approval lost its fingerprint: %+v %v", r, err)
	}
	assertMonetaryLocale(t, legacy, "de_DE.utf8")
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "after-monetary-upgrade"); err != nil {
		t.Fatal(err)
	}
}

func TestRepairMoneyRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	writer := monetaryEngine(t, e, "de_DE.utf8")
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE monetary_products(id int PRIMARY KEY,value money); SELECT gregale_repair.enroll('monetary_products',ARRAY['value']); INSERT INTO monetary_products(id,value) VALUES(1,'1234.56')`); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := e.Pool.QueryRow(t.Context(), `SELECT encode(cash_send(value),'hex') FROM monetary_products`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, writer, scope, "monetary_products", `UPDATE monetary_products SET value='9876,54'`)
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	e.CheckSQL = "SELECT encode(cash_send(value),'hex')='" + original + "' FROM public.monetary_products"
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "real-money"); err != nil {
		t.Fatal(err)
	}
	if r, err := writer.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("rehearsed money failed verification: %+v %v", r, err)
	}
	assertMonetaryLocale(t, writer, "de_DE.utf8")
}
