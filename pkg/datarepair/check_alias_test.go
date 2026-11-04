package datarepair

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
)

// ADR-20261103205518: scalar check shape cannot be replaced by a same-named column.
func TestRepairNamedCheckCompositeAlias(t *testing.T) {
	e, scope, source := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TYPE public.check_box AS (value boolean)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ sql, status string }{
		{"SELECT true AS q", "passed"},
		{"SELECT false AS q", "failed"},
		{"SELECT false,ROW(true)::public.check_box AS q", "error"},
		{"SELECT true,ROW(false)::public.check_box AS q", "error"},
		{"SELECT true,ROW(true)::public.check_box AS q", "error"},
	} {
		policy, _ := json.Marshal(CheckPolicy{Version: 1, Checks: []Check{{Name: "shape", SQL: tc.sql}}})
		var raw []byte
		if err := e.Pool.QueryRow(ctx, `SELECT gregale_repair.evaluate_checks($1::jsonb)`, policy).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var report CheckReport
		if err := json.Unmarshal(raw, &report); err != nil || len(report.Results) != 1 || report.Results[0].Status != tc.status {
			t.Fatalf("%s: %s %v", tc.sql, raw, err)
		}
	}
	e.Checks = []Check{{Name: "shape", SQL: "SELECT false,ROW(true)::public.check_box AS q"}}
	set, err := configuredChecks("", e.Checks)
	if err != nil {
		t.Fatal(err)
	}
	e.Rehearser = namedRehearsalFixture{set: set}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "must-block"); err == nil {
		t.Fatal("malformed check allowed a live commit")
	} else {
		var failed *CheckError
		if !errors.As(err, &failed) || failed.Report.Results[0].Status != "error" {
			t.Fatalf("wrong business-check failure: %v", err)
		}
	}
	// Failure diagnostics are retained, but no receipt, row edit or inverse
	// operation can survive the rolled-back apply.
	var unchanged bool
	if err := e.Pool.QueryRow(ctx, `SELECT category='bad' AND gregale_row_version=2 AND (SELECT count(*) FROM gregale_repair.operations)=1 FROM products`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("failed apply retained writes: %v %v", unchanged, err)
	}
}

func TestRepairNamedCheckCompositeAliasRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, source := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TYPE public.check_box AS (value boolean)`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	for _, named := range []bool{false, true} {
		e.CheckSQL, e.Checks = "SELECT false,ROW(true)::public.check_box AS q", nil
		if named {
			e.Checks, e.CheckSQL = []Check{{Name: "shape", SQL: e.CheckSQL}}, ""
		}
		e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL, Checks: e.Checks}
		p, err := e.Plan(t.Context(), scope, source)
		if err != nil {
			t.Fatal(err)
		}
		failed, err := e.Rehearse(t.Context(), scope, p.ID, p.Digest)
		if err == nil || failed.RehearsedAt != nil {
			t.Fatalf("malformed check passed isolated rehearsal: named=%v %+v %v", named, failed, err)
		}
		if named {
			var checks *CheckError
			if !errors.As(err, &checks) || checks.Report.Results[0].Status != "error" {
				t.Fatalf("wrong named-check diagnostic: %v", err)
			}
		}
	}
}
