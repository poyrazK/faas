package datarepair

import (
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func renderingEngine(t *testing.T, e *Engine, dateStyle, intervalStyle string) *Engine {
	t.Helper()
	cfg := e.Pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["DateStyle"] = dateStyle
	cfg.ConnConfig.RuntimeParams["IntervalStyle"] = intervalStyle
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Engine{Pool: pool, CheckSQL: e.CheckSQL, Checks: e.Checks, Rehearser: e.Rehearser}
}

func assertRepairRendering(t *testing.T, e *Engine, dateStyle, intervalStyle string) {
	t.Helper()
	var date, interval string
	if err := e.Pool.QueryRow(t.Context(), `SELECT current_setting('DateStyle'),current_setting('IntervalStyle')`).Scan(&date, &interval); err != nil || date != dateStyle || interval != intervalStyle {
		t.Fatalf("render settings leaked: date=%q interval=%q: %v", date, interval, err)
	}
}

func TestRepairIntervalsAcrossConnectionStyles(t *testing.T) {
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		t.Run(style, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE duration_products(id int PRIMARY KEY,value interval); SELECT gregale_repair.enroll('duration_products',ARRAY['value']); INSERT INTO duration_products(id,value) VALUES(1,make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4))`); err != nil {
				t.Fatal(err)
			}
			writer := renderingEngine(t, e, "SQL, DMY", style)
			source := captureValidation(t, writer, scope, "duration_products", `UPDATE duration_products SET value=make_interval(days=>1)`)
			assertRepairRendering(t, writer, "SQL, DMY", style)
			p := approved(t, e, scope, source)
			for _, observerStyle := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
				observer := renderingEngine(t, e, "German, DMY", observerStyle)
				if r, err := observer.Validate(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Matches {
					t.Fatalf("%s validation: %+v %v", observerStyle, r, err)
				}
				assertRepairRendering(t, observer, "German, DMY", observerStyle)
			}
			executor := renderingEngine(t, e, "SQL, DMY", "sql_standard")
			child, err := executor.Replace(t.Context(), scope, p.ID, p.Digest, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			child, err = executor.Rehearse(t.Context(), scope, child.ID, child.Digest)
			if err != nil {
				t.Fatal(err)
			}
			child, err = executor.Approve(t.Context(), scope, child.ID, child.Digest, "interval-reviewer")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Apply(t.Context(), scope, child.ID, child.Digest, uuid.NewString(), "interval-repair"); err != nil {
				t.Fatal(err)
			}
			var epoch int
			var version int
			if err := e.Pool.QueryRow(t.Context(), `SELECT extract(epoch FROM value)::int,gregale_row_version FROM duration_products`).Scan(&epoch, &version); err != nil || epoch != -93784 || version != 3 {
				t.Fatalf("incorrect restoration epoch=%d version=%d: %v", epoch, version, err)
			}
			if r, err := writer.Verify(t.Context(), scope, child.ID, child.Digest); err != nil || !r.Available || r.Unchanged != 1 {
				t.Fatalf("cross-style verification: %+v %v", r, err)
			}
			assertRepairRendering(t, executor, "SQL, DMY", "sql_standard")
		})
	}
}

func TestRepairNestedIntervalsAcrossStyles(t *testing.T) {
	for _, tc := range []struct{ name, setup, datatype, before, after, observed string }{
		{"domain", `CREATE DOMAIN duration_value AS interval;`, "duration_value", `make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4)`, `make_interval(days=>1)`, "value"},
		{"composite", `CREATE TYPE duration_value AS (duration interval);`, "duration_value", `ROW(make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4))`, `ROW(make_interval(days=>1))`, "(value).duration"},
		{"range", `CREATE TYPE duration_range AS RANGE(subtype=interval,multirange_type_name=duration_multirange);`, "duration_range", `duration_range(make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4),make_interval(days=>2),'[)')`, `duration_range(make_interval(days=>1),make_interval(days=>2),'[)')`, "lower(value)"},
		{"multirange", `CREATE TYPE duration_range AS RANGE(subtype=interval,multirange_type_name=duration_multirange);`, "duration_multirange", `duration_multirange(duration_range(make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4),make_interval(days=>2),'[)'))`, `duration_multirange(duration_range(make_interval(days=>1),make_interval(days=>2),'[)'))`, "lower(value)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), tc.setup+`CREATE TABLE duration_products(id int PRIMARY KEY,value `+tc.datatype+`); SELECT gregale_repair.enroll('duration_products',ARRAY['value']); INSERT INTO duration_products(id,value) VALUES(1,`+tc.before+`)`); err != nil {
				t.Fatal(err)
			}
			writer := renderingEngine(t, e, "ISO, MDY", "sql_standard")
			source := captureValidation(t, writer, scope, "duration_products", `UPDATE duration_products SET value=`+tc.after)
			p := approved(t, e, scope, source)
			if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "nested-interval"); err != nil {
				t.Fatal(err)
			}
			var epoch int
			if err := e.Pool.QueryRow(t.Context(), `SELECT extract(epoch FROM `+tc.observed+`)::int FROM duration_products`).Scan(&epoch); err != nil || epoch != -93784 {
				t.Fatalf("nested interval epoch=%d: %v", epoch, err)
			}
		})
	}
}

// Isolate the preceding producer's rendering behavior. Type signatures are
// contemporaneous; separate upgrade tests cover missing historical type proof.
func installV2RenderingContract(t *testing.T, e *Engine) {
	t.Helper()
	contract := strings.ReplaceAll(captureContract, "__CURRENT_ROW_CONTRACT__", "true")
	begin := strings.Replace(embeddedFunction(retentionContract, "begin_operation")[0], ",true),3);", ",true),2);", 1)
	parts := []string{previousV2RowContractSchema(), contract, begin, strings.ReplaceAll(valueComparisonSchema, " SET IntervalStyle = 'postgres'", "")}
	for _, name := range []string{"create_plan", "apply_plan"} {
		part := strings.ReplaceAll(embeddedFunction(retentionContract, name)[0], " SET IntervalStyle = 'postgres'", "")
		part = strings.NewReplacer("__UNPROVEN_OPERATION__", "false", "__UNPROVEN_PLAN__", "false").Replace(part)
		parts = append(parts, part)
	}
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(strings.Join(parts, "\n"))); err != nil {
		t.Fatal(err)
	}
}

func TestRepairIntervalUpgradePreservesEvidenceAndReceipts(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	installV2RenderingContract(t, e)
	writer := renderingEngine(t, e, "ISO, MDY", "sql_standard")
	if _, err := writer.Pool.Exec(t.Context(), `CREATE TABLE duration_products(id int PRIMARY KEY,value interval); SELECT gregale_repair.enroll('duration_products',ARRAY['value']); INSERT INTO duration_products(id,value) VALUES(1,make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4))`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, writer, scope, "duration_products", `UPDATE duration_products SET value=make_interval(days=>1)`)
	pending := approved(t, writer, scope, source)
	committed := approved(t, writer, scope, source)
	repairID := uuid.NewString()
	if _, err := writer.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "legacy-interval"); err != nil {
		t.Fatal(err)
	}
	before := validationState(t, e, pending.ID)
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE gregale_repair.enrollments DROP COLUMN fingerprint_datestyle,DROP COLUMN fingerprint_intervalstyle`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, e, pending.ID) != before {
		t.Fatal("upgrade rewrote interval evidence or approval")
	}
	if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy interval authorized planning: %v", err)
	}
	if _, err := e.Replace(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy interval authorized replacement: %v", err)
	}
	if _, err := e.Apply(t.Context(), scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy interval authorized apply: %v", err)
	}
	if r, err := e.Validate(t.Context(), scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("legacy interval validated: %+v %v", r, err)
	}
	if r, err := e.Verify(t.Context(), scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("legacy interval verified: %+v %v", r, err)
	}
	if receipt, err := e.Apply(t.Context(), scope, committed.ID, committed.Digest, repairID, "receipt-replay"); err != nil || receipt.AppliedAt == nil {
		t.Fatalf("receipt recovery blocked: %+v %v", receipt, err)
	}
	newSource := captureValidation(t, e, scope, "duration_products", `UPDATE duration_products SET value=make_interval(days=>3)`)
	p := approved(t, e, scope, newSource)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "canonical-interval"); err != nil {
		t.Fatal(err)
	}
	var epoch int
	if err := e.Pool.QueryRow(t.Context(), `SELECT extract(epoch FROM value)::int FROM duration_products`).Scan(&epoch); err != nil || epoch != -93784 {
		t.Fatalf("new producer did not retain exact interval: %d %v", epoch, err)
	}
}

func TestRepairIntervalRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE duration_products(id int PRIMARY KEY,value interval); SELECT gregale_repair.enroll('duration_products',ARRAY['value']); INSERT INTO duration_products(id,value) VALUES(1,make_interval(days=>-1,hours=>-2,mins=>-3,secs=>-4))`); err != nil {
		t.Fatal(err)
	}
	writer := renderingEngine(t, e, "SQL, DMY", "sql_standard")
	source := captureValidation(t, writer, scope, "duration_products", `UPDATE duration_products SET value=make_interval(days=>1)`)
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	e.CheckSQL = "SELECT extract(epoch FROM value)=-93784 FROM public.duration_products"
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "real-interval"); err != nil {
		t.Fatal(err)
	}
	if r, err := writer.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("rehearsed interval failed verification: %+v %v", r, err)
	}
}
