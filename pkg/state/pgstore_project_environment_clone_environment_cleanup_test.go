//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

const cloneEnvironmentCleanupMigration = "20261011232006408_clone_environment_metadata_cleanups.sql"

func TestPgCloneEnvironmentCleanupAtomicRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "domain", "route_budget", "scope", "binding_verification", "project_binding_verification", "release_stage", "hosting_site", "mcp_readiness_policy", "mcp_readiness_incident", "mcp_readiness_maintenance", "protected", "stale", "missing_workloads", "deployed", "foreign_deployment", "non_fk_uuid", "other_clone"} {
		t.Run(fault, func(t *testing.T) {
			phase := "materialized"
			if fault == "deployed" {
				phase = "deployed"
			}
			s, pool, l, appID := configurationCleanupFixtureForPhase(t, true, phase)
			original, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if fault != "missing_workloads" {
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			envID := original.Plan.EnvironmentID
			switch fault {
			case "write":
				_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_environment_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_environment_cleanup BEFORE INSERT ON project_environment_clone_environment_cleanups FOR EACH ROW EXECUTE FUNCTION reject_environment_cleanup();`)
			case "domain":
				_, err = pool.Exec(t.Context(), "INSERT INTO custom_domains(domain,app_id,environment_id) VALUES('borrowed.example.com',$1,$2)", appID, envID)
			case "route_budget":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_environment_route_budgets(account_id,app_id,scope,project_id,environment_id,inherit,enabled,revision,routes) VALUES($1,$2,$3,$4,$5,false,false,1,'[]')", l.Operation.AccountID, appID, l.Operation.TargetEnvironment, l.Operation.ProjectID, envID)
			case "scope":
				_, err = pool.Exec(t.Context(), "INSERT INTO github_deploy_branches(project_id,branch,scope) VALUES($1,'borrowed',$2)", l.Operation.ProjectID, l.Operation.TargetEnvironment)
			case "binding_verification":
				_, err = pool.Exec(t.Context(), "INSERT INTO binding_verification_runs(id,account_id,app_id,deployment_id,scope,status,snapshot,created_at,expires_at) VALUES(gen_random_uuid(),$1,$2,$3,$4,'passed','{}',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '55 minutes')", l.Operation.AccountID, appID, original.Plan.Workloads[0].SourceDeploymentID, l.Operation.TargetEnvironment)
			case "project_binding_verification":
				_, err = pool.Exec(t.Context(), "INSERT INTO project_binding_verification_runs(id,account_id,project_id,environment_slug,status,snapshot,created_at,expires_at) VALUES(gen_random_uuid(),$1,$2,$3,'passed','{}',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '55 minutes')", l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment)
			case "release_stage":
				_, err = pool.Exec(t.Context(), "INSERT INTO project_release_stages(id,account_id,project_id,environment_slug,manifest,targets,created_at,expires_at,cancelled) VALUES(gen_random_uuid(),$1,$2,$3,'{}','{\"retained\":true}',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '55 minutes',true)", l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment)
			case "hosting_site":
				_, err = pool.Exec(t.Context(), "INSERT INTO hosting_sites(id,account_id,project_id,environment,name,hostname,origin_id) VALUES(gen_random_uuid(),$1,$2,$3,'borrowed','borrowed.hosting.example',repeat('a',64))", l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment)
			case "mcp_readiness_policy":
				_, err = pool.Exec(t.Context(), "INSERT INTO app_mcp_readiness_policies(app_id,scope,policy) VALUES($1,$2,jsonb_build_object('scope',$2::text,'enabled',false))", appID, l.Operation.TargetEnvironment)
			case "mcp_readiness_incident":
				_, err = pool.Exec(t.Context(), `WITH rule AS (INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed,mcp_readiness_scope) VALUES($1,$2,'borrowed','mcp_readiness_unhealthy','gte',1,'5m','https://example.com/hook','\x00',$3) RETURNING id)
				INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at,resolved_at) SELECT gen_random_uuid(),rule.id,$1,$2,$3,'',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '55 minutes' FROM rule`, l.Operation.AccountID, appID, l.Operation.TargetEnvironment)
			case "mcp_readiness_maintenance":
				_, err = pool.Exec(t.Context(), "INSERT INTO mcp_readiness_maintenance_windows(id,account_id,app_id,scope,starts_at,ends_at,reason,created_by,created_at,cancelled_at,cancelled_by,cancel_reason) VALUES(gen_random_uuid(),$1,$2,$3,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour',$4,$1,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '90 minutes',$1,$4)", l.Operation.AccountID, appID, l.Operation.TargetEnvironment, "borrowed")
			case "foreign_deployment":
				_, err = pool.Exec(t.Context(), `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'scope',$2::text,'reason','foreign','revision',d.revision+1))).* FROM deployments d WHERE d.id=$3`, uuid.NewString(), l.Operation.TargetEnvironment, original.Plan.Workloads[0].SourceDeploymentID)
			case "non_fk_uuid":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_runtime_environment_owners(deployment_id,environment_id) VALUES($1,$2) ON CONFLICT(deployment_id) DO UPDATE SET environment_id=excluded.environment_id", original.Plan.Workloads[0].SourceDeploymentID, envID)
			case "other_clone":
				_, err = pool.Exec(t.Context(), "INSERT INTO project_environment_clone_operations(account_id,project_id,source_environment,target_environment,idempotency_key,source_revision_hash) VALUES($1,$2,$3,'foreign-stage','borrower',$4)", l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment, strings.Repeat("a", 64))
			case "protected":
				_, err = pool.Exec(t.Context(), "UPDATE project_environments SET protected=true WHERE id=$1", envID)
			case "stale":
				l.Token = uuid.NewString()
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l)
			if fault == "none" && err != nil {
				t.Fatal("environment cleanup", err)
			}
			if fault != "none" && err == nil {
				t.Fatal("unsafe environment deletion committed")
			}
			var environment, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environments WHERE id=$1),EXISTS(SELECT 1 FROM project_environment_clone_environment_cleanups WHERE operation_id=$2)", envID, l.Operation.ID).Scan(&environment, &receipt); err != nil {
				t.Fatal(err)
			}
			if fault != "none" {
				if !environment || receipt {
					t.Fatal("partial deletion survived")
				}
				return
			}
			if environment || !receipt {
				t.Fatal("environment removal incomplete")
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal("lost commit reply", err)
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
				t.Fatal("workload receipt replay", err)
			}
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal("settings receipt replay", err)
			}
			replay, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil || dispatch || !reflect.DeepEqual(original.Plan, replay.Plan) {
				t.Fatal("native authority changed or redispatched", err)
			}
			report, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if err != nil || report.EnvironmentID != envID || report.OwnedDeployments != 0 || report.ForeignDeployments != 0 {
				t.Fatal("post-cleanup inventory", report, err)
			}
			for table, count := range report.References {
				if count != 0 {
					t.Fatal("post-cleanup reference", table, count)
				}
			}
			var evidence bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environment_clone_materializations WHERE operation_id=$1) AND EXISTS(SELECT 1 FROM project_environment_clone_configuration_ownership WHERE operation_id=$1) AND EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE operation_id=$1)", l.Operation.ID).Scan(&evidence); err != nil || !evidence {
				t.Fatal("original evidence removed", err)
			}
		})
	}
}

func TestPgCloneEnvironmentCleanupRejectsChangedEvidence(t *testing.T) {
	for _, fault := range []string{"receipt", "missing", "workload_receipt", "time", "environment_id", "replacement_id", "replacement_slug", "late_scope"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixtureForPhase(t, true, "materialized")
			original, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_environment_cleanups SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_environment_cleanups WHERE operation_id=$1", l.Operation.ID)
			case "workload_receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET cleaned_at=cleaned_at+interval '1 microsecond' WHERE operation_id=$1", l.Operation.ID)
			case "time":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_environment_cleanups SET cleaned_at=cleaned_at-interval '1 day' WHERE operation_id=$1", l.Operation.ID)
			case "environment_id":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_environment_cleanups SET environment_id=$2 WHERE operation_id=$1", l.Operation.ID, uuid.NewString())
			case "replacement_id", "replacement_slug":
				id, slug := original.Plan.EnvironmentID, "foreign-environment"
				if fault == "replacement_slug" {
					id, slug = uuid.NewString(), l.Operation.TargetEnvironment
				}
				_, err = pool.Exec(t.Context(), "INSERT INTO project_environments(id,account_id,project_id,slug) VALUES($1,$2,$3,$4)", id, l.Operation.AccountID, l.Operation.ProjectID, slug)
			case "late_scope":
				_, err = pool.Exec(t.Context(), "INSERT INTO github_deploy_branches(project_id,branch,scope) VALUES($1,'late',$2)", l.Operation.ProjectID, l.Operation.TargetEnvironment)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed cleanup accepted", fault, err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("changed cleanup granted native replay", fault, err)
			}
			report, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneEnvironmentBorrowers{}) {
				t.Fatal("partial inventory after changed cleanup", report, err)
			}
		})
	}
}

func dropEmptyCloneEnvironmentCleanupForParentMigration(t *testing.T, tx pgx.Tx) {
	t.Helper()
	dropEmptyCloneCompensationReceiptForParentMigration(t, tx)
	raw, err := migrations.FS.ReadFile(cloneEnvironmentCleanupMigration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("dependent environment cleanup downgrade", err)
	}
}

func TestPgCloneEnvironmentCleanupMigrationRefusesEvidence(t *testing.T) {
	s, pool, l, _ := configurationCleanupFixtureForPhase(t, true, "materialized")
	raw, err := migrations.FS.ReadFile(cloneEnvironmentCleanupMigration)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	dropEmptyCloneCompensationReceiptForParentMigration(t, tx)
	for _, statement := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_environment_cleanup_down_no_evidence" {
		t.Fatal("retained evidence downgrade", err)
	}
	_ = tx.Rollback(t.Context())
}

// Scope enums used for account/runtime configuration and historical clone or
// tombstone journals are reviewed exceptions. A new concrete environment/scope
// column must be classified before the deletion inventory can stay complete.
func TestPgCloneEnvironmentCleanupScopeColumnInventory(t *testing.T) {
	_, _, pool := pgWithPool(t)
	rows, err := pool.Query(t.Context(), `SELECT c.table_name||'.'||c.column_name FROM information_schema.columns c JOIN information_schema.tables t USING(table_schema,table_name) WHERE c.table_schema='public' AND t.table_type='BASE TABLE' AND c.column_name IN ('environment_id','environment_slug','environment','scope','deployment_scope','managed_scope','queue_binding_scope','queue_binding_environment_id') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"app_binding_release_policies.scope",
		"app_binding_release_policy_history.scope",
		"app_config_guards.scope",    // Closed and open config guard windows are change history; app_envs already blocks while configuration remains.
		"app_config_revisions.scope", // Append-only configuration revision history.
		"app_environment_route_budgets.environment_id",
		"app_environment_route_budgets.scope",
		"app_environment_secret_ref_suppressions.environment_id",
		"app_environment_secret_ref_suppressions.scope",
		"app_environment_secret_refs.environment_id",
		"app_environment_secret_refs.scope",
		"app_environment_workload_intents.environment_id",
		"app_envs.scope",
		"app_issues.environment",
		"app_mcp_readiness_policies.scope",       // Per-environment readiness gate configuration blocks scope cleanup.
		"app_mcp_readiness_policy_history.scope", // Append-only policy audit history is retained evidence.
		"app_runtime_config_scope_changes.scope",
		"app_secret_revocations.scope",
		"app_secret_runtime_reload_observations.scope",
		"app_secrets.scope",
		"app_tasks.deployment_scope",
		"app_webhooks.scope",
		"application_standard_assignments.scope", // ADR-595 hierarchy level (organization/project/application), not an environment.
		"binding_verification_runs.scope",        // Retained verification evidence blocks deployment and scope cleanup.
		"custom_domains.environment_id",
		"customer_operation_approval_forms.scope",
		"customer_operation_concurrency_policies.scope",
		"customer_operation_definitions.scope",
		"customer_operation_queue.scope",
		"customer_operation_webhook_bindings.scope",
		"customer_operation_workflow_states.scope",
		"customer_workflow_readiness_subscriptions.scope",
		"data_upstreams.deployment_scope",
		"data_upstreams.scope",
		"deployment_openapi_snapshots.scope",
		"deployment_rollback_operations.scope",
		"deployment_rollbacks.scope", // Rollback outcome history; deployment_rollback_operations blocks in-flight work.
		"deployment_route_policy_snapshots.scope",
		"deployment_runtime_environment_owners.environment_id",
		"deployments.scope",
		"dev_bridge_sessions.environment_id",
		"dev_bridge_sessions.scope",
		"environment_external_field_owners.environment_id",
		"environment_git_sources.environment_id",
		"environment_gitops_runtime_effects.environment_slug",
		"environment_managed_fields.environment_id",
		"environment_management_overrides.environment_id",
		"environment_workload_graphs.environment_id",
		"environment_workload_retirements.environment_id", // ADR-1331: immutable retained retirement evidence; journal/source deletion guards prevent cleanup cascades.
		"exclusive_work_keys.environment_id",
		"feature_flag_versions.environment_id",
		"financial_budget_stop_targets.environment_id",      // In-flight (requested/draining) budget stops block scope cleanup; enforced and failed ones are retained history.
		"financial_preview_command_runtimes.environment_id", // Preparing/active preview runtimes block scope cleanup; retired rows are retained history.
		"github_deploy_branches.scope",
		"hosting_oidc_trusts.environment", // GitHub Actions OIDC environment claim, not a Gregale environment; its site FK is classified.
		"hosting_sites.environment",       // Sites reference the environment through a composite foreign key.
		"instance_runtime_config_receipts.scope",
		"internal_mail_reputation_events.scope", // Mail reputation scope (producer or domain), not an environment.
		"internal_mail_reputation_holds.scope",  // Mail reputation scope (producer or domain), not an environment.
		"invocation_environment_queue_admissions.environment_id",
		"invocation_work_environment_admissions.environment_id",
		"invocation_work_environment_domains.environment_id",
		"invocations.deployment_scope",
		"invocations.environment_id",
		"issue_group_links.environment", // Scoped foreign keys require app_issues parents; their deletion guard retains these links.
		"issue_ingest_tokens.environment",
		"issue_split_rules.environment", // Scoped foreign keys require app_issues parents; their deletion guard retains these rules.
		"managed_postgres_bindings.scope",
		"managed_postgres_cutover_drain_plan_members.deployment_scope", // Immutable captured retirement evidence survives environment retention.
		"managed_postgres_cutovers.scope",
		"mcp_readiness_incidents.scope",           // Rule-owned incident state blocks scope cleanup until its rule retires it.
		"mcp_readiness_maintenance_windows.scope", // Cancelled or expired windows still block, like other inactive scope rows.
		"object_buckets.scope",
		"object_storage_s3_credentials.managed_scope",
		"pg_ratelimit_counters.scope",
		"platform_mail_deployment_fences.scope",              // Monotonic mail outbox fence; a recreated environment continues its revisions.
		"production_lifecycle_reviews.scope",                 // Retained historical deployment review evidence.
		"project_binding_verification_runs.environment_slug", // Scope-only verification receipts block metadata cleanup.
		"project_environment_approvals.environment_slug",
		"project_environment_cleanup_jobs.environment_slug",
		"project_environment_clone_configuration_ownership.environment_id",
		"project_environment_clone_deployment_cleanups.environment_id",
		"project_environment_clone_environment_cleanups.environment_id",
		"project_environment_clone_materializations.environment_id",
		"project_environment_clone_postgres_archives.scope",
		"project_environment_clone_postgres_checkpoint_selections.scope",
		"project_environment_clone_postgres_contents.scope",
		"project_environment_clone_postgres_copy_readers.scope",
		"project_environment_clone_postgres_database_plans.scope",
		"project_environment_clone_postgres_database_sql_pins.scope",
		"project_environment_clone_postgres_inventories.scope",
		"project_environment_clone_postgres_membership_plans.scope",
		"project_environment_clone_postgres_role_plans.scope",
		"project_environment_clone_postgres_target_sql_pins.scope",
		"project_environment_clone_postgres_verifications.scope",
		"project_environment_config_versions.environment_slug",
		"project_environment_edge_policies.environment_slug",
		"project_environment_qualifications.environment_slug",
		"project_environment_queue_runtime_sets.environment_id",
		"project_environment_route_policies.environment_slug",
		"project_environment_secrets.environment_slug", // ADR-1139: a trigger deletes them with the environment row.
		"project_environment_workload_heads.environment_id",
		"project_environment_workload_specs.environment_id",
		"project_release_sets.environment_slug",
		"project_release_stages.environment_slug", // Retained stages reference the environment through a composite foreign key.
		"queue_bindings.deployment_scope",
		"queue_bindings.environment_id",
		"runtime_config_apply_receipts.scope", // Platform runtime-config scope (global/control_plane/daemon/node), not an environment.
		"runtime_config_entries.scope",
		"runtime_config_operations.scope",
		"runtime_config_revisions.scope",
		"runtime_environment_scaling_states.environment_id",
		"runtime_environment_scaling_states.scope",
		"runtime_instance_config_proofs.environment_id",
		"runtime_instance_config_proofs.scope",
		"snapshot_runtime_config_receipts.scope",
		"triggers.queue_binding_environment_id",
		"triggers.queue_binding_scope",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("environment scope inventory changed; review deletion guards and retained evidence exceptions", actual)
	}
}

