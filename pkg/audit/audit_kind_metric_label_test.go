// pkg/audit/audit_kind_metric_label_test.go — PR-#TBD / fix-cluster D
// pins for auditKindMetricLabel. The function is unexported so this
// file is `package audit` (internal) rather than `package audit_test`
// like the rest of the suite.
//
// Six tests pin the closed-set mapping end-to-end:
//
//  1. verb-oriented request shapes (force_<verb> → force_<verb>)
//  2. verb-oriented outcome shapes (force_<verb>.outcome →
//     force_<verb>.outcome)
//  3. apid request-side instance-oriented aliases
//     (park_instance / restart_instance → force_park /
//     force_restart)
//  4. apid request-side instance-oriented outcome aliases
//     (park_instance.outcome / restart_instance.outcome →
//     force_park.outcome / force_restart.outcome)
//  5. force_cold_boot regression pin (this was the only kind
//     that worked pre-fix and must continue to work)
//  6. unknown kind collapses to "other" (cardinality bound)
//
// The shape is table-driven where it can be, but kept as one
// top-level test per mapping branch because each pin is a
// regression guard for a specific decision in the audit emit
// surface. If a future reviewer touches auditKindMetricLabel
// the test diff surfaces the intent.
package audit

import "testing"

// TestAuditKindMetricLabel_OperatorActionForcePark pins the
// verb-oriented request shape — schedd writes
// "operator.action.force_park" on the request row for a
// force-park intent.
func TestAuditKindMetricLabel_OperatorActionForcePark(t *testing.T) {
	got := auditKindMetricLabel("operator.action.force_park")
	if got != "force_park" {
		t.Errorf("auditKindMetricLabel(operator.action.force_park) = %q, want %q", got, "force_park")
	}
}

// TestAuditKindMetricLabel_OperatorActionForceParkOutcome pins
// the verb-oriented outcome shape — schedd writes
// "operator.action.force_park.outcome" on the terminal outcome
// audit row for a force-park intent.
func TestAuditKindMetricLabel_OperatorActionForceParkOutcome(t *testing.T) {
	got := auditKindMetricLabel("operator.action.force_park.outcome")
	if got != "force_park.outcome" {
		t.Errorf("auditKindMetricLabel(operator.action.force_park.outcome) = %q, want %q", got, "force_park.outcome")
	}
}

// TestAuditKindMetricLabel_OperatorActionParkInstance pins the
// apid request-side instance-oriented alias — apid writes
// "operator.action.park_instance" on the request-row audit
// emission in postForcePark. Pre-fix this collapsed to "other"
// (the alias wasn't in the switch) so the auditLogWriteTotal
// counter was incrementing the "other" label series instead
// of "force_park" — /obs/health's join with the schedd-side
// verb-oriented outcome series silently broke.
func TestAuditKindMetricLabel_OperatorActionParkInstance(t *testing.T) {
	got := auditKindMetricLabel("operator.action.park_instance")
	if got != "force_park" {
		t.Errorf("auditKindMetricLabel(operator.action.park_instance) = %q, want %q (alias to force_park)", got, "force_park")
	}
}

// TestAuditKindMetricLabel_OperatorActionParkInstanceOutcome pins
// the apid request-side outcome alias (currently unused — schedd
// owns the outcome emission — but the alias MUST exist so a
// future apid emit of an outcome row doesn't silently land on
// "other").
func TestAuditKindMetricLabel_OperatorActionParkInstanceOutcome(t *testing.T) {
	got := auditKindMetricLabel("operator.action.park_instance.outcome")
	if got != "force_park.outcome" {
		t.Errorf("auditKindMetricLabel(operator.action.park_instance.outcome) = %q, want %q (alias to force_park.outcome)", got, "force_park.outcome")
	}
}

// TestAuditKindMetricLabel_OperatorActionRestartInstance pins
// the apid restart-side alias — mirrors the park_instance pin
// for postForceRestart.
func TestAuditKindMetricLabel_OperatorActionRestartInstance(t *testing.T) {
	got := auditKindMetricLabel("operator.action.restart_instance")
	if got != "force_restart" {
		t.Errorf("auditKindMetricLabel(operator.action.restart_instance) = %q, want %q (alias to force_restart)", got, "force_restart")
	}
}

