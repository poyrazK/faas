package datarepair

import (
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const customRepairIndex = `CREATE FUNCTION public.contract_compare(a int,b int) RETURNS int LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF (SELECT enabled FROM public.contract_policy) THEN PERFORM pg_advisory_lock(890251239); END IF;
 RETURN CASE WHEN a<b THEN -1 WHEN a>b THEN 1 ELSE 0 END;
END$$;
CREATE OPERATOR CLASS public.contract_int_ops FOR TYPE int USING btree AS
 OPERATOR 1 < (int,int), OPERATOR 2 <= (int,int), OPERATOR 3 = (int,int),
 OPERATOR 4 >= (int,int), OPERATOR 5 > (int,int), FUNCTION 1 public.contract_compare(int,int);
CREATE INDEX ON contract_products(value public.contract_int_ops);
UPDATE contract_policy SET enabled=true;`

// ADR-20261103224500: qualification must include DDL committed after approval.
func TestRepairLateCatalogChangesBlockEveryEntryPoint(t *testing.T) {
	for _, tc := range []struct{ name, setup, update, ddl string }{
		{"cascade", `CREATE TABLE contract_products(id int PRIMARY KEY,value text UNIQUE); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `CREATE TABLE contract_children(value text REFERENCES contract_products(value) ON UPDATE CASCADE); INSERT INTO contract_children VALUES('001')`},
		{"set null", `CREATE TABLE contract_products(id int PRIMARY KEY,value text UNIQUE); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `CREATE TABLE contract_children(value text REFERENCES contract_products(value) ON UPDATE SET NULL); INSERT INTO contract_children VALUES('001')`},
		{"set default", `CREATE TABLE contract_products(id int PRIMARY KEY,value text UNIQUE); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `CREATE TABLE contract_children(value text REFERENCES contract_products(value) ON UPDATE SET DEFAULT); INSERT INTO contract_children VALUES('001')`},
		{"managed version", `CREATE TABLE contract_products(id int PRIMARY KEY,value text); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `CREATE UNIQUE INDEX managed_version_reference ON contract_products(gregale_row_version); CREATE TABLE contract_children(value bigint REFERENCES contract_products(gregale_row_version) ON UPDATE CASCADE); INSERT INTO contract_children VALUES(3)`},
		{"domain recreation", `CREATE DOMAIN public.contract_value AS text; CREATE TABLE contract_products(id int PRIMARY KEY,value public.contract_value); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `ALTER TABLE contract_products ALTER COLUMN value TYPE text; DROP DOMAIN public.contract_value; CREATE DOMAIN public.contract_value AS numeric; ALTER TABLE contract_products ALTER COLUMN value TYPE public.contract_value USING value::numeric`},
		{"enum label", `CREATE TYPE public.contract_value AS ENUM('001','002'); CREATE TABLE contract_products(id int PRIMARY KEY,value public.contract_value); INSERT INTO contract_products VALUES(1,'001')`, `UPDATE contract_products SET value='002'`, `ALTER TYPE public.contract_value RENAME VALUE '001' TO 'changed'`},
		{"composite member", `CREATE TYPE public.contract_value AS (amount text); CREATE TABLE contract_products(id int PRIMARY KEY,value public.contract_value); INSERT INTO contract_products VALUES(1,ROW('001'))`, `UPDATE contract_products SET value=ROW('002')`, `ALTER TYPE public.contract_value ADD ATTRIBUTE extra int CASCADE`},
		{"custom opclass", `CREATE TABLE contract_products(id int PRIMARY KEY,value int); INSERT INTO contract_products VALUES(1,10),(2,1000); CREATE TABLE contract_policy(enabled bool); INSERT INTO contract_policy VALUES(false)`, `UPDATE contract_products SET value=20 WHERE id=1`, customRepairIndex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, writer, options, role := captureTestSetup(t)
			e.CheckSQL = "SELECT 1=1"
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, tc.setup+`; SELECT gregale_repair.enroll('contract_products',ARRAY['value']); GRANT SELECT,UPDATE ON contract_products TO `+role); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, options.Scope, "contract_products", tc.update)
			pending := approved(t, e, options.Scope, source)
			committed := approved(t, e, options.Scope, source)
			repairID := uuid.NewString()
			if _, err := e.Apply(ctx, options.Scope, committed.ID, committed.Digest, repairID, "before-ddl"); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, tc.ddl); err != nil {
				t.Fatal(err)
			}
			var frozen, after string
			if err := e.Pool.QueryRow(ctx, `SELECT row_to_json(p)::text FROM gregale_repair.plans p WHERE id=$1`, pending.ID).Scan(&frozen); err != nil {
				t.Fatal(err)
			}
			options.Table = "public.contract_products"
			if r, err := e.Doctor(ctx, options.Scope, options.Table); err != nil || r.Status != "blocked" {
				t.Fatalf("doctor accepted late DDL: %+v %v", r, err)
			}
			if r, err := writer.CheckCapture(ctx, options); err != nil || r.Status != "blocked" {
				t.Fatalf("writer accepted late DDL: %+v %v", r, err)
			}
			if r, err := e.Validate(ctx, options.Scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("validation certified changed semantics: %+v %v", r, err)
			}
			if r, err := e.Verify(ctx, options.Scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("verification certified changed semantics: %+v %v", r, err)
			}
			if _, err := e.Plan(ctx, options.Scope, source); err == nil {
				t.Fatal("planning accepted late DDL")
			}
			if _, err := e.Replace(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString()); err == nil {
				t.Fatal("replacement accepted late DDL")
			}
			if _, err := e.Apply(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); err == nil {
				t.Fatal("apply accepted late DDL")
			}
			tx, err := writer.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = Capture(ctx, tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
			_ = tx.Rollback(ctx)
			if err == nil || called {
				t.Fatalf("capture reached business code: called=%v err=%v", called, err)
			}
			if receipt, err := e.Apply(ctx, options.Scope, committed.ID, committed.Digest, repairID, "receipt-replay"); err != nil || receipt.AppliedAt == nil {
				t.Fatalf("DDL blocked historical receipt recovery: %+v %v", receipt, err)
			}
			if err := e.Pool.QueryRow(ctx, `SELECT row_to_json(p)::text FROM gregale_repair.plans p WHERE id=$1`, pending.ID).Scan(&after); err != nil || after != frozen {
				t.Fatalf("blocked calls modified frozen approval: %v", err)
			}
			if strings.HasPrefix(tc.name, "set ") || tc.name == "cascade" || tc.name == "managed version" {
				var value string
				if err := e.Pool.QueryRow(ctx, `SELECT value::text FROM contract_children`).Scan(&value); err != nil || (tc.name != "managed version" && value != "001") || (tc.name == "managed version" && value != "3") {
					t.Fatalf("repair changed an unreviewed child: %q %v", value, err)
				}
			}
			var leaked int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=890251239 AND granted`).Scan(&leaked); err != nil || leaked != 0 {
				t.Fatalf("custom index function escaped rollback: locks=%d %v", leaked, err)
			}
		})
	}
}

