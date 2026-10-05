package state

// adr: 592. Standards configuration needs isolation; runtime authority resets.

import "testing"

func TestApplicationStandardCloneSchemaPreservesConfigurationAndAuthorityBoundaries(t *testing.T) {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	report, err := cloneSchemaCoverage(policies)
	if err != nil || !report.Known {
		t.Fatalf("standards schema is unknown: %v", err)
	}
	kinds, blocked := map[string]string{}, map[string]bool{}
	for _, p := range policies {
		kinds[p.TableName] = p.Kind
	}
	for _, b := range report.Blockers {
		blocked[b.Table] = true
	}
	for _, table := range []string{"app_application_standards", "application_standard_assignments", "application_standard_exceptions", "application_standard_control_bindings", "application_standard_control_backups", "application_standard_log_destinations", "application_standard_publishers", "application_standard_versions", "application_standards"} {
		if kinds[table] != CloneSchemaConfiguration || !blocked[table] {
			t.Fatalf("%s silently became copyable configuration", table)
		}
	}
	for _, table := range []string{"instance_application_standard_admissions", "instance_application_standard_boots", "instance_application_standard_promotions", "application_standard_snapshot_captures", "application_standard_native_incarnations", "application_standard_log_consumer_sessions", "deployment_registry_rootfs", "deployment_runtime_scans", "base_image_producers", "source_build_rootfs", "build_export_publications"} {
		if kinds[table] != CloneSchemaOperational {
			t.Fatalf("%s would transfer source runtime authority", table)
		}
	}
}
