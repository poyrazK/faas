package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type testPostgresSpec struct {
	Version       int                 `yaml:"version" json:"version"`
	Fixture       string              `yaml:"fixture" json:"fixture"`
	Tables        []rehearsalpg.Table `yaml:"tables" json:"tables"`
	MaxLockWaitMS int64               `yaml:"max_lock_wait_ms" json:"max_lock_wait_ms"`
}

func validateTestPostgres(s testScenario) error {
	if s.PostgresFixture == nil {
		return nil
	}
	p := s.PostgresFixture
	if s.CommandExecution == nil || s.Postgres || len(s.Cleanup) > 0 {
		return errors.New("postgres_fixture requires contained commands, platform-owned cleanup and no ordinary Postgres binding")
	}
	for _, values := range []map[string]string{s.Secrets, s.CommandExecution.Env} {
		for key := range values {
			if strings.EqualFold(key, "DATABASE_URL") {
				return errors.New("postgres_fixture owns DATABASE_URL; explicit overrides are unsupported")
			}
		}
	}
	if !filepath.IsLocal(p.Fixture) || p.Fixture == "." || path.Clean(p.Fixture) != p.Fixture || strings.Contains(p.Fixture, "\\") {
		return errors.New("PostgreSQL fixture must be a clean relative file path")
	}
	spec := rehearsalpg.Spec{Version: p.Version, FixtureSHA256: strings.Repeat("a", 64), Tables: p.Tables, MaxLockWaitMS: p.MaxLockWaitMS}
	return spec.Validate()
}

type testPostgresClient interface {
	CreateScenarioPostgres(context.Context, string, rehearsalpg.CreateRequest) (rehearsalpg.Receipt, error)
	SealScenarioPostgres(context.Context, string, string) (rehearsalpg.Evidence, error)
	DeleteScenarioPostgres(context.Context, string) error
}
type testPostgresSession struct {
	spec    rehearsalpg.Spec
	sql     string
	created bool
	receipt rehearsalpg.Receipt
}

func prepareTestPostgres(source string, s testScenario, receipt *testRunReceipt) (*testPostgresSession, error) {
	if s.PostgresFixture == nil {
		return nil, nil
	}
	if e := validateTestPostgres(s); e != nil {
		return nil, e
	}
	receipt.Postgres = &rehearsalpg.Evidence{Receipt: rehearsalpg.Receipt{Version: 1, RunID: receipt.RunID}, Status: "incomplete", Provenance: rehearsalpg.Provenance, Reason: "PostgreSQL fixture execution did not complete"}
	root, e := os.OpenRoot(source)
	if e != nil {
		return nil, e
	}
	defer func() { _ = root.Close() }()
	name := s.PostgresFixture.Fixture
	parts := strings.Split(name, "/")
	for i := range parts {
		info, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("PostgreSQL fixture cannot traverse symlinks")
		}
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("PostgreSQL fixture must be a regular file")
	}
	body, e := io.ReadAll(io.LimitReader(f, rehearsalpg.MaxFixtureBytes+1))
	if e != nil || len(body) == 0 || len(body) > rehearsalpg.MaxFixtureBytes || !utf8.Valid(body) || strings.IndexByte(string(body), 0) >= 0 {
		return nil, errors.New("PostgreSQL fixture must contain 1–65536 bytes of UTF-8 SQL")
	}
	p := s.PostgresFixture
	spec := rehearsalpg.Spec{Version: p.Version, FixtureSHA256: rehearsalpg.HashBytes(body), Tables: p.Tables, MaxLockWaitMS: p.MaxLockWaitMS}
	return &testPostgresSession{spec: spec, sql: string(body)}, nil
}

func (p *testPostgresSession) create(ctx context.Context, c testPostgresClient, r *testRunReceipt, appID string) error {
	if p == nil {
		return nil
	}
	// Even a lost create response may have allocated a database. The delete
	// endpoint treats a missing journal as a confirmed no-allocation result.
	p.created = true
	ack, e := c.CreateScenarioPostgres(ctx, r.RunID, rehearsalpg.CreateRequest{AppSlug: r.AppSlug, Spec: p.spec, SQL: p.sql})
	if e != nil {
		return e
	}
	p.created = true
	p.receipt = ack
	if ack.RunID != r.RunID || ack.AppID != appID || ack.FixtureSHA256 != p.spec.FixtureSHA256 || ack.ContractSHA256 != p.spec.ContractSHA256() {
		return errors.New("PostgreSQL fixture identity does not match the test workload")
	}
	if r.Postgres != nil {
		r.Postgres.Receipt = ack
	}
	return nil
}