func TestRepairTypeStateFencesChangedAndRestoredDefinition(t *testing.T) {
	for _, phase := range []string{"callback", "after finish"} {
		t.Run(phase, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), `CREATE DOMAIN contract_value AS text; CREATE TABLE contract_products(id int PRIMARY KEY,value contract_value); SELECT gregale_repair.enroll('contract_products',ARRAY['value']); INSERT INTO contract_products(id,value) VALUES(1,'001')`); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			ddl := `ALTER DOMAIN contract_value SET NOT NULL; ALTER DOMAIN contract_value DROP NOT NULL`
			id := uuid.NewString()
			err = Capture(t.Context(), tx, scope, id, "type-ddl", "contract_products", func(tx pgx.Tx) error {
				if phase == "callback" {
					_, err := tx.Exec(t.Context(), ddl)
					return err
				}
				return nil
			})
			if phase == "after finish" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), ddl); err != nil {
					t.Fatal(err)
				}
				err = tx.Commit(t.Context())
			}
			if err == nil {
				t.Fatal("transient type DDL was certified")
			}
		})
	}
}

func TestRepairNamedTypeRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE DOMAIN public.contract_value AS text CHECK(VALUE<>''); CREATE TYPE public.contract_pair AS (value public.contract_value); CREATE TABLE contract_products(id int PRIMARY KEY,value public.contract_pair); SELECT gregale_repair.enroll('contract_products',ARRAY['value']); INSERT INTO contract_products(id,value) VALUES(1,ROW('001'))`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "contract_products", `UPDATE contract_products SET value=ROW('002')`)
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	e.CheckSQL = "SELECT (value).value='001' FROM public.contract_products"
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "named-type-repair"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("dump/restore changed the logical type contract: %+v %v", r, err)
	}
}

func TestRepairIndexClassEnrollment(t *testing.T) {
	for _, comparator := range []string{"public.contract_compare(int,int)", "pg_catalog.btint4cmp(int,int)"} {
		t.Run(comparator, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			ddl := `CREATE TABLE contract_products(id int PRIMARY KEY,value int); CREATE TABLE contract_policy(enabled bool); INSERT INTO contract_policy VALUES(false);` + customRepairIndex
			if comparator != "public.contract_compare(int,int)" {
				ddl = strings.Replace(ddl, "FUNCTION 1 public.contract_compare(int,int)", "FUNCTION 1 "+comparator, 1)
			}
			if _, err := e.Pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('contract_products',ARRAY['value'])`); err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported") {
				t.Fatalf("custom operator class enrolled: %v", err)
			}
		})
	}
}

