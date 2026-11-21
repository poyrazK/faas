package operatorobs

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

const RuntimeDiagnosticsScope = "selected_runtime_diagnostics"

// RuntimeDiagnostics records explicit intent, not collection availability.
// An absent descriptor preserves the existing all-diagnostics behavior.
// Identity, readiness, process and loop evidence are always collected.
type RuntimeDiagnostics struct {
	Scope       string   `json:"scope"`
	Selected    []string `json:"selected"`
	NotSelected []string `json:"not_selected"`
}

func RuntimeDiagnosticGroups(role string) []string {
	if !RuntimeBuildRoleValid(role) {
		return nil
	}
	if strings.HasSuffix(role, "-v1") {
		return []string{}
	}
	return []string{"builder_health", "handoff_health", "build_recovery_health", "app_task_health", "command_cron_health", "command_cron_outcomes", "http_cron_delivery", "delayed_task_dispatch", "command_cron_timing", "app_task_results", "app_task_operations", "post_capture_health", "snapshot_prime_health", "preparation_health"}
}

func ParseRuntimeDiagnostics(role, raw string) (*RuntimeDiagnostics, error) {
	names := RuntimeDiagnosticGroups(role)
	if !RuntimeBuildRoleValid(role) || raw == "" || raw != "all" && raw != "none" && len(raw) > len(strings.Join(names, ",")) {
		return nil, state.ErrInvalidArgument
	}
	selected := []string{}
	switch raw {
	case "all":
		selected = names
	case "none":
	default:
		for _, name := range strings.Split(raw, ",") {
			if !slices.Contains(names, name) || slices.Contains(selected, name) {
				return nil, state.ErrInvalidArgument
			}
			selected = append(selected, name)
		}
	}
	out := &RuntimeDiagnostics{Scope: RuntimeDiagnosticsScope, Selected: []string{}, NotSelected: []string{}}
	for _, name := range names {
		if slices.Contains(selected, name) {
			out.Selected = append(out.Selected, name)
		} else {
			out.NotSelected = append(out.NotSelected, name)
		}
	}
	return out, nil
}
func RuntimeDiagnosticsValid(role string, d *RuntimeDiagnostics) bool {
	if d == nil || d.Scope != RuntimeDiagnosticsScope || d.Selected == nil || d.NotSelected == nil {
		return false
	}
	names := RuntimeDiagnosticGroups(role)
	if !RuntimeBuildRoleValid(role) || len(d.Selected)+len(d.NotSelected) != len(names) {
		return false
	}
	selected, omitted := []string{}, []string{}
	for _, name := range names {
		if slices.Contains(d.Selected, name) {
			selected = append(selected, name)
		} else {
			omitted = append(omitted, name)
		}
	}
	return slices.Equal(d.Selected, selected) && slices.Equal(d.NotSelected, omitted)
}
func RuntimeDiagnosticSelected(d *RuntimeDiagnostics, name string) bool {
	return d == nil || slices.Contains(d.Selected, name)
}
func CloneRuntimeDiagnostics(d *RuntimeDiagnostics) *RuntimeDiagnostics {
	if d == nil {
		return nil
	}
	out := *d
	out.Selected = append([]string{}, d.Selected...)
	out.NotSelected = append([]string{}, d.NotSelected...)
	return &out
}
func RuntimeDiagnosticsEqual(a, b *RuntimeDiagnostics) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Scope == b.Scope && slices.Equal(a.Selected, b.Selected) && slices.Equal(a.NotSelected, b.NotSelected)
}
func (d *RuntimeDiagnostics) QueryValue() string {
	if len(d.Selected) == 0 {
		return "none"
	}
	if len(d.NotSelected) == 0 {
		return "all"
	}
	return strings.Join(d.Selected, ",")
}
func (d *RuntimeDiagnostics) UnmarshalJSON(body []byte) error {
	type wire RuntimeDiagnostics
	var value wire
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	if _, ok := runtimeRequiredJSONFields(body, "scope", "selected", "not_selected"); !ok {
		return errors.New("incomplete diagnostic selection")
	}
	*d = RuntimeDiagnostics(value)
	return nil
}
func (s RuntimeBuildSelection) diagnosticSelected(name string) bool {
	return RuntimeDiagnosticSelected(s.diagnostics, name)
}
