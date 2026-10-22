package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func doctorFinding(t *testing.T, r DoctorReport, id, code string) DoctorFinding {
	t.Helper()
	if !r.Valid() {
		t.Fatalf("invalid doctor report: %+v", r)
	}
	for _, f := range r.Findings {
		if f.ID == id {
			if f.Code != code {
				t.Fatalf("%s: got %s, want %s: %+v", id, f.Code, code, r)
			}
			return f
		}
	}
	t.Fatalf("missing finding %s", id)
	return DoctorFinding{}
}

func TestDoctorReadOnlyConfiguration(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	e.Checks = []Check{{Name: "products exist", SQL: "SELECT EXISTS(SELECT 1 FROM products)"}}
	before := validationState(t, e, p.ID)
	r, err := e.Doctor(t.Context(), s, "public.products")
	if err != nil || r.Status != "ready" {
		t.Fatalf("healthy readiness: %+v %v", r, err)
	}
	for id, code := range map[string]string{"connection": "connected", "installation": "schema_current", "functions": "functions_current", "enrollment": "enrolled", "fingerprint": "fingerprint_matches", "triggers": "triggers_enabled", "permissions": "permissions_present", "policy": "policy_configured"} {
		doctorFinding(t, r, id, code)
	}
	if after := validationState(t, e, p.ID); before != after {
		t.Fatal("doctor changed application rows, journals or the saved plan")
	}
	set, _ := configuredChecks("", e.Checks)
	if r.Policy.Digest != set.digest || r.Policy.Count != 1 || r.Policy.Mode != "named" {
		t.Fatalf("wrong policy identity: %+v", r.Policy)
	}
	// An installed helper and a trusted policy may have side effects outside
	// ordinary database writes. Neither is invoked by catalog inspection.
	if _, err := e.Pool.Exec(t.Context(), `CREATE OR REPLACE FUNCTION gregale_repair.fingerprint(target regclass) RETURNS text LANGUAGE sql STABLE SET search_path=pg_catalog AS $$SELECT pg_advisory_lock(9917)::text$$`); err != nil {
		t.Fatal(err)
	}
	e.Checks[0].SQL = "SELECT pg_advisory_lock(9918) IS NULL"
	r, err = e.Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	doctorFinding(t, r, "functions", "functions_incompatible")
	doctorFinding(t, r, "fingerprint", "fingerprint_matches")
	var locks int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid IN (9917,9918)`).Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("doctor executed an installed function or business check: %d %v", locks, err)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "pg_advisory_lock") || strings.Contains(string(raw), "bad-import") || strings.Contains(string(raw), "before_values") {
		t.Fatalf("readiness leaked SQL or row payloads: %s", raw)
	}
}

func TestDoctorConfigurationFailures(t *testing.T) {
	for _, tc := range []struct{ name, sql, id, code string }{
		{"capture disabled", `ALTER TABLE products DISABLE TRIGGER gregale_repair_capture`, "triggers", "triggers_invalid"},
		{"guard removed", `DROP TRIGGER gregale_repair_guard ON products`, "triggers", "triggers_invalid"},
		{"schema drift", `ALTER TABLE products ADD COLUMN note text`, "fingerprint", "fingerprint_changed"},
		{"row security", `ALTER TABLE products ENABLE ROW LEVEL SECURITY`, "enrollment", "enrollment_invalid"},
		{"old function", `DROP FUNCTION gregale_repair.list_history(uuid,jsonb,timestamptz,uuid,integer)`, "functions", "functions_incompatible"},
		{"old columns", `ALTER TABLE gregale_repair.plans DROP COLUMN apply_resolution CASCADE`, "installation", "schema_incompatible"},
		{"disabled journal protection", `ALTER TABLE gregale_repair.changes DISABLE TRIGGER gregale_repair_protect_change`, "installation", "schema_incompatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, _ := fixture(t, 1)
			e.CheckSQL = "SELECT count(*)>0 FROM products"
			if _, err := e.Pool.Exec(t.Context(), tc.sql); err != nil {
				t.Fatal(err)
			}
			r, err := e.Doctor(t.Context(), s, "public.products")
			if err != nil || r.Status != "blocked" {
				t.Fatalf("expected blocked report: %+v %v", r, err)
			}
			doctorFinding(t, r, tc.id, tc.code)
		})
	}
}

func TestDoctorMissingInstallationAndEnrollment(t *testing.T) {
	e := &Engine{Pool: pgtest.OpenDatabase(t), CheckSQL: "SELECT 1=1"}
	s := Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	r, err := e.Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	doctorFinding(t, r, "installation", "schema_missing")
	doctorFinding(t, r, "functions", "not_inspected")
	if _, err := e.Pool.Exec(t.Context(), Schema()); err != nil {
		t.Fatal(err)
	}
	r, err = e.Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	doctorFinding(t, r, "enrollment", "not_enrolled")
	if _, err := e.Doctor(t.Context(), s, "products"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted an ambiguous unqualified table: %v", err)
	}
	if _, err := e.Doctor(t.Context(), Scope{}, "public.products"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted invalid scope: %v", err)
	}
}

func TestDoctorFunctionManifest(t *testing.T) {
	functions, fingerprint := doctorFunctions()
	names := []string{}
	for _, f := range functions {
		names = append(names, f.Name)
		if !digestPattern.MatchString(f.Body) || f.Args == "" && !slices.Contains([]string{"guard_row", "capture_row", "protect_change", "protect_operation", "require_finalized"}, f.Name) {
			t.Fatalf("bad manifest: %+v", f)
		}
	}
	optional := []string{"capture_transaction", "protect_capture_receipt"}
	for _, name := range optional {
		if slices.Contains(names, name) || !strings.Contains(Schema(), "CREATE OR REPLACE FUNCTION gregale_repair."+name+"(") {
			t.Fatalf("optional transaction contract entered executor readiness: %s", name)
		}
	}
	if len(names)+len(optional) != strings.Count(Schema(), "CREATE FUNCTION gregale_repair.")+strings.Count(Schema(), "CREATE OR REPLACE FUNCTION gregale_repair.") || !slices.Contains(names, "verify_plan") || !strings.Contains(fingerprint, "$1::oid") || strings.Contains(fingerprint, "target") {
		t.Fatalf("manifest missed current functions: %v", names)
	}
}

func TestDoctorUnavailableAndPolicy(t *testing.T) {
	s := Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	for _, tc := range []struct {
		engine *Engine
		code   string
	}{{nil, "not_inspected"}, {&Engine{}, "policy_default"}, {&Engine{Checks: []Check{}}, "policy_invalid"}} {
		r, err := tc.engine.Doctor(t.Context(), s, "public.products")
		if err != nil || r.Status == "ready" {
			t.Fatalf("unavailable source became ready: %+v %v", r, err)
		}
		doctorFinding(t, r, "policy", tc.code)
		r.BoundDoctor()
		doctorFinding(t, r, "binding", "binding_configured")
	}
}

func TestDoctorExecutorPermissions(t *testing.T) {
	e, s, _ := fixture(t, 1)
	role := pgx.Identifier{"repair_doctor_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := e.Pool.Exec(t.Context(), "CREATE ROLE "+role+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if _, err := e.Pool.Exec(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	if _, err := e.Pool.Exec(t.Context(), "GRANT USAGE ON SCHEMA public,gregale_repair TO "+role+";"+
		"GRANT SELECT ON gregale_repair.enrollments,products TO "+role+";"+
		"GRANT SELECT,INSERT,UPDATE ON gregale_repair.operations,gregale_repair.changes,gregale_repair.plans TO "+role+";"+
		"GRANT UPDATE ON products TO "+role+";"+
		"GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA gregale_repair TO "+role); err != nil {
		t.Fatal(err)
	}
	cfg := e.Pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	limited := &Engine{Pool: pool, CheckSQL: "SELECT 1=1"}
	r, err := limited.Doctor(t.Context(), s, "public.products")
	if err != nil || r.Status != "ready" {
		t.Fatalf("restricted runtime role was not ready: %+v %v", r, err)
	}
	for _, tc := range []struct{ revoke, object string }{
		{"REVOKE UPDATE ON products FROM ", "declared fields: UPDATE"},
		{"REVOKE EXECUTE ON FUNCTION gregale_repair.apply_plan(uuid,jsonb,text,uuid,text) FROM ", "gregale_repair.apply_plan: EXECUTE"},
		{"REVOKE INSERT ON gregale_repair.plans FROM ", "gregale_repair.plans: INSERT"},
		{"REVOKE SELECT ON gregale_repair.enrollments FROM ", "gregale_repair.enrollments: SELECT"},
		{"REVOKE USAGE ON SCHEMA gregale_repair FROM ", "gregale_repair: USAGE"},
	} {
		if _, err := e.Pool.Exec(t.Context(), tc.revoke+role); err != nil {
			t.Fatal(err)
		}
		r, err = limited.Doctor(t.Context(), s, "public.products")
		if err != nil || r.Status != "blocked" {
			t.Fatalf("missing privilege was not blocked: %+v %v", r, err)
		}
		f := doctorFinding(t, r, "permissions", "permissions_missing")
		if !slices.Contains(f.Objects, tc.object) {
			t.Fatalf("missing exact grant %s: %+v", tc.object, f)
		}
	}
	doctorFinding(t, r, "enrollment", "not_inspected")
}

func TestDoctorExplicitUpgradeAndReplicationMode(t *testing.T) {
	e, s, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.list_history(uuid,jsonb,timestamptz,uuid,integer)`); err != nil {
		t.Fatal(err)
	}
	r, err := e.Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	doctorFinding(t, r, "functions", "functions_incompatible")
	var absent bool
	if err := e.Pool.QueryRow(t.Context(), `SELECT to_regprocedure('gregale_repair.list_history(uuid,jsonb,timestamptz,uuid,integer)') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatal("doctor installed an upgrade", err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	r, err = e.Doctor(t.Context(), s, "public.products")
	if err != nil || r.Status != "ready" {
		t.Fatalf("explicit upgrade did not restore readiness: %+v %v", r, err)
	}
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["session_replication_role"] = "replica"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	r, err = (&Engine{Pool: pool, CheckSQL: e.CheckSQL}).Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	doctorFinding(t, r, "triggers", "triggers_invalid")
}

func TestDoctorFunctionDefaultCompatibility(t *testing.T) {
	e, s, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	var definition string
	if err := e.Pool.QueryRow(t.Context(), `SELECT pg_get_functiondef('gregale_repair.begin_operation(uuid,jsonb,text,regclass,uuid)'::regprocedure)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(definition, "DEFAULT NULL::uuid", "DEFAULT gen_random_uuid()", 1)
	if changed == definition {
		t.Fatal("fixture did not change the default expression")
	}
	if _, err := e.Pool.Exec(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	r, err := e.Doctor(t.Context(), s, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	f := doctorFinding(t, r, "functions", "functions_incompatible")
	if !slices.Contains(f.Objects, "gregale_repair.begin_operation") {
		t.Fatal("changed function defaults escaped compatibility inspection", f)
	}
}
