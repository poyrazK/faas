package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type postgresClientTest struct {
	req                       rehearsalpg.CreateRequest
	ack                       rehearsalpg.Receipt
	evidence                  rehearsalpg.Evidence
	createError, cleanupError bool
	deleted                   bool
}

func (c *postgresClientTest) CreateScenarioPostgres(_ context.Context, _ string, r rehearsalpg.CreateRequest) (rehearsalpg.Receipt, error) {
	c.req = r
	if c.createError {
		return c.ack, errors.New("lost response")
	}
	return c.ack, nil
}
func (c *postgresClientTest) SealScenarioPostgres(context.Context, string, string) (rehearsalpg.Evidence, error) {
	return c.evidence, nil
}
func (c *postgresClientTest) DeleteScenarioPostgres(context.Context, string) error {
	c.deleted = true
	if c.cleanupError {
		return errors.New("cleanup unavailable")
	}
	return nil
}

func postgresScenarioForTest() main.TestScenario {
	return main.TestScenario{CommandExecution: &main.TestCommandExecutionSpec{Version: 1, Runtime: "contained"}, PostgresFixture: &main.TestPostgresSpec{Version: 1, Fixture: "fixture.sql", MaxLockWaitMS: 50, Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}}}
}

func TestPostgresFixturePinsBytesAndCleansAmbiguousCreation(t *testing.T) {
	dir := t.TempDir()
	body := []byte("CREATE TABLE public.imports(item text);")
	if e := os.WriteFile(filepath.Join(dir, "fixture.sql"), body, 0o600); e != nil {
		t.Fatal(e)
	}
	r := main.TestRunReceipt{RunID: strings.Repeat("a", 32)}
	p, e := main.PrepareTestPostgres(dir, postgresScenarioForTest(), &r)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "fixture.sql"), []byte("changed"), 0o600); e != nil {
		t.Fatal(e)
	}
	client := &postgresClientTest{createError: true}
	if e = p.Create(t.Context(), client, &r, "app"); e == nil {
		t.Fatal("ambiguous creation passed")
	}
	p.Cleanup(t.Context(), client, &r)
	if !client.deleted || client.req.SQL != string(body) || client.req.Spec.FixtureSHA256 != rehearsalpg.HashBytes(body) {
		t.Fatal("fixture bytes were not pinned or cleanup was skipped")
	}
}

func TestPostgresFixtureRefusesUnsafeRecipeAndFiles(t *testing.T) {
	for _, name := range []string{"host command", "ordinary binding", "cleanup", "path escape", "symlink", "database override"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := postgresScenarioForTest()
			if e := os.WriteFile(filepath.Join(dir, "fixture.sql"), []byte("SELECT 1"), 0o600); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "host command":
				s.CommandExecution = nil
			case "ordinary binding":
				s.Postgres = true
			case "cleanup":
				s.Cleanup = [][]string{{"touch", "marker"}}
			case "database override":
				s.CommandExecution.Env = map[string]string{"DATABASE_URL": "postgresql://unrelated.invalid/live"}
			case "path escape":
				s.PostgresFixture.Fixture = "../fixture.sql"
			case "symlink":
				if e := os.Symlink("fixture.sql", filepath.Join(dir, "alias.sql")); e != nil {
					t.Fatal(e)
				}
				s.PostgresFixture.Fixture = "alias.sql"
			}
			if _, e := main.PrepareTestPostgres(dir, s, &main.TestRunReceipt{}); e == nil {
				t.Fatal("unsupported fixture was admitted")
			}
		})
	}
}

func postgresEvidenceForTest(rows ...string) *rehearsalpg.Evidence {
	s := rehearsalpg.Spec{Version: 1, FixtureSHA256: strings.Repeat("a", 64), Tables: postgresScenarioForTest().PostgresFixture.Tables, MaxLockWaitMS: 50}
	before := []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}
	after := []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}
	for _, r := range rows {
		after[0].Rows = append(after[0].Rows, json.RawMessage(r))
	}
	after, _ = rehearsalpg.CanonicalSnapshots(after)
	return &rehearsalpg.Evidence{Receipt: rehearsalpg.Receipt{Version: 1, RunID: strings.Repeat("a", 32), AppID: "app", ResourceID: "resource", FixtureSHA256: s.FixtureSHA256, ContractSHA256: s.ContractSHA256(), InitialSHA256: rehearsalpg.Hash(before)}, Status: "complete", Provenance: rehearsalpg.Provenance, Spec: s, DeploymentID: "deployment", Before: before, After: after, Changes: rehearsalpg.Changes(before, after), LockSamples: 1, LockBudgetPassed: true, CleanupConfirmed: true}
}

func TestPostgresComparisonGatesDuplicateImportsAndLockRegressions(t *testing.T) {
	a := main.TestRunReceipt{Scenario: "import", Engine: "real-vm", Profile: "warm", Attempt: 1, Status: "passed", Postgres: postgresEvidenceForTest(`{"item":"a"}`)}
	b := a
	b.Postgres = postgresEvidenceForTest(`{"item":"a"}`, `{"item":"a"}`)
	comparison, e := main.CompareTestReports("base", "candidate", []main.TestRunReceipt{a}, []main.TestRunReceipt{b})
	if e != nil || comparison.PostgresStatus != "failed" {
		t.Fatalf("duplicate missed: %+v %v", comparison, e)
	}
	if text := main.RenderTestComparisonMarkdown(comparison); !strings.Contains(text, "PostgreSQL comparison: FAILED") || !strings.Contains(text, "1 / 0 | 2 / 0") {
		t.Fatalf("missing database comparison: %s", text)
	}
	b.Postgres = postgresEvidenceForTest(`{"item":"a"}`)
	b.Postgres.MaxObservedLockWaitMS = 100
	b.Postgres.LockBudgetPassed = false
	b.Status = "failed"
	b.Error = "PostgreSQL lock-wait budget exceeded"
	comparison, e = main.CompareTestReports("base", "candidate", []main.TestRunReceipt{a}, []main.TestRunReceipt{b})
	if e != nil || comparison.PostgresStatus != "failed" {
		t.Fatalf("lock regression was not a failed comparison: %+v %v", comparison, e)
	}
	b.CleanupError = "unconfirmed cleanup"
	comparison, _ = main.CompareTestReports("base", "candidate", []main.TestRunReceipt{a}, []main.TestRunReceipt{b})
	if comparison.PostgresStatus != "inconclusive" {
		t.Fatal("cleanup failure passed")
	}
}