func TestPgCloneEnvironmentCleanupBlocksConcurrentBorrowers(t *testing.T) {
	s, pool, l, appID := configurationCleanupFixtureForPhase(t, true, "materialized")
	native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(ctx) }()
	var gatePID int32
	if err = gate.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&gatePID); err != nil {
		t.Fatal(err)
	}
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_xact_lock(59011105)"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION pause_environment_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(59011105); RETURN NEW; END $$; CREATE TRIGGER pause_environment_cleanup BEFORE INSERT ON project_environment_clone_environment_cleanups FOR EACH ROW EXECUTE FUNCTION pause_environment_cleanup();`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- s.CleanupProjectEnvironmentCloneEnvironmentForLease(ctx, l) }()
	// Pause after deletion but before receipt persistence. FK and scope-only
	// borrowers must be unable to enter this commit window.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))", gatePID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err = <-result:
			t.Fatal("cleanup did not reach receipt gate", err)
		case <-ctx.Done():
			t.Fatal("cleanup never reached receipt gate", ctx.Err())
		case <-ticker.C:
		}
	}
	for _, borrower := range []string{"foreign_key", "scope", "workflow_state", "workflow_readiness", "binding_verification", "project_binding_verification", "release_stage", "mcp_readiness_policy", "mcp_readiness_incident", "mcp_readiness_maintenance"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='150ms'"); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if borrower == "foreign_key" {
			_, err = tx.Exec(ctx, "INSERT INTO custom_domains(domain,app_id,environment_id) VALUES('racing.example.com',$1,$2)", appID, native.Plan.EnvironmentID)
		} else if borrower == "workflow_readiness" {
			_, err = tx.Exec(ctx, "LOCK TABLE customer_workflow_readiness_subscriptions IN ROW EXCLUSIVE MODE")
		} else if borrower == "workflow_state" {
			_, err = tx.Exec(ctx, "LOCK TABLE customer_operation_workflow_states IN ROW EXCLUSIVE MODE")
		} else if borrower == "binding_verification" {
			_, err = tx.Exec(ctx, "LOCK TABLE binding_verification_runs IN ROW EXCLUSIVE MODE")
		} else if borrower == "project_binding_verification" {
			_, err = tx.Exec(ctx, "LOCK TABLE project_binding_verification_runs IN ROW EXCLUSIVE MODE")
		} else if borrower == "release_stage" {
			_, err = tx.Exec(ctx, "LOCK TABLE project_release_stages IN ROW EXCLUSIVE MODE")
		} else if borrower == "mcp_readiness_policy" {
			_, err = tx.Exec(ctx, "LOCK TABLE app_mcp_readiness_policies IN ROW EXCLUSIVE MODE")
		} else if borrower == "mcp_readiness_incident" {
			_, err = tx.Exec(ctx, "LOCK TABLE mcp_readiness_incidents IN ROW EXCLUSIVE MODE")
		} else if borrower == "mcp_readiness_maintenance" {
			_, err = tx.Exec(ctx, "LOCK TABLE mcp_readiness_maintenance_windows IN ROW EXCLUSIVE MODE")
		} else {
			_, err = tx.Exec(ctx, "INSERT INTO github_deploy_branches(project_id,branch,scope) VALUES($1,'racing',$2)", l.Operation.ProjectID, l.Operation.TargetEnvironment)
		}
		_ = tx.Rollback(ctx)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
			t.Fatal("borrower entered deletion window", borrower, err)
		}
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatal("cleanup after borrower rejection", err)
		}
	case <-ctx.Done():
		t.Fatal("cleanup did not finish", ctx.Err())
	}
	if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
		t.Fatal("concurrent recovery", err)
	}
}
