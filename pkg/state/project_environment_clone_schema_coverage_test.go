// adr: 590
// adr: 1576 — CI configuration needs a strategy; execution authority stays with its source.
// adr: 623 — pending resize intents belong only to their original database.
// adr: 738
// adr: 694
// adr: 695
// adr: 708
// adr: 1701 — commercial configuration needs isolation; user email consent is not copied.
package state

import (
	"errors"
	"reflect"
	"testing"
)

func TestCloneSchemaRegistryNamesConfigurationDataAndResetBoundaries(t *testing.T) {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{
		// adr: 20261017190804, 20261017190807 — clone neither artifact pins nor original serving authority.
		"environment_build_artifact_pins":      CloneSchemaOperational,
		"environment_recovery_serving_history": CloneSchemaOperational,

		// ADR-1522–1530: recovery claims, physical bindings and completion
		// receipts belong to their original runtime owner, never to a clone.
		"instance_snapshot_recovery":    CloneSchemaOperational,
		"migration_admissions":          CloneSchemaOperational,
		"migration_sources":             CloneSchemaOperational,
		"node_lifecycle_completions":    CloneSchemaOperational,
		"operator_node_executions":      CloneSchemaOperational,
		"operator_park_executions":      CloneSchemaOperational,
		"operator_recovery_submissions": CloneSchemaOperational,
		"operator_restart_executions":   CloneSchemaOperational,

		// ADR-1562: retain delivery of original physical storage settlements.
		"object_node_settlement_outbox": CloneSchemaOperational,

		// ADR-20261017201003–15: rehearsal runs, credentials, fixture owners and
		// physical execution receipts stay with their original isolated run.
		"scenario_cold_preparations":    CloneSchemaOperational,
		"scenario_command_networks":     CloneSchemaOperational,
		"scenario_managed_work":         CloneSchemaOperational,
		"scenario_object_fixtures":      CloneSchemaOperational,
		"scenario_postgres_fixtures":    CloneSchemaOperational,
		"scenario_recorder_requests":    CloneSchemaOperational,
		"scenario_recorder_sessions":    CloneSchemaOperational,
		"scenario_simulation_attempts":  CloneSchemaOperational,
		"scenario_suspend_resumes":      CloneSchemaOperational,
		"scenario_test_action_policies": CloneSchemaOperational,
		"scenario_worker_faults":        CloneSchemaOperational,

		// Keep Product Control configuration, identity and original execution state distinct.
		"product_control_heads":               CloneSchemaConfiguration,
		"product_control_revisions":           CloneSchemaConfiguration,
		"product_control_customers":           CloneSchemaConfiguration,
		"product_control_help_requests":       CloneSchemaData,
		"product_control_email_subscriptions": CloneSchemaIdentity,
		"product_control_receipts":            CloneSchemaOperational,
		"product_control_billing_documents":   CloneSchemaOperational,
		// CI configuration stays scoped; execution authority remains at its source.
		"ci_automation_event_retries": CloneSchemaOperational,
		"ci_automation_branches":      CloneSchemaOperational,
		"ci_automation_pull_requests": CloneSchemaOperational,
		"ci_automation_events":        CloneSchemaOperational,
		"ci_automation_path_checks":   CloneSchemaOperational,
		"ci_source_check_retries":     CloneSchemaOperational,
		"ci_automation_policies":      CloneSchemaConfiguration,
		"ci_dispatch_budget":          CloneSchemaOperational,
		"ci_execution_archives":       CloneSchemaOperational,
		"ci_execution_check_retries":  CloneSchemaOperational,
		"ci_execution_checks":         CloneSchemaOperational,
		"ci_execution_profiles":       CloneSchemaConfiguration,
		"ci_executions":               CloneSchemaOperational,
		"ci_github_bindings":          CloneSchemaConfiguration,
		"ci_github_checks":            CloneSchemaOperational,
		"ci_github_triggers":          CloneSchemaOperational,
		"ci_job_attempts":             CloneSchemaOperational,
		"ci_job_runtime":              CloneSchemaOperational,
		"ci_job_runtime_objects":      CloneSchemaOperational,
		"ci_managed_objects":          CloneSchemaOperational,
		"ci_pipeline_revisions":       CloneSchemaConfiguration,
		"ci_run_events":               CloneSchemaOperational,
		"ci_runs":                     CloneSchemaOperational,
		"ci_storage_budget":           CloneSchemaOperational,
		"ci_worker_health":            CloneSchemaOperational,
		// ADR-202610171453: inventory internal hosting without copying identity or serving state.
		"hosting_sites":                                 CloneSchemaConfiguration,
		"hosting_domains":                               CloneSchemaConfiguration,
		"hosting_retention_policies":                    CloneSchemaConfiguration,
		"hosting_releases":                              CloneSchemaData,
		"hosting_automation_credentials":                CloneSchemaIdentity,
		"hosting_oidc_trusts":                           CloneSchemaIdentity,
		"hosting_domain_claims":                         CloneSchemaIdentity,
		"hosting_ci_runs":                               CloneSchemaOperational,
		"hosting_previews":                              CloneSchemaOperational,
		"hosting_promotion_events":                      CloneSchemaOperational,
		"hosting_route_publications":                    CloneSchemaOperational,
		"app_issue_lifecycle_alert_policies":            CloneSchemaConfiguration,
		"issue_lifecycle_alert_receipts":                CloneSchemaOperational,
		"workflow_automation_live_baseline_promotions":  CloneSchemaOperational,
		"workflow_automation_live_baseline_retirements": CloneSchemaOperational,
		"workflow_automation_live_suites":               CloneSchemaOperational,
		"workflow_automation_live_verifications":        CloneSchemaOperational,

		"app_issue_frequency_alert_policies":       CloneSchemaConfiguration,
		"issue_application_escalation_policies":    CloneSchemaConfiguration,
		"issue_application_reminder_policies":      CloneSchemaConfiguration,
		"issue_application_reminders":              CloneSchemaOperational,
		"issue_application_slack_policies":         CloneSchemaConfiguration,
		"issue_application_triage":                 CloneSchemaOperational,
		"issue_application_triage_activity":        CloneSchemaOperational,
		"issue_frequency_alert_receipts":           CloneSchemaOperational,
		"issue_slack_action_messages":              CloneSchemaOperational,
		"issue_slack_identities":                   CloneSchemaIdentity,
		"issue_slack_installations":                CloneSchemaConfiguration,
		"issue_slack_interaction_policies":         CloneSchemaConfiguration,
		"issue_slack_link_challenges":              CloneSchemaOperational,
		"issue_slack_oauth_flows":                  CloneSchemaOperational,
		"issue_slack_routing_policies":             CloneSchemaConfiguration,
		"issue_team_slack_deliveries":              CloneSchemaOperational,
		"issue_team_slack_destinations":            CloneSchemaConfiguration,
		"issue_team_slack_threads":                 CloneSchemaOperational,
		"issue_tracker_auto_sync":                  CloneSchemaConfiguration,
		"issue_tracker_callback_diagnostics":       CloneSchemaOperational,
		"issue_tracker_callbacks":                  CloneSchemaOperational,
		"issue_tracker_inbound_guards":             CloneSchemaOperational,
		"issue_tracker_linear_connections":         CloneSchemaConfiguration,
		"issue_tracker_linear_oauth_flows":         CloneSchemaOperational,
		"issue_tracker_source_guards":              CloneSchemaOperational,
		"issue_tracker_sync_escalation_policies":   CloneSchemaConfiguration,
		"issue_tracker_sync_history":               CloneSchemaOperational,
		"issue_tracker_sync_incidents":             CloneSchemaOperational,
		"issue_tracker_sync_notification_policies": CloneSchemaConfiguration,
		"issue_tracker_sync_notifications":         CloneSchemaOperational,
		"issue_tracker_sync_reminder_policies":     CloneSchemaConfiguration,
		"issue_tracker_sync_triage":                CloneSchemaOperational,
		"issue_tracker_sync_triage_activity":       CloneSchemaOperational,
		"native_issue_ticket_receipts":             CloneSchemaOperational,
		"workflow_initial_recovery_reviews":        CloneSchemaOperational,
		"profile_deployment_policies":              CloneSchemaConfiguration,
		"profile_investigations":                   CloneSchemaOperational,
		"profile_deployment_checks":                CloneSchemaOperational,
		"profile_route_alert_state":                CloneSchemaOperational,
		"profile_canary_checks":                    CloneSchemaOperational,
		"profile_periodic_monitors":                CloneSchemaOperational,
		"app_binding_release_policies":             CloneSchemaConfiguration,
		"app_binding_release_policy_history":       CloneSchemaConfiguration,
		"alert_rollback_actions":                   CloneSchemaOperational,
		"alert_historical_rollback_claims":         CloneSchemaOperational,
		"deployment_recovery_lineage":              CloneSchemaOperational,
		"deployment_rollback_operations":           CloneSchemaOperational,
		"deployment_dependency_gates":              CloneSchemaOperational,
		"app_environment_secret_refs":              CloneSchemaConfiguration,
		"app_environment_secret_ref_suppressions":  CloneSchemaConfiguration,
		"app_environment_workload_intents":         CloneSchemaConfiguration,
		"environment_git_sources":                  CloneSchemaConfiguration,
		"environment_external_field_owners":        CloneSchemaConfiguration,
		"environment_desired_revisions":            CloneSchemaConfiguration,
		"environment_managed_fields":               CloneSchemaConfiguration,
		"environment_management_overrides":         CloneSchemaConfiguration,
		"environment_workload_graphs":              CloneSchemaConfiguration,
		"deployment_route_policy_snapshots":        CloneSchemaConfiguration,
		"instance_runtime_config_receipts":         CloneSchemaOperational,
		"snapshot_runtime_config_receipts":         CloneSchemaOperational,
		"financial_budget_policies":                CloneSchemaConfiguration,
		"financial_budget_revisions":               CloneSchemaConfiguration,
		"financial_usage_evidence":                 CloneSchemaIdentity,
		"financial_price_snapshots":                CloneSchemaIdentity,
		"financial_evidence_coverage":              CloneSchemaOperational,
		"financial_sampling_windows":               CloneSchemaOperational,

		"object_bucket_encryption":                           CloneSchemaConfiguration,
		"object_bucket_lifecycle":                            CloneSchemaConfiguration,
		"object_bucket_notifications":                        CloneSchemaConfiguration,
		"object_bucket_object_lock":                          CloneSchemaConfiguration,
		"object_version_protection":                          CloneSchemaConfiguration,
		"managed_postgres_usage_imports":                     CloneSchemaIdentity,
		"managed_postgres_creation_receipts":                 CloneSchemaIdentity,
		"managed_postgres_restore_verifications":             CloneSchemaOperational,
		"managed_postgres_accounting_reconciliations":        CloneSchemaIdentity,
		"managed_postgres_resizes":                           CloneSchemaOperational,
		"managed_postgres_cutover_drain_plans":               CloneSchemaOperational,
		"managed_postgres_cutover_drain_plan_members":        CloneSchemaOperational,
		"managed_postgres_cutover_drains":                    CloneSchemaOperational,
		"object_bucket_versioning":                           CloneSchemaConfiguration,
		"object_s3_copy_source_grants":                       CloneSchemaConfiguration,
		"object_s3_copy_source_epochs":                       CloneSchemaConfiguration,
		"object_version_references":                          CloneSchemaData,
		"object_deletions":                                   CloneSchemaOperational,
		"object_lifecycle_scans":                             CloneSchemaOperational,
		"object_storage_capacity_reconciliations":            CloneSchemaOperational,
		"object_storage_multipart_part_grants":               CloneSchemaOperational,
		"object_storage_version_inventory_cursors":           CloneSchemaOperational,
		"object_storage_version_inventory_entries":           CloneSchemaOperational,
		"object_storage_write_admissions":                    CloneSchemaOperational,
		"project_environment_clone_object_grant_revocations": CloneSchemaOperational,

		"customer_operation_code_pins":              CloneSchemaOperational,
		"entity_outbox_acceptances":                 CloneSchemaOperational,
		"event_delivery_capacity":                   CloneSchemaOperational,
		"event_delivery_slots":                      CloneSchemaOperational,
		"event_fanout_history_summaries":            CloneSchemaOperational,
		"event_fanout_recipients":                   CloneSchemaOperational,
		"event_replay_job_items":                    CloneSchemaOperational,
		"event_replay_jobs":                         CloneSchemaOperational,
		"event_routing_backlog":                     CloneSchemaOperational,
		"event_routing_fairness":                    CloneSchemaOperational,
		"event_storage_admission":                   CloneSchemaOperational,
		"invocation_attempt_history":                CloneSchemaOperational,
		"invocation_keyed_replays":                  CloneSchemaOperational,
		"invocation_plain_replays":                  CloneSchemaOperational,
		"app_service_address_cursors":               CloneSchemaOperational,
		"workflow_automation_definitions":           CloneSchemaConfiguration,
		"workflow_automation_revisions":             CloneSchemaConfiguration,
		"workflow_code_pins":                        CloneSchemaOperational,
		"workflow_dispatch_cursors":                 CloneSchemaOperational,
		"workflow_event_code_refs":                  CloneSchemaOperational,
		"workflow_event_receipts":                   CloneSchemaOperational,
		"workflow_operation_effects":                CloneSchemaOperational,
		"workflow_run_resumes":                      CloneSchemaOperational,
		"platform_tenant_workflow_schedule_cursors": CloneSchemaOperational,
		"workflow_schedule_cursors":                 CloneSchemaOperational,
		"workflow_schedule_occurrences":             CloneSchemaOperational,
		"workflow_webhook_bindings":                 CloneSchemaConfiguration,
		"workflow_webhook_receipts":                 CloneSchemaOperational,
		"customer_operation_definitions":            CloneSchemaConfiguration,
		"customer_operations":                       CloneSchemaOperational,
		"customer_operation_milestones":             CloneSchemaOperational,
		"customer_operation_events":                 CloneSchemaOperational,
		"customer_operation_delivery_retries":       CloneSchemaOperational,
		"customer_operation_attempt_instances":      CloneSchemaOperational,
		"customer_operation_webhook_bindings":       CloneSchemaConfiguration,
		"customer_operation_webhook_receipts":       CloneSchemaOperational,
		"customer_operation_executions":             CloneSchemaOperational,
		"customer_operation_idempotency":            CloneSchemaOperational,
		"customer_operation_job_executions":         CloneSchemaOperational,
		"customer_operation_recoveries":             CloneSchemaOperational,
		"customer_operation_result_blobs":           CloneSchemaOperational,
		"customer_operation_reports":                CloneSchemaOperational,
		"customer_operation_stream_leases":          CloneSchemaOperational,
		"customer_operation_workflow_executions":    CloneSchemaOperational,
		"customer_operation_workflow_guest_claims":  CloneSchemaOperational,
		"customer_operation_workflow_state_reports": CloneSchemaOperational,
		"customer_operation_workflow_states":        CloneSchemaOperational,
		"queue_bindings":                            CloneSchemaConfiguration, "crons": CloneSchemaConfiguration, "jobs": CloneSchemaConfiguration,
		"app_webhooks": CloneSchemaConfiguration, "outbound_integration_credentials": CloneSchemaConfiguration,
		"app_work_policies": CloneSchemaConfiguration, "event_subscription_work_bindings": CloneSchemaConfiguration,
		"trigger_work_bindings": CloneSchemaConfiguration, "app_private_network_attachments": CloneSchemaConfiguration,
		"runtime_config_entries":            CloneSchemaConfiguration,
		"managed_realtime_channel_messages": CloneSchemaData, "managed_realtime_channel_heads": CloneSchemaData,
		"instances": CloneSchemaOperational, "invocations": CloneSchemaOperational, "app_tasks": CloneSchemaOperational,
		"invocation_environment_queue_admissions":    CloneSchemaOperational,
		"invocation_environment_queue_receipts":      CloneSchemaOperational,
		"deployment_runtime_environment_owners":      CloneSchemaOperational,
		"app_health_collection_state":                CloneSchemaOperational,
		"app_health_history":                         CloneSchemaOperational,
		"runtime_releases":                           CloneSchemaPlatform,
		"runtime_release_qualifications":             CloneSchemaPlatform,
		"runtime_artifact_bindings":                  CloneSchemaOperational,
		"deployment_runtime_upgrade_targets":         CloneSchemaOperational,
		"deployment_runtime_upgrade_baselines":       CloneSchemaOperational,
		"deployment_runtime_upgrade_acceptances":     CloneSchemaOperational,
		"deployment_runtime_upgrade_cutovers":        CloneSchemaOperational,
		"runtime_upgrade_operations":                 CloneSchemaOperational,
		"runtime_upgrade_verifications":              CloneSchemaOperational,
		"runtime_upgrade_gateway_rosters":            CloneSchemaPlatform,
		"runtime_upgrade_gateway_roster_head":        CloneSchemaPlatform,
		"runtime_upgrade_gateway_heartbeats":         CloneSchemaOperational,
		"runtime_upgrade_external_fence_authorities": CloneSchemaPlatform,
		"runtime_upgrade_external_fence_intents":     CloneSchemaPlatform,
		"runtime_upgrade_external_fence_receipts":    CloneSchemaOperational,
		"runtime_upgrade_native_public_startups":     CloneSchemaOperational,
		"invocation_work_environment_domains":        CloneSchemaOperational, "invocation_work_environment_admissions": CloneSchemaOperational,
		"project_environment_queue_runtime_sets": CloneSchemaOperational, "project_environment_queue_consumers": CloneSchemaOperational,
		"feature_flag_versions":           CloneSchemaConfiguration,
		"exclusive_work_policies":         CloneSchemaConfiguration,
		"exclusive_work_trigger_bindings": CloneSchemaConfiguration,
		"app_issue_impact_alert_policies": CloneSchemaConfiguration,
		"issue_ingest_tokens":             CloneSchemaConfiguration,
		"app_udp_listeners":               CloneSchemaConfiguration,
		"exclusive_work_operations":       CloneSchemaOperational,
		"exclusive_work_keys":             CloneSchemaOperational,
		"schedule_occurrences":            CloneSchemaOperational,
		"dev_bridge_sessions":             CloneSchemaOperational,
		"execution_artifact_grants":       CloneSchemaOperational,
		"execution_outbound_integrations": CloneSchemaOperational,
		"mcp_catalog_monitors":            CloneSchemaOperational,
		"mcp_oauth_retirements":           CloneSchemaOperational,
		"service_recovery":                CloneSchemaOperational,
		"service_capacity_policy":         CloneSchemaPlatform,
		"accounts":                        CloneSchemaIdentity, "orgs": CloneSchemaIdentity, "api_keys": CloneSchemaIdentity,
		"egress_policy": CloneSchemaPlatform, "compute_nodes": CloneSchemaPlatform,

		"project_release_sets": CloneSchemaConfiguration,

		// Evidence and expiring execution plans belong to the source environment.
		"binding_verification_runs":         CloneSchemaOperational,
		"binding_verification_run_steps":    CloneSchemaOperational,
		"project_binding_verification_runs": CloneSchemaOperational,
		"project_release_stages":            CloneSchemaOperational,
		"queue_binding_canaries":            CloneSchemaOperational,
	}
	for _, policy := range policies {
		if kind, ok := wanted[policy.TableName]; ok {
			if policy.Kind != kind {
				t.Fatalf("%s policy=%s, want %s", policy.TableName, policy.Kind, kind)
			}
			delete(wanted, policy.TableName)
		}
	}
	if len(wanted) > 0 {
		t.Fatalf("missing policy boundaries: %v", wanted)
	}
	dispatchReceiptColumnRegistered := false
	for _, policy := range policies {
		if policy.TableName != "customer_operation_workflow_guest_claims" {
			continue
		}
		for _, column := range policy.Columns {
			if column == "dispatch_started_at" {
				dispatchReceiptColumnRegistered = true
				break
			}
		}
		break
	}
	if !dispatchReceiptColumnRegistered {
		t.Fatal("customer operation workflow guest claims clone policy must retain dispatch_started_at receipts")
	}
	report, err := cloneSchemaCoverage(policies)
	if err != nil || !report.Known || len(report.Hash) != 64 || len(report.Blockers) == 0 {
		t.Fatalf("registered does not mean fully copyable: %+v %v", report, err)
	}
	policies[0].Kind, policies[0].Columns[0] = "changed", "changed"
	fresh, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil || fresh[0].Kind == "changed" || fresh[0].Columns[0] == "changed" {
		t.Fatalf("caller changed capture registry: %v", err)
	}
}

