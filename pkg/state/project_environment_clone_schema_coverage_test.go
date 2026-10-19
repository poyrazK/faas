// adr: 590
// adr: 1576 — CI configuration needs a strategy; execution authority stays with its source.
// adr: 623 — pending resize intents belong only to their original database.
// adr: 738
// adr: 694
// adr: 695
// adr: 708
// adr: 1701 — commercial configuration needs isolation; user email consent is not copied.
// adr: 749 — notification channels and rule bindings are customer configuration.
// adr: 747 — SLO definitions are configuration; hourly rollups are operational.
// adr: 748 — synthetic check definitions are configuration; runs are operational.
package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"testing"
)

func TestCloneSchemaRegistryNamesConfigurationDataAndResetBoundaries(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{
		// adr:1660,1662,1665,1666 — saved authority stays with its source;
		// public compile inputs require an isolated configuration strategy.
		"deployment_build_dependency_gates": state.CloneSchemaOperational,
		"deployment_public_build_urls":      state.CloneSchemaConfiguration,
		"migration_cutovers":                state.CloneSchemaOperational,
		"data_repair_rehearsals":            state.CloneSchemaOperational,
		"migration_verifications":           state.CloneSchemaOperational,
		"project_migrations":                state.CloneSchemaOperational,

		// MCP policies and notification credentials need an isolated strategy.
		// Evidence, overrides, leases and incident delivery authority stay at source.
		"app_mcp_readiness_policies":               state.CloneSchemaConfiguration,
		"app_mcp_readiness_policy_history":         state.CloneSchemaConfiguration,
		"deployment_mcp_readiness_evidence":        state.CloneSchemaOperational,
		"deployment_mcp_readiness_monitors":        state.CloneSchemaOperational,
		"deployment_mcp_readiness_overrides":       state.CloneSchemaOperational,
		"mcp_readiness_attention_actions":          state.CloneSchemaOperational,
		"mcp_readiness_fallback_policies":          state.CloneSchemaConfiguration,
		"mcp_readiness_incident_attention":         state.CloneSchemaOperational,
		"mcp_readiness_incident_cursors":           state.CloneSchemaOperational,
		"mcp_readiness_incident_events":            state.CloneSchemaOperational,
		"mcp_readiness_incidents":                  state.CloneSchemaOperational,
		"mcp_readiness_maintenance_windows":        state.CloneSchemaConfiguration,
		"mcp_readiness_notification_actions":       state.CloneSchemaOperational,
		"mcp_readiness_pagerduty_ack_events":       state.CloneSchemaOperational,
		"mcp_readiness_pagerduty_acknowledgements": state.CloneSchemaConfiguration,
		"mcp_readiness_pagerduty_policies":         state.CloneSchemaConfiguration,
		"mcp_readiness_reminder_defaults":          state.CloneSchemaConfiguration,

		// Rehearsal authority, admission policies and evidence belong to the
		// original run; cloning must never enroll or resume its execution.
		"scenario_cold_preparations":          state.CloneSchemaOperational,
		"scenario_command_networks":           state.CloneSchemaOperational,
		"scenario_managed_work":               state.CloneSchemaOperational,
		"scenario_object_fixtures":            state.CloneSchemaOperational,
		"scenario_postgres_fixtures":          state.CloneSchemaOperational,
		"scenario_postgres_faults":            state.CloneSchemaOperational,
		"resource_namespaces":                 state.CloneSchemaIdentity,
		"scenario_recorder_requests":          state.CloneSchemaOperational,
		"scenario_recorder_sessions":          state.CloneSchemaOperational,
		"scenario_simulation_attempts":        state.CloneSchemaOperational,
		"scenario_suspend_resumes":            state.CloneSchemaOperational,
		"scenario_test_action_policies":       state.CloneSchemaOperational,
		"scenario_worker_faults":              state.CloneSchemaOperational,
		"exclusive_work_effects":              state.CloneSchemaOperational,
		"product_control_heads":               state.CloneSchemaConfiguration,
		"product_control_revisions":           state.CloneSchemaConfiguration,
		"product_control_customers":           state.CloneSchemaConfiguration,
		"product_control_help_requests":       state.CloneSchemaData,
		"product_control_email_subscriptions": state.CloneSchemaIdentity,
		"product_control_receipts":            state.CloneSchemaOperational,
		"product_control_billing_documents":   state.CloneSchemaOperational,
		// CI configuration stays scoped; execution authority remains at its source.
		"ci_automation_event_retries": state.CloneSchemaOperational,
		"ci_automation_branches":      state.CloneSchemaOperational,
		"ci_automation_pull_requests": state.CloneSchemaOperational,
		"ci_automation_events":        state.CloneSchemaOperational,
		"ci_automation_path_checks":   state.CloneSchemaOperational,
		"ci_source_check_retries":     state.CloneSchemaOperational,
		"ci_automation_policies":      state.CloneSchemaConfiguration,
		"ci_dispatch_budget":          state.CloneSchemaOperational,
		"ci_execution_archives":       state.CloneSchemaOperational,
		"ci_execution_check_retries":  state.CloneSchemaOperational,
		"ci_execution_checks":         state.CloneSchemaOperational,
		"ci_execution_profiles":       state.CloneSchemaConfiguration,
		"ci_executions":               state.CloneSchemaOperational,
		"ci_github_bindings":          state.CloneSchemaConfiguration,
		"ci_github_checks":            state.CloneSchemaOperational,
		"ci_github_triggers":          state.CloneSchemaOperational,
		"ci_job_attempts":             state.CloneSchemaOperational,
		"ci_job_runtime":              state.CloneSchemaOperational,
		"ci_job_runtime_objects":      state.CloneSchemaOperational,
		"ci_managed_objects":          state.CloneSchemaOperational,
		"ci_pipeline_revisions":       state.CloneSchemaConfiguration,
		"ci_run_events":               state.CloneSchemaOperational,
		"ci_runs":                     state.CloneSchemaOperational,
		"ci_storage_budget":           state.CloneSchemaOperational,
		"ci_worker_health":            state.CloneSchemaOperational,
		// ADR-202610171453: inventory internal hosting without copying identity or serving state.
		"hosting_sites":                                 state.CloneSchemaConfiguration,
		"hosting_domains":                               state.CloneSchemaConfiguration,
		"hosting_retention_policies":                    state.CloneSchemaConfiguration,
		"hosting_releases":                              state.CloneSchemaData,
		"hosting_automation_credentials":                state.CloneSchemaIdentity,
		"hosting_oidc_trusts":                           state.CloneSchemaIdentity,
		"hosting_domain_claims":                         state.CloneSchemaIdentity,
		"hosting_ci_runs":                               state.CloneSchemaOperational,
		"hosting_previews":                              state.CloneSchemaOperational,
		"hosting_promotion_events":                      state.CloneSchemaOperational,
		"hosting_route_publications":                    state.CloneSchemaOperational,
		"app_issue_lifecycle_alert_policies":            state.CloneSchemaConfiguration,
		"issue_lifecycle_alert_receipts":                state.CloneSchemaOperational,
		"workflow_automation_live_baseline_promotions":  state.CloneSchemaOperational,
		"workflow_automation_live_baseline_retirements": state.CloneSchemaOperational,
		"workflow_automation_live_suites":               state.CloneSchemaOperational,
		"workflow_automation_live_verifications":        state.CloneSchemaOperational,

		"app_issue_frequency_alert_policies":       state.CloneSchemaConfiguration,
		"issue_application_escalation_policies":    state.CloneSchemaConfiguration,
		"issue_application_reminder_policies":      state.CloneSchemaConfiguration,
		"issue_application_reminders":              state.CloneSchemaOperational,
		"issue_application_slack_policies":         state.CloneSchemaConfiguration,
		"issue_application_triage":                 state.CloneSchemaOperational,
		"issue_application_triage_activity":        state.CloneSchemaOperational,
		"issue_frequency_alert_receipts":           state.CloneSchemaOperational,
		"issue_slack_action_messages":              state.CloneSchemaOperational,
		"issue_slack_identities":                   state.CloneSchemaIdentity,
		"issue_slack_installations":                state.CloneSchemaConfiguration,
		"issue_slack_interaction_policies":         state.CloneSchemaConfiguration,
		"issue_slack_link_challenges":              state.CloneSchemaOperational,
		"issue_slack_oauth_flows":                  state.CloneSchemaOperational,
		"issue_slack_routing_policies":             state.CloneSchemaConfiguration,
		"issue_team_slack_deliveries":              state.CloneSchemaOperational,
		"issue_team_slack_destinations":            state.CloneSchemaConfiguration,
		"issue_team_slack_threads":                 state.CloneSchemaOperational,
		"issue_tracker_auto_sync":                  state.CloneSchemaConfiguration,
		"issue_tracker_callback_diagnostics":       state.CloneSchemaOperational,
		"issue_tracker_callbacks":                  state.CloneSchemaOperational,
		"issue_tracker_inbound_guards":             state.CloneSchemaOperational,
		"issue_tracker_linear_connections":         state.CloneSchemaConfiguration,
		"issue_tracker_linear_oauth_flows":         state.CloneSchemaOperational,
		"issue_tracker_source_guards":              state.CloneSchemaOperational,
		"issue_tracker_sync_escalation_policies":   state.CloneSchemaConfiguration,
		"issue_tracker_sync_history":               state.CloneSchemaOperational,
		"issue_tracker_sync_incidents":             state.CloneSchemaOperational,
		"issue_tracker_sync_notification_policies": state.CloneSchemaConfiguration,
		"issue_tracker_sync_notifications":         state.CloneSchemaOperational,
		"issue_tracker_sync_reminder_policies":     state.CloneSchemaConfiguration,
		"issue_tracker_sync_triage":                state.CloneSchemaOperational,
		"issue_tracker_sync_triage_activity":       state.CloneSchemaOperational,
		"native_issue_ticket_receipts":             state.CloneSchemaOperational,
		"workflow_initial_recovery_reviews":        state.CloneSchemaOperational,
		"profile_deployment_policies":              state.CloneSchemaConfiguration,
		"profile_investigations":                   state.CloneSchemaOperational,
		"profile_deployment_checks":                state.CloneSchemaOperational,
		"profile_route_alert_state":                state.CloneSchemaOperational,
		"profile_canary_checks":                    state.CloneSchemaOperational,
		"profile_periodic_monitors":                state.CloneSchemaOperational,
		"app_binding_release_policies":             state.CloneSchemaConfiguration,
		"app_binding_release_policy_history":       state.CloneSchemaConfiguration,
		"alert_rollback_actions":                   state.CloneSchemaOperational,
		"alert_historical_rollback_claims":         state.CloneSchemaOperational,
		"deployment_recovery_lineage":              state.CloneSchemaOperational,
		"deployment_rollback_operations":           state.CloneSchemaOperational,
		"deployment_dependency_gates":              state.CloneSchemaOperational,
		"app_environment_secret_refs":              state.CloneSchemaConfiguration,
		"app_environment_secret_ref_suppressions":  state.CloneSchemaConfiguration,
		"app_environment_workload_intents":         state.CloneSchemaConfiguration,
		"environment_git_sources":                  state.CloneSchemaConfiguration,
		"environment_external_field_owners":        state.CloneSchemaConfiguration,
		"environment_desired_revisions":            state.CloneSchemaConfiguration,
		"environment_managed_fields":               state.CloneSchemaConfiguration,
		"environment_management_overrides":         state.CloneSchemaConfiguration,
		"environment_workload_graphs":              state.CloneSchemaConfiguration,
		"deployment_route_policy_snapshots":        state.CloneSchemaConfiguration,
		"instance_runtime_config_receipts":         state.CloneSchemaOperational,
		"snapshot_runtime_config_receipts":         state.CloneSchemaOperational,
		"financial_budget_policies":                state.CloneSchemaConfiguration,
		"financial_budget_revisions":               state.CloneSchemaConfiguration,
		"financial_usage_evidence":                 state.CloneSchemaIdentity,
		"financial_price_snapshots":                state.CloneSchemaIdentity,
		"financial_evidence_coverage":              state.CloneSchemaOperational,
		"financial_sampling_windows":               state.CloneSchemaOperational,
		"notification_channels":                    state.CloneSchemaConfiguration,
		"alert_rule_channels":                      state.CloneSchemaConfiguration,
		"app_slos":                                 state.CloneSchemaConfiguration,
		"app_slo_hourly":                           state.CloneSchemaOperational,
		"synthetic_checks":                         state.CloneSchemaConfiguration,
		"synthetic_check_runs":                     state.CloneSchemaOperational,

		// Capacity and runtime observations are source-bound, expiring evidence.
		"managed_postgres_connection_capacity":     state.CloneSchemaOperational,
		"data_api_runtime_connection_observations": state.CloneSchemaOperational,

		"object_bucket_encryption":                           state.CloneSchemaConfiguration,
		"object_bucket_lifecycle":                            state.CloneSchemaConfiguration,
		"object_bucket_notifications":                        state.CloneSchemaConfiguration,
		"object_bucket_object_lock":                          state.CloneSchemaConfiguration,
		"object_version_protection":                          state.CloneSchemaConfiguration,
		"managed_postgres_usage_imports":                     state.CloneSchemaIdentity,
		"managed_postgres_creation_receipts":                 state.CloneSchemaIdentity,
		"managed_postgres_restore_verifications":             state.CloneSchemaOperational,
		"managed_postgres_accounting_reconciliations":        state.CloneSchemaIdentity,
		"managed_postgres_resizes":                           state.CloneSchemaOperational,
		"managed_postgres_cutover_drain_plans":               state.CloneSchemaOperational,
		"managed_postgres_cutover_drain_plan_members":        state.CloneSchemaOperational,
		"managed_postgres_cutover_drains":                    state.CloneSchemaOperational,
		"object_bucket_versioning":                           state.CloneSchemaConfiguration,
		"object_s3_copy_source_grants":                       state.CloneSchemaConfiguration,
		"object_s3_copy_source_epochs":                       state.CloneSchemaConfiguration,
		"object_version_references":                          state.CloneSchemaData,
		"object_deletions":                                   state.CloneSchemaOperational,
		"object_node_settlement_outbox":                      state.CloneSchemaOperational, // ADR-1562: source delivery authority.
		"object_lifecycle_scans":                             state.CloneSchemaOperational,
		"object_storage_capacity_reconciliations":            state.CloneSchemaOperational,
		"object_storage_multipart_part_grants":               state.CloneSchemaOperational,
		"object_storage_version_inventory_cursors":           state.CloneSchemaOperational,
		"object_storage_version_inventory_entries":           state.CloneSchemaOperational,
		"object_storage_write_admissions":                    state.CloneSchemaOperational,
		"project_environment_clone_object_grant_revocations": state.CloneSchemaOperational,

		"customer_operation_code_pins":              state.CloneSchemaOperational,
		"entity_outbox_acceptances":                 state.CloneSchemaOperational,
		"event_delivery_capacity":                   state.CloneSchemaOperational,
		"event_delivery_slots":                      state.CloneSchemaOperational,
		"event_fanout_history_summaries":            state.CloneSchemaOperational,
		"event_fanout_recipients":                   state.CloneSchemaOperational,
		"event_replay_job_items":                    state.CloneSchemaOperational,
		"event_replay_jobs":                         state.CloneSchemaOperational,
		"event_routing_backlog":                     state.CloneSchemaOperational,
		"event_routing_fairness":                    state.CloneSchemaOperational,
		"event_storage_admission":                   state.CloneSchemaOperational,
		"invocation_attempt_history":                state.CloneSchemaOperational,
		"invocation_keyed_replays":                  state.CloneSchemaOperational,
		"invocation_plain_replays":                  state.CloneSchemaOperational,
		"app_service_address_cursors":               state.CloneSchemaOperational,
		"workflow_automation_definitions":           state.CloneSchemaConfiguration,
		"workflow_automation_revisions":             state.CloneSchemaConfiguration,
		"workflow_code_pins":                        state.CloneSchemaOperational,
		"workflow_dispatch_cursors":                 state.CloneSchemaOperational,
		"workflow_event_code_refs":                  state.CloneSchemaOperational,
		"workflow_event_receipts":                   state.CloneSchemaOperational,
		"workflow_operation_effects":                state.CloneSchemaOperational,
		"workflow_run_resumes":                      state.CloneSchemaOperational,
		"platform_tenant_workflow_schedule_cursors": state.CloneSchemaOperational,
		"workflow_schedule_cursors":                 state.CloneSchemaOperational,
		"workflow_schedule_occurrences":             state.CloneSchemaOperational,
		"workflow_webhook_bindings":                 state.CloneSchemaConfiguration,
		"workflow_webhook_receipts":                 state.CloneSchemaOperational,
		"customer_operation_definitions":            state.CloneSchemaConfiguration,
		"customer_operations":                       state.CloneSchemaOperational,
		"customer_operation_milestones":             state.CloneSchemaOperational,
		"customer_operation_events":                 state.CloneSchemaOperational,
		"customer_operation_delivery_retries":       state.CloneSchemaOperational,
		"customer_operation_attempt_instances":      state.CloneSchemaOperational,
		"customer_operation_webhook_bindings":       state.CloneSchemaConfiguration,
		"customer_operation_webhook_receipts":       state.CloneSchemaOperational,
		"customer_operation_executions":             state.CloneSchemaOperational,
		"customer_operation_idempotency":            state.CloneSchemaOperational,
		"customer_operation_job_executions":         state.CloneSchemaOperational,
		"customer_operation_recoveries":             state.CloneSchemaOperational,
		"customer_operation_result_blobs":           state.CloneSchemaOperational,
		"customer_operation_reports":                state.CloneSchemaOperational,
		"customer_operation_stream_leases":          state.CloneSchemaOperational,
		"customer_operation_workflow_executions":    state.CloneSchemaOperational,
		"customer_operation_workflow_guest_claims":  state.CloneSchemaOperational,
		"customer_operation_workflow_state_reports": state.CloneSchemaOperational,
		"customer_operation_workflow_states":        state.CloneSchemaOperational,
		"queue_bindings":                            state.CloneSchemaConfiguration, "crons": state.CloneSchemaConfiguration, "jobs": state.CloneSchemaConfiguration,
		"app_webhooks": state.CloneSchemaConfiguration, "outbound_integration_credentials": state.CloneSchemaConfiguration,
		"app_work_policies": state.CloneSchemaConfiguration, "event_subscription_work_bindings": state.CloneSchemaConfiguration,
		"trigger_work_bindings": state.CloneSchemaConfiguration, "app_private_network_attachments": state.CloneSchemaConfiguration,
		"runtime_config_entries":            state.CloneSchemaConfiguration,
		"managed_realtime_channel_messages": state.CloneSchemaData, "managed_realtime_channel_heads": state.CloneSchemaData,
		"instances": state.CloneSchemaOperational, "invocations": state.CloneSchemaOperational, "app_tasks": state.CloneSchemaOperational,
		"invocation_environment_queue_admissions":    state.CloneSchemaOperational,
		"invocation_environment_queue_receipts":      state.CloneSchemaOperational,
		"deployment_runtime_environment_owners":      state.CloneSchemaOperational,
		"app_health_collection_state":                state.CloneSchemaOperational,
		"app_health_history":                         state.CloneSchemaOperational,
		"runtime_releases":                           state.CloneSchemaPlatform,
		"runtime_release_qualifications":             state.CloneSchemaPlatform,
		"runtime_artifact_bindings":                  state.CloneSchemaOperational,
		"deployment_runtime_upgrade_targets":         state.CloneSchemaOperational,
		"deployment_runtime_upgrade_baselines":       state.CloneSchemaOperational,
		"deployment_runtime_upgrade_acceptances":     state.CloneSchemaOperational,
		"deployment_runtime_upgrade_cutovers":        state.CloneSchemaOperational,
		"runtime_upgrade_operations":                 state.CloneSchemaOperational,
		"runtime_upgrade_verifications":              state.CloneSchemaOperational,
		"runtime_upgrade_gateway_rosters":            state.CloneSchemaPlatform,
		"runtime_upgrade_gateway_roster_head":        state.CloneSchemaPlatform,
		"runtime_upgrade_gateway_heartbeats":         state.CloneSchemaOperational,
		"runtime_upgrade_external_fence_authorities": state.CloneSchemaPlatform,
		"runtime_upgrade_external_fence_intents":     state.CloneSchemaPlatform,
		"runtime_upgrade_external_fence_receipts":    state.CloneSchemaOperational,
		"runtime_upgrade_native_public_startups":     state.CloneSchemaOperational,
		"invocation_work_environment_domains":        state.CloneSchemaOperational, "invocation_work_environment_admissions": state.CloneSchemaOperational,
		"project_environment_queue_runtime_sets": state.CloneSchemaOperational, "project_environment_queue_consumers": state.CloneSchemaOperational,
		"feature_flag_versions":           state.CloneSchemaConfiguration,
		"exclusive_work_policies":         state.CloneSchemaConfiguration,
		"exclusive_work_trigger_bindings": state.CloneSchemaConfiguration,
		"app_issue_impact_alert_policies": state.CloneSchemaConfiguration,
		"issue_ingest_tokens":             state.CloneSchemaConfiguration,
		"app_udp_listeners":               state.CloneSchemaConfiguration,
		"exclusive_work_operations":       state.CloneSchemaOperational,
		"exclusive_work_keys":             state.CloneSchemaOperational,
		"schedule_occurrences":            state.CloneSchemaOperational,
		"dev_bridge_sessions":             state.CloneSchemaOperational,
		"execution_artifact_grants":       state.CloneSchemaOperational,
		"execution_outbound_integrations": state.CloneSchemaOperational,
		"mcp_catalog_monitors":            state.CloneSchemaOperational,
		"mcp_oauth_retirements":           state.CloneSchemaOperational,
		"service_recovery":                state.CloneSchemaOperational,
		// ADR-1522–1530: recovery fences and receipts remain with their source.
		"instance_snapshot_recovery":    state.CloneSchemaOperational,
		"migration_admissions":          state.CloneSchemaOperational,
		"migration_sources":             state.CloneSchemaOperational,
		"node_lifecycle_completions":    state.CloneSchemaOperational,
		"operator_node_executions":      state.CloneSchemaOperational,
		"operator_park_executions":      state.CloneSchemaOperational,
		"operator_recovery_submissions": state.CloneSchemaOperational,
		"operator_restart_executions":   state.CloneSchemaOperational,
		"service_capacity_policy":       state.CloneSchemaPlatform,
		"accounts":                      state.CloneSchemaIdentity, "orgs": state.CloneSchemaIdentity, "api_keys": state.CloneSchemaIdentity,
		"egress_policy": state.CloneSchemaPlatform, "compute_nodes": state.CloneSchemaPlatform,

		"project_release_sets": state.CloneSchemaConfiguration,

		// Evidence and expiring execution plans belong to the source environment.
		"binding_verification_runs":         state.CloneSchemaOperational,
		"binding_verification_run_steps":    state.CloneSchemaOperational,
		"project_binding_verification_runs": state.CloneSchemaOperational,
		"project_release_stages":            state.CloneSchemaOperational,
		"queue_binding_canaries":            state.CloneSchemaOperational,
		// Pipeline, readiness, lease, and rehearsal state remains explicitly source-owned.
		"data_connection_checks":           state.CloneSchemaOperational,
		"data_pipeline_alert_deliveries":   state.CloneSchemaOperational,
		"data_pipeline_alerts":             state.CloneSchemaConfiguration,
		"data_pipeline_connections":        state.CloneSchemaIdentity,
		"data_pipeline_schema_refreshes":   state.CloneSchemaOperational,
		"data_pipeline_setups":             state.CloneSchemaOperational,
		"data_pipelines":                   state.CloneSchemaConfiguration,
		"managed_postgres_live_keys":       state.CloneSchemaOperational,
		"outbound_ai_access_tokens":        state.CloneSchemaIdentity,
		"outbound_ai_batches":              state.CloneSchemaOperational,
		"outbound_ai_evaluation_items":     state.CloneSchemaOperational,
		"outbound_ai_evaluations":          state.CloneSchemaOperational,
		"outbound_ai_log_exports":          state.CloneSchemaOperational,
		"outbound_ai_route_canaries":       state.CloneSchemaOperational,
		"outbound_ai_route_target_minutes": state.CloneSchemaOperational,
		"outbound_ai_tag_limits":           state.CloneSchemaConfiguration,
		"outbound_ai_tag_usage_daily":      state.CloneSchemaOperational,
		"node_leases":                      state.CloneSchemaOperational,
		"postgres_capacity_reservations":   state.CloneSchemaOperational,
		"postgres_capacity_resources":      state.CloneSchemaOperational,
		"postgres_capacity_sources":        state.CloneSchemaOperational,
		"postgres_capacity_targets":        state.CloneSchemaOperational,
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
	report, err := state.CloneSchemaCoverage(policies)
	if err != nil || !report.Known || len(report.Hash) != 64 || len(report.Blockers) == 0 {
		t.Fatalf("registered does not mean fully copyable: %+v %v", report, err)
	}
	policies[0].Kind, policies[0].Columns[0] = "changed", "changed"
	fresh, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil || fresh[0].Kind == "changed" || fresh[0].Columns[0] == "changed" {
		t.Fatalf("caller changed capture registry: %v", err)
	}
}

func TestHostingCloneInventoryRetainsCopyStrategyBlockers(t *testing.T) {
	// adr: 202610171453 — known schema does not qualify hosting configuration or artifact cloning.
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.CloneSchemaCoverage(policies)
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

func TestMCPReadinessCloneInventoryRetainsCopyStrategyBlockers(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.CloneSchemaCoverage(policies)
	if err != nil || !report.Known {
		t.Fatal("registered schema must be known", report, err)
	}
	wanted := map[string]bool{
		"app_mcp_readiness_policies":               true,
		"app_mcp_readiness_policy_history":         true,
		"mcp_readiness_fallback_policies":          true,
		"mcp_readiness_maintenance_windows":        true,
		"mcp_readiness_pagerduty_acknowledgements": true,
		"mcp_readiness_pagerduty_policies":         true,
		"mcp_readiness_reminder_defaults":          true,
	}
	for _, blocker := range report.Blockers {
		if blocker.Code == "isolated_strategy_unavailable" {
			delete(wanted, blocker.Table)
		}
	}
	if len(wanted) != 0 {
		t.Fatal("MCP readiness configuration became implicitly cloneable", wanted)
	}
}

func TestCloneSchemaCoverageRejectsNewAndMissingTablesAndColumns(t *testing.T) {
	for _, fault := range []struct {
		name, table, column, code string
		change                    func([]state.ProjectEnvironmentCloneSchemaPolicy) []state.ProjectEnvironmentCloneSchemaPolicy
	}{
		{"new_table", "new_integration", "", "unregistered_table", func(p []state.ProjectEnvironmentCloneSchemaPolicy) []state.ProjectEnvironmentCloneSchemaPolicy {
			return append(p, state.ProjectEnvironmentCloneSchemaPolicy{TableName: "new_integration", Kind: state.CloneSchemaOperational, Columns: []string{"app_id", "config"}})
		}},
		{"new_column", "account_async_quota", "new_setting", "unregistered_column", func(p []state.ProjectEnvironmentCloneSchemaPolicy) []state.ProjectEnvironmentCloneSchemaPolicy {
			p[0].Columns = append(p[0].Columns, "new_setting")
			return p
		}},
		{"missing_column", "account_async_quota", "account_id", "missing_column", func(p []state.ProjectEnvironmentCloneSchemaPolicy) []state.ProjectEnvironmentCloneSchemaPolicy {
			p[0].Columns = p[0].Columns[1:]
			return p
		}},
		{"missing_table", "account_async_quota", "", "missing_table", func(p []state.ProjectEnvironmentCloneSchemaPolicy) []state.ProjectEnvironmentCloneSchemaPolicy {
			return p[1:]
		}},
	} {
		t.Run(fault.name, func(t *testing.T) {
			policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
			if err != nil {
				t.Fatal(err)
			}
			report, err := state.CloneSchemaCoverage(fault.change(policies))
			if err != nil || report.Known {
				t.Fatalf("unregistered schema was accepted: %+v %v", report, err)
			}
			var coverageError *state.ProjectEnvironmentCloneSchemaCoverageError
			if !errors.As(state.RequireKnownCloneSchema(report), &coverageError) || !errors.Is(coverageError, state.ErrConflict) {
				t.Fatal("capture did not return a named coverage error")
			}
			want := state.ProjectEnvironmentCloneCoverageBlocker{Table: fault.table, Column: fault.column, Code: fault.code}
			if !reflect.DeepEqual(coverageError.Blockers, []state.ProjectEnvironmentCloneCoverageBlocker{want}) {
				t.Fatalf("schema blockers = %+v, want %+v", coverageError.Blockers, want)
			}
		})
	}
}

func TestCloneSchemaCoverageCanonicalOrderAndNoInputMutation(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := state.CloneSchemaCoverage(policies)
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
	reordered, err := state.CloneSchemaCoverage(policies)
	if err != nil || !reflect.DeepEqual(reordered, canonical) || !reflect.DeepEqual(policies[0].Columns, firstColumns) {
		t.Fatalf("coverage changed with input order/classification or mutated input: %v", err)
	}
	duplicate := append(policies, policies[0])
	if _, err := state.CloneSchemaCoverage(duplicate); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate table accepted: %v", err)
	}
}
