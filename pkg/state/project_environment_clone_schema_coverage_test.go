// adr: 590
// adr: 623 — pending resize intents belong only to their original database.
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
		"app_binding_release_policies":            CloneSchemaConfiguration,
		"app_binding_release_policy_history":      CloneSchemaConfiguration,
		"alert_rollback_actions":                  CloneSchemaOperational,
		"alert_historical_rollback_claims":        CloneSchemaOperational,
		"deployment_recovery_lineage":             CloneSchemaOperational,
		"deployment_rollback_operations":          CloneSchemaOperational,
		"deployment_dependency_gates":             CloneSchemaOperational,
		"app_environment_secret_refs":             CloneSchemaConfiguration,
		"app_environment_secret_ref_suppressions": CloneSchemaConfiguration,
		"app_environment_workload_intents":        CloneSchemaConfiguration,
		"environment_git_sources":                 CloneSchemaConfiguration,
		"environment_external_field_owners":       CloneSchemaConfiguration,
		"environment_desired_revisions":           CloneSchemaConfiguration,
		"environment_managed_fields":              CloneSchemaConfiguration,
		"environment_management_overrides":        CloneSchemaConfiguration,
		"environment_workload_graphs":             CloneSchemaConfiguration,
		"deployment_route_policy_snapshots":       CloneSchemaConfiguration,
		"instance_runtime_config_receipts":        CloneSchemaOperational,
		"snapshot_runtime_config_receipts":        CloneSchemaOperational,
		"financial_budget_policies":               CloneSchemaConfiguration,
		"financial_budget_revisions":              CloneSchemaConfiguration,
		"financial_usage_evidence":                CloneSchemaIdentity,
		"financial_price_snapshots":               CloneSchemaIdentity,
		"financial_evidence_coverage":             CloneSchemaOperational,
		"financial_sampling_windows":              CloneSchemaOperational,

		"object_bucket_encryption":                           CloneSchemaConfiguration,
		"object_bucket_lifecycle":                            CloneSchemaConfiguration,
		"object_bucket_notifications":                        CloneSchemaConfiguration,
		"object_bucket_object_lock":                          CloneSchemaConfiguration,
		"object_version_protection":                          CloneSchemaConfiguration,
		"managed_postgres_usage_imports":                     CloneSchemaIdentity,
		"managed_postgres_creation_receipts":                 CloneSchemaIdentity,
		"managed_postgres_accounting_reconciliations":        CloneSchemaIdentity,
		"managed_postgres_resizes":                           CloneSchemaOperational,
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
		"workflow_event_receipts":                   CloneSchemaOperational,
		"workflow_operation_effects":                CloneSchemaOperational,
		"workflow_run_resumes":                      CloneSchemaOperational,
		"platform_tenant_workflow_schedule_cursors": CloneSchemaOperational,
		"workflow_schedule_cursors":                 CloneSchemaOperational,
		"workflow_webhook_bindings":                 CloneSchemaConfiguration,
		"workflow_webhook_receipts":                 CloneSchemaOperational,
		"customer_operation_definitions":            CloneSchemaConfiguration,
		"customer_operations":                       CloneSchemaOperational,
		"customer_operation_events":                 CloneSchemaOperational,
		"customer_operation_delivery_retries":       CloneSchemaOperational,
		"customer_operation_executions":             CloneSchemaOperational,
		"customer_operation_idempotency":            CloneSchemaOperational,
		"customer_operation_recoveries":             CloneSchemaOperational,
		"customer_operation_result_blobs":           CloneSchemaOperational,
		"customer_operation_reports":                CloneSchemaOperational,
		"customer_operation_stream_leases":          CloneSchemaOperational,
		"queue_bindings":                            CloneSchemaConfiguration, "crons": CloneSchemaConfiguration, "jobs": CloneSchemaConfiguration,
		"app_webhooks": CloneSchemaConfiguration, "outbound_integration_credentials": CloneSchemaConfiguration,
		"app_work_policies": CloneSchemaConfiguration, "event_subscription_work_bindings": CloneSchemaConfiguration,
		"trigger_work_bindings": CloneSchemaConfiguration, "app_private_network_attachments": CloneSchemaConfiguration,
		"runtime_config_entries":            CloneSchemaConfiguration,
		"managed_realtime_channel_messages": CloneSchemaData, "managed_realtime_channel_heads": CloneSchemaData,
		"instances": CloneSchemaOperational, "invocations": CloneSchemaOperational, "app_tasks": CloneSchemaOperational,
		"invocation_environment_queue_admissions": CloneSchemaOperational,
		"invocation_environment_queue_receipts":   CloneSchemaOperational,
		"deployment_runtime_environment_owners":   CloneSchemaOperational,
		"invocation_work_environment_domains":     CloneSchemaOperational, "invocation_work_environment_admissions": CloneSchemaOperational,
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
		"service_recovery":                CloneSchemaOperational,
		"service_capacity_policy":         CloneSchemaPlatform,
		"accounts":                        CloneSchemaIdentity, "orgs": CloneSchemaIdentity, "api_keys": CloneSchemaIdentity,
		"egress_policy": CloneSchemaPlatform, "compute_nodes": CloneSchemaPlatform,
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
