package datarepair

// ADR-20261103201136: stable rendering without rewriting historical approvals.

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const temporalDefaultTable = `CREATE TABLE default_products(id int PRIMARY KEY,category text,
 stamp timestamptz DEFAULT '2026-11-03T10:00:00Z'::timestamptz CHECK(stamp>='2026-11-01T00:00:00Z'::timestamptz));
SELECT gregale_repair.enroll('default_products',ARRAY['category']);
INSERT INTO default_products(id,category) VALUES(1,'original')`

func assertRepairTimezone(t *testing.T, e *Engine, want string) {
	t.Helper()
	var actual string
	if err := e.Pool.QueryRow(t.Context(), `SHOW TimeZone`).Scan(&actual); err != nil || actual != want {
		t.Fatalf("session timezone leaked: got %q want %q: %v", actual, want, err)
	}
}

func TestRepairFingerprintPinnedTimezone(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	writer := timezoneEngine(t, e, "Europe/Istanbul")
	if _, err := writer.Pool.Exec(t.Context(), temporalDefaultTable); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, writer, scope, "default_products", "UPDATE default_products SET category='bad'")
	assertRepairTimezone(t, writer, "Europe/Istanbul")
	p := approved(t, writer, scope, source)
	for _, zone := range []string{"UTC", "Europe/Istanbul", "America/New_York", "Asia/Kathmandu"} {
		observer := timezoneEngine(t, e, zone)
		if r, err := observer.Doctor(t.Context(), scope, "public.default_products"); err != nil || r.Status != "ready" {
			t.Fatalf("%s doctor: %+v %v", zone, r, err)
		}
		if r, err := observer.Validate(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Matches {
			t.Fatalf("%s validation: %+v %v", zone, r, err)
		}
		assertRepairTimezone(t, observer, zone)
	}
	executor := timezoneEngine(t, e, "Asia/Kathmandu")
	if _, err := executor.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "timezone-fingerprint"); err != nil {
		t.Fatal(err)
	}
	assertRepairTimezone(t, executor, "Asia/Kathmandu")
	if r, err := writer.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("cross-zone verification: %+v %v", r, err)
	}
	// A changed instant remains real schema drift, even under the same timezone.
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE default_products ALTER COLUMN stamp SET DEFAULT '2026-11-04T10:00:00Z'::timestamptz`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Plan(t.Context(), scope, source); !errors.Is(err, ErrConflict) {
		t.Fatalf("actual default drift was hidden: %v", err)
	}
}

func TestRepairFingerprintTimezoneUpgradePreservesApproval(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	ctx := t.Context()
	// Emulate the preceding contract, where enrollment used the caller's zone
	// but did not retain that zone. Keep the exact reviewed fingerprint body.
	previous := embeddedFunction(previousRowContractSchema(), "fingerprint")[0]
	previous += "\n" + schemaLimits().Replace(rowContractV1)
	enroll := embeddedFunction(captureContract, "enroll")[0]
	enroll = strings.Replace(enroll, ",fingerprint_timezone", "", 1)
	enroll = strings.Replace(enroll, ",'UTC','ISO, YMD'", ",'ISO, YMD'", 1)
	check := embeddedFunction(captureContract, "check_enrollment")[0]
	check = strings.Replace(check, " IF e.fingerprint_timezone IS NULL THEN RAISE EXCEPTION 'repair_fingerprint_timezone_requires_review' USING ERRCODE='22023'; END IF;\n", "", 1)
	begin := strings.Replace(embeddedFunction(retentionContract, "begin_operation")[0], ",gregale_repair.type_fingerprint(e.relation,true),3);", ",gregale_repair.type_fingerprint(e.relation,true),1);", 1)
	legacySQL := strings.ReplaceAll(previous+"\n"+enroll+"\n"+check+"\n"+begin, "__CURRENT_ROW_CONTRACT__", "true")
	if _, err := e.Pool.Exec(ctx, schemaBounds().Replace(legacySQL)); err != nil {
		t.Fatal(err)
	}
	legacy := timezoneEngine(t, e, "Europe/Istanbul")
	// The proof must also use the fingerprint function's catalog search path,
	// rather than deparsing the public domain as an unqualified type name.
	legacyTable := `CREATE DOMAIN public.default_stamp AS timestamptz CHECK(VALUE IS NOT NULL);` + strings.Replace(temporalDefaultTable, "stamp timestamptz DEFAULT", "stamp public.default_stamp DEFAULT", 1)
	if _, err := legacy.Pool.Exec(ctx, legacyTable); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, legacy, scope, "default_products", "UPDATE default_products SET category='bad'")
	p := approved(t, legacy, scope, source)
	before := validationState(t, legacy, p.ID)
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE gregale_repair.enrollments DROP COLUMN fingerprint_timezone`); err != nil {
		t.Fatal(err)
	}
	utc := timezoneEngine(t, e, "UTC")
	if _, err := utc.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	var unproven bool
	if err := utc.Pool.QueryRow(ctx, `SELECT fingerprint_timezone IS NULL FROM gregale_repair.enrollments WHERE relation='default_products'::regclass`).Scan(&unproven); err != nil || !unproven {
		t.Fatalf("wrong-zone upgrade certified an unmatched fingerprint: %v %v", unproven, err)
	}
	if _, err := utc.Plan(ctx, scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unproven enrollment accepted: %v", err)
	}
	if _, err := legacy.Pool.Exec(ctx, `ALTER TABLE default_products ALTER COLUMN stamp SET DEFAULT '2026-11-04T10:00:00Z'::timestamptz`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if err := utc.Pool.QueryRow(ctx, `SELECT fingerprint_timezone IS NULL FROM gregale_repair.enrollments WHERE relation='default_products'::regclass`).Scan(&unproven); err != nil || !unproven {
		t.Fatalf("upgrade adopted real schema drift: %v %v", unproven, err)
	}
	if _, err := legacy.Pool.Exec(ctx, `ALTER TABLE default_products ALTER COLUMN stamp SET DEFAULT '2026-11-03T10:00:00Z'::timestamptz`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := legacy.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, legacy, p.ID) != before {
		t.Fatal("timezone upgrade rewrote the saved approval or capture")
	}
	if r, err := utc.Validate(ctx, scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("legacy approval lost: %+v %v", r, err)
	}
	if _, err := utc.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "upgraded-timezone"); err != nil {
		t.Fatal(err)
	}
	assertRepairTimezone(t, utc, "UTC")
}