func TestHostingCloneInventoryRetainsCopyStrategyBlockers(t *testing.T) {
	// adr: 202610171453 — known schema does not qualify hosting configuration or artifact cloning.
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	report, err := cloneSchemaCoverage(policies)
	if err != nil || !report.Known {
		t.Fatal("registered schema must be known", report, err)
	}
	wanted := map[string]string{
		"hosting_sites":              "isolated_strategy_unavailable",
		"hosting_domains":            "isolated_strategy_unavailable",
		"hosting_retention_policies": "isolated_strategy_unavailable",
		"hosting_releases":           "isolated_data_strategy_unavailable",
	}
	for _, blocker := range report.Blockers {
		if wanted[blocker.Table] == blocker.Code {
			delete(wanted, blocker.Table)
		}
	}
	if len(wanted) != 0 {
		t.Fatal("hosting copy strategies became implicitly available", wanted)
	}
}

func TestCloneSchemaCoverageRejectsNewAndMissingTablesAndColumns(t *testing.T) {
	for _, fault := range []struct {
		name, table, column, code string
		change                    func([]ProjectEnvironmentCloneSchemaPolicy) []ProjectEnvironmentCloneSchemaPolicy
	}{
		{"new_table", "new_integration", "", "unregistered_table", func(p []ProjectEnvironmentCloneSchemaPolicy) []ProjectEnvironmentCloneSchemaPolicy {
			return append(p, ProjectEnvironmentCloneSchemaPolicy{TableName: "new_integration", Kind: CloneSchemaOperational, Columns: []string{"app_id", "config"}})
		}},
		{"new_column", "account_async_quota", "new_setting", "unregistered_column", func(p []ProjectEnvironmentCloneSchemaPolicy) []ProjectEnvironmentCloneSchemaPolicy {
			p[0].Columns = append(p[0].Columns, "new_setting")
			return p
		}},
		{"missing_column", "account_async_quota", "account_id", "missing_column", func(p []ProjectEnvironmentCloneSchemaPolicy) []ProjectEnvironmentCloneSchemaPolicy {
			p[0].Columns = p[0].Columns[1:]
			return p
		}},
		{"missing_table", "account_async_quota", "", "missing_table", func(p []ProjectEnvironmentCloneSchemaPolicy) []ProjectEnvironmentCloneSchemaPolicy { return p[1:] }},
	} {
		t.Run(fault.name, func(t *testing.T) {
			policies, err := ProjectEnvironmentCloneSchemaPolicies()
			if err != nil {
				t.Fatal(err)
			}
			report, err := cloneSchemaCoverage(fault.change(policies))
			if err != nil || report.Known {
				t.Fatalf("unregistered schema was accepted: %+v %v", report, err)
			}
			var coverageError *ProjectEnvironmentCloneSchemaCoverageError
			if !errors.As(requireKnownCloneSchema(report), &coverageError) || !errors.Is(coverageError, ErrConflict) {
				t.Fatal("capture did not return a named coverage error")
			}
			want := ProjectEnvironmentCloneCoverageBlocker{Table: fault.table, Column: fault.column, Code: fault.code}
			if !reflect.DeepEqual(coverageError.Blockers, []ProjectEnvironmentCloneCoverageBlocker{want}) {
				t.Fatalf("schema blockers = %+v, want %+v", coverageError.Blockers, want)
			}
		})
	}
}

func TestCloneSchemaCoverageCanonicalOrderAndNoInputMutation(t *testing.T) {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := cloneSchemaCoverage(policies)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(policies)-1; i < j; i, j = i+1, j-1 {
		policies[i], policies[j] = policies[j], policies[i]
	}
	for i := range policies {
		columns := policies[i].Columns
		for i, j := 0, len(columns)-1; i < j; i, j = i+1, j-1 {
			columns[i], columns[j] = columns[j], columns[i]
		}
		policies[i].Kind = "caller-cannot-classify"
	}
	firstColumns := append([]string(nil), policies[0].Columns...)
	reordered, err := cloneSchemaCoverage(policies)
	if err != nil || !reflect.DeepEqual(reordered, canonical) || !reflect.DeepEqual(policies[0].Columns, firstColumns) {
		t.Fatalf("coverage changed with input order/classification or mutated input: %v", err)
	}
	duplicate := append(policies, policies[0])
	if _, err := cloneSchemaCoverage(duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate table accepted: %v", err)
	}
}