func TestRepairIncomingKeyEnrollment(t *testing.T) {
	for _, action := range []string{"CASCADE", "SET NULL", "SET DEFAULT", "RESTRICT", "NO ACTION"} {
		t.Run(action, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE contract_products(id int PRIMARY KEY,value text UNIQUE); CREATE TABLE contract_children(value text REFERENCES contract_products(value) ON UPDATE `+action+`)`); err != nil {
				t.Fatal(err)
			}
			_, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('contract_products',ARRAY['value'])`)
			allowed := action == "RESTRICT" || action == "NO ACTION"
			if allowed && err != nil || !allowed && (err == nil || !strings.Contains(err.Error(), "repair_cascading_fields_unsupported")) {
				t.Fatalf("incoming key qualification allowed=%v: %v", allowed, err)
			}
		})
	}
}

func TestRepairNestedIndexClassEnrollment(t *testing.T) {
	e, _, _ := fixture(t, 1)
	// record_ops dispatches comparison to each member's default class. There is
	// no direct dependency from this index to the nested application comparator.
	if _, err := e.Pool.Exec(t.Context(), `CREATE TYPE contract_enum AS ENUM('a','b');
CREATE FUNCTION public.contract_enum_compare(a contract_enum,b contract_enum) RETURNS int LANGUAGE sql IMMUTABLE AS $$SELECT pg_catalog.enum_cmp(a,b)$$;
CREATE OPERATOR CLASS public.contract_enum_ops DEFAULT FOR TYPE contract_enum USING btree AS
 OPERATOR 1 < (anyenum,anyenum), OPERATOR 2 <= (anyenum,anyenum), OPERATOR 3 = (anyenum,anyenum),
 OPERATOR 4 >= (anyenum,anyenum), OPERATOR 5 > (anyenum,anyenum), FUNCTION 1 public.contract_enum_compare(contract_enum,contract_enum);
CREATE TYPE contract_pair AS (label contract_enum);
CREATE TABLE contract_products(id int PRIMARY KEY,value contract_pair);
CREATE INDEX ON contract_products(value)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('contract_products',ARRAY['value'])`); err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported") {
		t.Fatalf("composite index hid a custom member class: %v", err)
	}
}

