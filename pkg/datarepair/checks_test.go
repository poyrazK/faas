package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type namedRehearsalFixture struct{ set checkSet }

func TestRepairNamedChecksRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("requires the isolated PostgreSQL Docker fixture")
	}
	e, scope, source := namedCheckFixture(t, 3)
	cfg := e.Pool.Config().ConnConfig
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+cfg.Database, ""
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), Checks: e.Checks}
	p, err := e.Plan(t.Context(), scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	failedPlan, err := e.Rehearse(t.Context(), scope, p.ID, p.Digest)
	var failed *CheckError
	if !errors.As(err, &failed) || failedPlan.CheckFailure == nil || failedPlan.RehearsedAt != nil || failedPlan.Rehearsal != nil {
		t.Fatalf("bad correction did not fail rehearsal: %+v %v", failedPlan, err)
	}
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("failed rehearsal enabled approval", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	passed, err := e.Rehearse(t.Context(), scope, p.ID, p.Digest)
	if err != nil || passed.Rehearsal == nil || !passed.ValidCheckResults(*passed.Rehearsal) || passed.CheckFailure != nil {
		t.Fatalf("valid named rehearsal: %+v %v", passed, err)
	}
	var untouched int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM products WHERE category='bad' AND gregale_row_version=2`).Scan(&untouched); err != nil || untouched != 3 {
		t.Fatal("rehearsal changed source rows", untouched, err)
	}
}

func (r namedRehearsalFixture) Rehearse(_ context.Context, p Plan) (Evidence, error) {
	results := make([]CheckResult, len(r.set.descriptors))
	for i, d := range r.set.descriptors {
		results[i] = CheckResult{Name: d.Name, SQLSHA256: d.SQLSHA256, Status: "passed"}
	}
	return Evidence{Passed: true, PlanDigest: p.Digest, SnapshotSHA256: strings.Repeat("a", 64), Runner: "test-only", ChecksSHA256: p.ChecksSHA256, CheckResults: &CheckReport{Results: results}}, nil
}

func namedCheckFixture(t *testing.T, n int) (*Engine, Scope, string) {
	t.Helper()
	e, s, source := fixture(t, n)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE repair_policy(id int PRIMARY KEY,enabled boolean NOT NULL); INSERT INTO repair_policy VALUES(1,true)`); err != nil {
		t.Fatal(err)
	}
	e.Checks = []Check{
		{Name: "Product categories restored", SQL: fmt.Sprintf("SELECT count(*) FILTER(WHERE category='original')=%d FROM public.products", n)},
		{Name: "Repair remains enabled", SQL: "SELECT enabled FROM public.repair_policy WHERE id=1 FOR SHARE"},
		{Name: "Final invariant", SQL: "SELECT true;"},
	}
	set, err := configuredChecks("", e.Checks)
	if err != nil {
		t.Fatal(err)
	}
	e.Rehearser = namedRehearsalFixture{set: set}
	return e, s, source
}

func TestRepairNamedChecksBindPolicyAndRejectDrift(t *testing.T) {
	e, scope, source := namedCheckFixture(t, 2)
	id := uuid.NewString()
	p, err := e.Prepare(t.Context(), scope, source, id)
	if err != nil || len(p.Checks) != 3 {
		t.Fatalf("named plan: %+v %v", p, err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "SELECT") || strings.Contains(string(raw), "FOR SHARE") {
		t.Fatal("query text exposed in reviewed plan")
	}
	for _, mutate := range []func([]Check){
		func(c []Check) { c[0].Name = "Renamed invariant" },
		func(c []Check) { c[0].SQL += " " },
		func(c []Check) { slices.Reverse(c) },
	} {
		original := slices.Clone(e.Checks)
		mutate(e.Checks)
		if _, err := e.Prepare(t.Context(), scope, source, id); !errors.Is(err, ErrConflict) {
			t.Fatalf("same identity accepted different policy: %v", err)
		}
		if _, err := e.Rehearse(t.Context(), scope, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
			t.Fatalf("old plan rehearsed under different policy: %v", err)
		}
		fresh, err := e.Plan(t.Context(), scope, source)
		if err != nil || fresh.Digest == p.Digest || fresh.ChecksSHA256 == p.ChecksSHA256 {
			t.Fatalf("policy change not bound to new digest: %+v %v", fresh, err)
		}
		e.Checks = original
	}
	p = approved(t, e, scope, source)
	e.Checks[0].Name = "Changed after rehearsal"
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer@example.test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approval accepted a changed named policy: %v", err)
	}
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "repair"); !errors.Is(err, ErrConflict) {
		t.Fatalf("live apply accepted a changed named policy: %v", err)
	}
}

