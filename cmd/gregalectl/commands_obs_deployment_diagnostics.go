package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"io"
	"strings"
)

func applyObsDeploymentDiagnostics(out *obsDeploymentRuntimeBuilds, d *operatorobs.RuntimeDiagnostics, source string) {
	out.Diagnostics = operatorobs.CloneRuntimeDiagnostics(d)
	if d != nil {
		out.DiagnosticsSource = source
	}
}
func filterObsDeploymentDiagnostics(out *obsDeploymentRuntimeBuilds) {
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "builder_health") {
		out.BuilderHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "handoff_health") {
		out.HandoffHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "build_recovery_health") {
		out.BuildRecoveryHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "app_task_health") {
		out.AppTaskHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "command_cron_health") {
		out.CommandCronHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "command_cron_outcomes") {
		out.CommandCronOutcomes = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "http_cron_delivery") {
		out.HTTPCronDelivery = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "delayed_task_dispatch") {
		out.DelayedTaskDispatch = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "command_cron_timing") {
		out.CommandCronTiming = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "app_task_results") {
		out.AppTaskResults = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "app_task_operations") {
		out.AppTaskOperations = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "post_capture_health") {
		out.PostCaptureHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "snapshot_prime_health") {
		out.SnapshotPrimeHealth = nil
	}
	if !operatorobs.RuntimeDiagnosticSelected(out.Diagnostics, "preparation_health") {
		out.PreparationHealth = nil
	}
}
func (r *obsDeploymentBuildRecorder) skipRuntimeDiagnostic(current *obsDeploymentRuntimeBuilds, key string) bool {
	if operatorobs.RuntimeDiagnosticSelected(current.Diagnostics, key) {
		return false
	}
	if r.diagnosticNotSelected == nil {
		r.diagnosticNotSelected = map[string]int{}
	}
	r.diagnosticNotSelected[key]++
	return true
}
func (r *obsDeploymentBuildRecorder) diagnosticMissingBeforeFirst(key string) bool {
	return r.progress.Observations > 1 && r.diagnosticNotSelected[key] != r.progress.Observations-1
}
func writeObsDeploymentDiagnostics(w io.Writer, out *obsDeploymentRuntimeBuilds) {
	if out.Diagnostics == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "runtime_diagnostics source=%s selected=%s not_selected=%s\n", out.DiagnosticsSource, strings.Join(out.Diagnostics.Selected, ","), strings.Join(out.Diagnostics.NotSelected, ","))
}
