// adr: 375
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
		"queue_bindings": CloneSchemaConfiguration, "crons": CloneSchemaConfiguration, "jobs": CloneSchemaConfiguration,
		"app_webhooks": CloneSchemaConfiguration, "outbound_integration_credentials": CloneSchemaConfiguration,
		"app_work_policies": CloneSchemaConfiguration, "event_subscription_work_bindings": CloneSchemaConfiguration,
		"trigger_work_bindings": CloneSchemaConfiguration, "app_private_network_attachments": CloneSchemaConfiguration,
		"runtime_config_entries":            CloneSchemaConfiguration,
		"managed_realtime_channel_messages": CloneSchemaData, "managed_realtime_channel_heads": CloneSchemaData,
		"instances": CloneSchemaOperational, "invocations": CloneSchemaOperational, "app_tasks": CloneSchemaOperational,
		"invocation_work_environment_domains": CloneSchemaOperational, "invocation_work_environment_admissions": CloneSchemaOperational,
		"project_environment_queue_runtime_sets": CloneSchemaOperational, "project_environment_queue_consumers": CloneSchemaOperational,
		"accounts": CloneSchemaIdentity, "orgs": CloneSchemaIdentity, "api_keys": CloneSchemaIdentity,
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