func TestRepairNamedTypeUpgradeDoesNotCertifyOldCaptures(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE DOMAIN contract_value AS text; CREATE TABLE contract_products(id int PRIMARY KEY,value contract_value); SELECT gregale_repair.enroll('contract_products',ARRAY['value']); INSERT INTO contract_products(id,value) VALUES(1,'001')`); err != nil {
		t.Fatal(err)
	}
	// The preceding installation did not retain type definitions. Emulate its
	// begin/finalization functions before producing history, never edit evidence.
	begin := embeddedFunction(retentionContract, "begin_operation")[0]
	begin = strings.Replace(begin, "e.type_fingerprint,gregale_repair.type_fingerprint(e.relation,true)", "NULL,NULL", 1)
	finish := embeddedFunction(captureContract, "finish_operation")[0]
	start := strings.Index(finish, " IF captured_types IS DISTINCT FROM")
	end := strings.Index(finish[start:], " END IF;") + start + len(" END IF;")
	finish = finish[:start] + finish[end:]
	deferred := embeddedFunction(captureContract, "require_finalized")[0]
	start = strings.Index(deferred, " IF o.capture_type_state IS DISTINCT FROM")
	end = strings.Index(deferred[start:], " END IF;") + start + len(" END IF;")
	deferred = deferred[:start] + deferred[end:]
	if _, err := e.Pool.Exec(t.Context(), begin+"\n"+finish+"\n"+deferred); err != nil {
		t.Fatal(err)
	}
	legacyPlan := strings.ReplaceAll(embeddedFunction(retentionContract, "create_plan")[0], "__UNPROVEN_OPERATION__", "false")
	legacyApply := strings.ReplaceAll(embeddedFunction(retentionContract, "apply_plan")[0], "__UNPROVEN_PLAN__", "false")
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(legacyPlan+"\n"+legacyApply)); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "contract_products", `UPDATE contract_products SET value='002'`)
	pending := approved(t, e, scope, source)
	committed := approved(t, e, scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "old-proof"); err != nil {
		t.Fatal(err)
	}
	before := validationState(t, e, pending.ID)
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if validationState(t, e, pending.ID) != before {
		t.Fatal("upgrade rewrote historical type proof or approval")
	}
	if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("upgrade retrospectively certified old type definitions: %v", err)
	}
	if _, err := e.Replace(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement certified old type definitions: %v", err)
	}
	if _, err := e.Apply(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString(), "old-proof"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply certified old type definitions: %v", err)
	}
	if r, err := e.Validate(t.Context(), scope, pending.ID, pending.Digest); err != nil || r.Matches || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("validation certified old type definitions: %+v %v", r, err)
	}
	if r, err := e.Verify(t.Context(), scope, committed.ID, committed.Digest); err != nil || r.Available || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("verification certified old type definitions: %+v %v", r, err)
	}
	if receipt, err := e.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "receipt-replay"); err != nil || receipt.AppliedAt == nil {
		t.Fatalf("upgrade blocked historical receipt recovery: %+v %v", receipt, err)
	}
	newSource := captureValidation(t, e, scope, "contract_products", `UPDATE contract_products SET value='003'`)
	p := approved(t, e, scope, newSource)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "new-proof"); err != nil {
		t.Fatal(err)
	}
}

func TestRepairTypeBaselineUpgradeOnlyAdoptsFutureCaptures(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE DOMAIN contract_value AS text; CREATE TABLE contract_products(id int PRIMARY KEY,value contract_value); SELECT gregale_repair.enroll('contract_products',ARRAY['value']); INSERT INTO contract_products(id,value) VALUES(1,'001')`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "contract_products", `UPDATE contract_products SET value='002'`)
	pending := approved(t, e, scope, source)
	if _, err := e.Pool.Exec(t.Context(), `ALTER DOMAIN contract_value ADD CONSTRAINT future_values CHECK(VALUE<>'never')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("baseline refresh certified an earlier capture: %v", err)
	}
	if _, err := e.Apply(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString(), "old-baseline"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("baseline refresh certified an earlier approval: %v", err)
	}
	newSource := captureValidation(t, e, scope, "contract_products", `UPDATE contract_products SET value='003'`)
	p := approved(t, e, scope, newSource)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "new-baseline"); err != nil {
		t.Fatal(err)
	}
}