func TestRepairNamedChecksLiveFailureRollsBackAllCandidates(t *testing.T) {
	e, scope, source := namedCheckFixture(t, 5)
	p := approved(t, e, scope, source)
	if p.Rehearsal.CheckResults == nil || !p.ValidCheckResults(*p.Rehearsal) {
		t.Fatal("missing bound per-check rehearsal results")
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=false WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "repair")
	var failed *CheckError
	if !errors.As(err, &failed) {
		t.Fatalf("missing named failure: %v", err)
	}
	if failed.Report.Results[0].Status != "passed" || failed.Report.Results[1].Status != "failed" || failed.Report.Results[2].Status != "not_run" {
		t.Fatalf("invalid live failure report: %+v %v", failed.Report, err)
	}
	var bad, version2, journals int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE category='bad'),count(*) FILTER(WHERE gregale_row_version=2),(SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products`, id).Scan(&bad, &version2, &journals); err != nil || bad != 5 || version2 != 5 || journals != 0 {
		t.Fatalf("failed check committed writes/versions/receipt: %d %d %d %v", bad, version2, journals, err)
	}
	retained, err := e.Get(t.Context(), scope, p.ID)
	if err != nil || retained.AppliedAt != nil || retained.ApplyChecks != nil || retained.CheckFailure == nil || retained.CheckFailure.Stage != "apply" || retained.CheckFailure.Evidence.Passed || !retained.ValidCheckResults(retained.CheckFailure.Evidence) || retained.Digest != p.Digest {
		t.Fatalf("missing durable rollback diagnostic: %+v %v", retained, err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	done, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "repair")
	if err != nil || done.ApplyChecks == nil || !done.ApplyChecks.Valid(true) || done.CheckFailure != nil || done.AppliedAt == nil {
		t.Fatalf("valid live checks did not commit: %+v %v", done, err)
	}
	// Reconciliation consumes the original check receipt, even with a changed
	// policy and later business state. It never evaluates callbacks again.
	e.Checks[0].SQL = "SELECT false"
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	replay, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "repair")
	if err != nil || replay.ApplyChecks == nil || !replay.ApplyChecks.Valid(true) || replay.RepairOperationID == nil || *replay.RepairOperationID != id {
		t.Fatalf("receipt replay reran named checks: %+v %v", replay, err)
	}
}

func TestRepairNamedChecksValidateScalarResultsAndRedactFailures(t *testing.T) {
	for _, sql := range []string{
		"SELECT NULL::boolean", "SELECT false", "SELECT true WHERE false", "SELECT true FROM generate_series(1,2)",
		"SELECT 'sensitive-result'::text", "SELECT true AS value,true AS value", "SELECT 1/0=1", "SELECT missing_sensitive_column FROM products",
	} {
		t.Run(sql, func(t *testing.T) {
			e, scope, source := namedCheckFixture(t, 1)
			// A separate fresh plan binds each actual query; evidence below is a
			// trusted test runner only, while live evaluation uses real PostgreSQL.
			e.Checks = []Check{{Name: "Declared invariant", SQL: sql}}
			set, err := configuredChecks("", e.Checks)
			if err != nil {
				t.Fatal(err)
			}
			e.Rehearser = namedRehearsalFixture{set: set}
			p := approved(t, e, scope, source)
			_, err = e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "repair")
			var failed *CheckError
			if !errors.As(err, &failed) || !failed.Report.Valid(false) {
				t.Fatalf("invalid scalar accepted: %v", err)
			}
			out, _ := json.Marshal(failed.Report)
			if strings.Contains(string(out), "sensitive") || strings.Contains(string(out), "SELECT") || strings.Contains(err.Error(), "sensitive") {
				t.Fatal("check failure exposed SQL or result values")
			}
		})
	}
}

func TestRepairNamedChecksRejectIncompleteAndStaleEvidence(t *testing.T) {
	e, scope, source := namedCheckFixture(t, 1)
	p, err := e.Plan(t.Context(), scope, source)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := configuredChecks("", e.Checks)
	valid, _ := (namedRehearsalFixture{set: set}).Rehearse(t.Context(), p)
	for _, mutate := range []func(*Evidence){
		func(e *Evidence) { e.CheckResults = nil },
		func(e *Evidence) { e.CheckResults.Results = e.CheckResults.Results[:1] },
		func(e *Evidence) { e.CheckResults.Results[0].Name = "Another check" },
		func(e *Evidence) { e.CheckResults.Results[0].SQLSHA256 = strings.Repeat("b", 64) },
		func(e *Evidence) { e.CheckResults.Results[0].Status = "failed" },
		func(e *Evidence) { slices.Reverse(e.CheckResults.Results) },
	} {
		raw, _ := json.Marshal(valid)
		var bad Evidence
		_ = json.Unmarshal(raw, &bad)
		mutate(&bad)
		if _, err := e.RecordRehearsal(t.Context(), scope, p.ID, p.Digest, bad); !errors.Is(err, ErrConflict) {
			t.Fatalf("incomplete/mismatched evidence accepted: %v", err)
		}
	}
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("invalid evidence enabled approval")
	}
	passed, err := e.RecordRehearsal(t.Context(), scope, p.ID, p.Digest, valid)
	if err != nil || passed.RehearsedAt == nil || passed.Rehearsal == nil {
		t.Fatal("complete evidence did not record success", err)
	}
	valid.Passed = false
	valid.CheckResults.Results[len(valid.CheckResults.Results)-1].Status = "failed"
	failed, err := e.RecordRehearsal(t.Context(), scope, p.ID, p.Digest, valid)
	if err != nil || failed.RehearsedAt != nil || failed.Rehearsal != nil || failed.CheckFailure == nil {
		t.Fatal("failed rehearsal kept prior approval evidence", err)
	}
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale successful evidence enabled approval")
	}
}

func TestRepairNamedCheckPolicyBoundsAndCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"checks":[]}`, `{"version":1,"checks":[]}`, `{"version":1,"checks":[{"name":"Valid","sql":"SELECT true","unknown":true}]}`,
		`{"version":1,"checks":[{"name":"Same","sql":"SELECT true"},{"name":"Same","sql":"SELECT false"}]}`,
		`{"version":1,"checks":[{"name":"Invalid\nname","sql":"SELECT true"}]}`, `{"version":1,"checks":[{"name":"Valid","sql":"DELETE FROM products"}]}`,
		`{"version":1,"checks":[{"name":"Valid","sql":"SELECT true"}]} {}`,
	} {
		if _, err := ParseCheckPolicy([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid policy accepted: %s", raw)
		}
	}
	checks := make([]Check, api.DataRepairMaxChecks+1)
	for i := range checks {
		checks[i] = Check{Name: fmt.Sprintf("Check %d", i), SQL: "SELECT true"}
	}
	if _, err := configuredChecks("", checks); !errors.Is(err, ErrInvalid) {
		t.Fatal("excessive check count accepted")
	}
	if _, err := configuredChecks("SELECT true", checks[:1]); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed legacy/named authority accepted")
	}
	legacy, _ := configuredChecks("SELECT true", nil)
	_, hash, _ := checkSQL("SELECT true")
	if legacy.digest != hash || legacy.descriptors != nil {
		t.Fatal("legacy SQL check identity changed")
	}
}