// TestAuditKindMetricLabel_OperatorActionRestartInstanceOutcome
// pins the apid restart-side outcome alias.
func TestAuditKindMetricLabel_OperatorActionRestartInstanceOutcome(t *testing.T) {
	got := auditKindMetricLabel("operator.action.restart_instance.outcome")
	if got != "force_restart.outcome" {
		t.Errorf("auditKindMetricLabel(operator.action.restart_instance.outcome) = %q, want %q (alias to force_restart.outcome)", got, "force_restart.outcome")
	}
}

// TestAuditKindMetricLabel_OperatorActionForceColdBoot is the
// regression pin for the only kind that worked before the
// fix-cluster. force_cold_boot has no instance-oriented alias
// (the apid handler emits per-deployment, not per-instance, so
// the kind stays verb-oriented end-to-end). This test guards
// against a future reviewer "tidying up" the switch and
// accidentally dropping the verb-only case.
func TestAuditKindMetricLabel_OperatorActionForceColdBoot(t *testing.T) {
	got := auditKindMetricLabel("operator.action.force_cold_boot")
	if got != "force_cold_boot" {
		t.Errorf("auditKindMetricLabel(operator.action.force_cold_boot) = %q, want %q", got, "force_cold_boot")
	}
}

func TestAuditKindMetricLabel_NodeLifecycleIntents(t *testing.T) {
	for _, tt := range []struct {
		kind string
		want string
	}{
		{"operator.action.node_drain", "node_drain"},
		{"operator.action.node_force_drain", "node_force_drain"},
		{"operator.action.node_activate", "node_activate"},
		{"operator.action.node_drain.outcome", "node_drain.outcome"},
		{"operator.action.node_force_drain.outcome", "node_force_drain.outcome"},
		{"operator.action.node_activate.outcome", "node_activate.outcome"},
	} {
		if got := auditKindMetricLabel(tt.kind); got != tt.want {
			t.Errorf("auditKindMetricLabel(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// TestAuditKindMetricLabel_UnknownKindCollapsesToOther pins the
// cardinality bound. A free-text kind that doesn't match any
// case in the switch MUST collapse to "other" so the audit
// counter series stays bounded regardless of caller typos or
// future audit kinds that haven't been added to the closed set
// yet.
func TestAuditKindMetricLabel_UnknownKindCollapsesToOther(t *testing.T) {
	for _, kind := range []string{
		"",                                  // empty
		"unrelated",                         // no prefix
		"operator.action.unknown_verb",      // wrong verb
		"operator.action.park_instance.bad", // wrong suffix
		"operator.action.",                  // prefix only
	} {
		got := auditKindMetricLabel(kind)
		if got != "other" {
			t.Errorf("auditKindMetricLabel(%q) = %q, want %q (cardinality bound)", kind, got, "other")
		}
	}
}

// TestAuditKindMetricLabel_DeploymentQueueControls pins the
// ADR-124 queue-control audit kinds onto the closed metric
// label set. Without these aliases the four new kinds
// (deployment.cancelled / .reordered / .cleared /
// .clear_obsolete) would land on the "other" series and break
// /v1/admin/obs/health's per-endpoint join with the audit
// events table.
//
// The mapping mirrors the existing project.workload.<verb>
// shape — the verb suffix IS the metric label so dashboards
// don't need a separate mapping table.
func TestAuditKindMetricLabel_DeploymentQueueControls(t *testing.T) {
	for _, tt := range []struct {
		kind string
		want string
	}{
		{"deployment.cancelled", "deployment.cancelled"},
		{"deployment.reordered", "deployment.reordered"},
		{"deployment.cleared", "deployment.cleared"},
		{"deployment.clear_obsolete", "deployment.clear_obsolete"},
	} {
		got := auditKindMetricLabel(tt.kind)
		if got != tt.want {
			t.Errorf("auditKindMetricLabel(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