func (p *testPostgresSession) finish(ctx context.Context, c testPostgresClient, r *testRunReceipt) {
	if p == nil || !p.created {
		return
	}
	if r.Actions == nil || r.Actions.Evidence == nil || r.Actions.Evidence.Status != "complete" {
		r.Error = joinTestPostgresError(r.Error, "PostgreSQL evidence requires complete action evidence")
		r.Status = "failed"
		return
	}
	e, err := c.SealScenarioPostgres(ctx, r.RunID, r.DeploymentID)
	if err != nil || e.Validate() != nil || e.ResourceID != p.receipt.ResourceID || e.InitialSHA256 != p.receipt.InitialSHA256 || e.FixtureSHA256 != p.spec.FixtureSHA256 || e.ContractSHA256 != p.spec.ContractSHA256() || e.RunID != r.RunID || e.DeploymentID != r.DeploymentID || e.AppID != p.receipt.AppID {
		r.Error = joinTestPostgresError(r.Error, "independent PostgreSQL evidence was incomplete")
		r.Status = "failed"
		return
	}
	r.Postgres = &e
	if !e.LockBudgetPassed {
		r.Error = joinTestPostgresError(r.Error, "PostgreSQL lock-wait budget exceeded")
		r.Status = "failed"
	}
}

func (p *testPostgresSession) cleanup(ctx context.Context, c testPostgresClient, r *testRunReceipt) {
	if p == nil || !p.created {
		return
	}
	if e := c.DeleteScenarioPostgres(ctx, r.RunID); e != nil {
		r.addCleanupError("PostgreSQL fixture cleanup was not confirmed")
		if r.Postgres != nil {
			r.Postgres.Status = "incomplete"
			r.Postgres.CleanupConfirmed = false
			r.Postgres.Reason = "fixture cleanup was not confirmed"
		}
	}
}
func joinTestPostgresError(old, message string) string {
	if old == "" {
		return message
	}
	return old + "; " + message
}

func compareTestPostgres(before, after testRunReceipt) *rehearsalpg.Comparison {
	c := rehearsalpg.Compare(before.Postgres, after.Postgres)
	if c != nil && (!testExecutionCompleteForEvidence(before) || !testExecutionCompleteForEvidence(after)) {
		c.Status = "inconclusive"
		c.Reason = "one or both executions or cleanup steps did not pass"
	}
	return c
}

func testExecutionCompleteForEvidence(r testRunReceipt) bool {
	if r.CleanupError != "" {
		return false
	}
	if r.Status == "passed" && r.Error == "" {
		return true
	}
	if testSuspendResumeBudgetOnlyFailure(r) {
		return true
	}
	if testColdStartBudgetOnlyFailure(r) {
		return true
	}
	return r.Status == "failed" && r.Error == "PostgreSQL lock-wait budget exceeded" && r.Postgres != nil && r.Postgres.Validate() == nil && !r.Postgres.LockBudgetPassed
}
func testPostgresComparisonStatus(runs []testRunComparison) string {
	status := ""
	for _, r := range runs {
		if r.Postgres == nil {
			continue
		}
		if r.Postgres.Status == "inconclusive" {
			return "inconclusive"
		}
		if status == "" || r.Postgres.Status == "failed" {
			status = r.Postgres.Status
		}
	}
	return status
}
func printTestPostgresComparison(out io.Writer, c *rehearsalpg.Comparison) {
	if c == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  PostgreSQL: %s (platform collector)\n", strings.ToUpper(c.Status))
	if c.Reason != "" {
		_, _ = fmt.Fprintf(out, "    %s\n", c.Reason)
	}
	for i, b := range c.Before {
		if i >= len(c.After) {
			break
		}
		a := c.After[i]
		_, _ = fmt.Fprintf(out, "    %s: baseline +%d/-%d rows; candidate +%d/-%d rows\n", b.Table, len(b.Inserted), len(b.Deleted), len(a.Inserted), len(a.Deleted))
	}
}
