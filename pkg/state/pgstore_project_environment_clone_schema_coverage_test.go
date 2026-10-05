//go:build !no_pg

// adr: 585
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This migrated-schema contract is the gate for new application resources and
// settings. It uses the authoritative migrations, not the sqlc schema snapshot.
func TestPgCloneSchemaRegistryCoversMigratedApplicationTablesAndFailsBeforeReservation(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "schema-coverage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "schema-coverage"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "schema-workload", WorkloadName: "schema-workload", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:coverage"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/coverage.ext4", "layers/coverage-"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "PRIVATE", "must-not-be-in-coverage"); err != nil {
		t.Fatal(err)
	}
	coverage, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, a.ID, p.ID)
	if err != nil || !coverage.Known || len(coverage.Hash) != 64 {
		t.Fatalf("migrated schema needs explicit policies: %+v %v", coverage.Blockers, err)
	}
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil || len(coverage.Tables) != len(policies) {
		t.Fatalf("schema inventory is incomplete: %d/%d %v", len(coverage.Tables), len(policies), err)
	}
	for _, want := range []state.ProjectEnvironmentCloneCoverageBlocker{
		{Table: "environment_external_field_owners", Code: "isolated_strategy_unavailable"},
		{Table: "queue_bindings", Code: "isolated_strategy_unavailable"},
		{Table: "financial_budget_policies", Code: "isolated_strategy_unavailable"},
		{Table: "financial_budget_revisions", Code: "isolated_strategy_unavailable"},
		{Table: "object_bucket_encryption", Code: "isolated_strategy_unavailable"},
		{Table: "object_bucket_lifecycle", Code: "isolated_strategy_unavailable"},
		{Table: "object_bucket_notifications", Code: "isolated_strategy_unavailable"},
		{Table: "object_bucket_object_lock", Code: "isolated_strategy_unavailable"},
		{Table: "object_version_protection", Code: "isolated_strategy_unavailable"},
		{Table: "object_bucket_versioning", Code: "isolated_strategy_unavailable"},
		{Table: "object_s3_copy_source_grants", Code: "isolated_strategy_unavailable"},
		{Table: "object_s3_copy_source_epochs", Code: "isolated_strategy_unavailable"},
		{Table: "object_version_references", Code: "isolated_data_strategy_unavailable"},

		{Table: "app_webhooks", Code: "isolated_strategy_unavailable"},
		{Table: "managed_realtime_channel_messages", Code: "isolated_data_strategy_unavailable"},
		{Table: "runtime_config_entries", Code: "isolated_strategy_unavailable"},
		{Table: "feature_flag_versions", Code: "isolated_strategy_unavailable"},
		{Table: "exclusive_work_policies", Code: "isolated_strategy_unavailable"},
		{Table: "exclusive_work_trigger_bindings", Code: "isolated_strategy_unavailable"},
		{Table: "app_udp_listeners", Code: "isolated_strategy_unavailable"},
		{Table: "app_issue_impact_alert_policies", Code: "isolated_strategy_unavailable"},
		{Table: "issue_ingest_tokens", Code: "isolated_strategy_unavailable"},
	} {
		assertCloneCoverageBlocker(t, coverage, want)
	}
	rawCoverage, err := json.Marshal(coverage)
	if err != nil || strings.Contains(string(rawCoverage), "must-not-be-in-coverage") {
		t.Fatal("coverage read tenant values")
	}
	other, err := s.CreateAccount(ctx, "other-schema-coverage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, other.ID, p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account schema report: %v", err)
	}
	if _, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, a.ID, "malformed"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("invalid scope identity: %v", err)
	}
	request := state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "captured", IdempotencyKey: "known"}
	op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request)
	if err != nil {
		t.Fatalf("known schema blocked internal typed capture: %v", err)
	}
	for _, fault := range []struct {
		name, add, remove string
		blockers          []state.ProjectEnvironmentCloneCoverageBlocker
	}{
		{"column", "alter table app_envs add column future_setting text", "alter table app_envs drop column future_setting", []state.ProjectEnvironmentCloneCoverageBlocker{
			{Table: "app_envs", Column: "future_setting", Code: "unregistered_column"},
		}},
		{"unowned_table", "create table test_clone_unowned_configuration(name text,value jsonb)", "drop table test_clone_unowned_configuration", []state.ProjectEnvironmentCloneCoverageBlocker{
			{Table: "test_clone_unowned_configuration", Code: "unregistered_table"},
		}},
		{"indirect_tables", "create table test_clone_future_config(id uuid primary key,owner uuid references accounts(id),config jsonb); create table test_clone_future_child(parent uuid references test_clone_future_config(id),config jsonb)", "drop table test_clone_future_child; drop table test_clone_future_config", []state.ProjectEnvironmentCloneCoverageBlocker{
			{Table: "test_clone_future_config", Code: "unregistered_table"}, {Table: "test_clone_future_child", Code: "unregistered_table"},
		}},
		{"renamed_table", "alter table app_log_drains rename to test_clone_renamed_drains", "alter table test_clone_renamed_drains rename to app_log_drains", []state.ProjectEnvironmentCloneCoverageBlocker{
			{Table: "app_log_drains", Code: "missing_table"}, {Table: "test_clone_renamed_drains", Code: "unregistered_table"},
		}},
	} {
		t.Run(fault.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, fault.add); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, fault.remove); err != nil {
					t.Fatal(err)
				}
			}()
			report, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, a.ID, p.ID)
			if err != nil || report.Known || report.Hash == coverage.Hash {
				t.Fatalf("schema drift was accepted: %+v %v", report, err)
			}
			for _, blocker := range fault.blockers {
				assertCloneCoverageBlocker(t, report, blocker)
			}
			newRequest := request
			newRequest.TargetEnvironment, newRequest.IdempotencyKey = "new-"+strings.ReplaceAll(fault.name, "_", "-"), "new-"+fault.name
			var named *state.ProjectEnvironmentCloneSchemaCoverageError
			if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, newRequest); !errors.As(err, &named) || len(named.Blockers) != len(fault.blockers) {
				t.Fatalf("capture did not reject named schema blockers: %v", err)
			}
			if _, err := s.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, a.ID, p.ID, newRequest.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unknown configuration reserved an operation: %v", err)
			}
			if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, newRequest.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unknown configuration created target: %v", err)
			}
			if replay, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || replay.ID != op.ID || replay.SourceRevisionHash != op.SourceRevisionHash {
				t.Fatalf("committed replay depended on changed live schema: %+v %v", replay, err)
			}
		})
	}
	restored, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, a.ID, p.ID)
	if err != nil || !restored.Known || restored.Hash != coverage.Hash {
		t.Fatalf("restored schema failed coverage: %+v %v", restored.Blockers, err)
	}
}

func assertCloneCoverageBlocker(t *testing.T, coverage state.ProjectEnvironmentCloneSchemaCoverage, want state.ProjectEnvironmentCloneCoverageBlocker) {
	t.Helper()
	for _, actual := range coverage.Blockers {
		if actual == want {
			return
		}
	}
	t.Fatalf("missing blocker %+v in %+v", want, coverage.Blockers)
}
