package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// EnvironmentWorkloadIntent is scoped customer intent. Recording it does not
// qualify a deployment or prove that its serving graph uses these settings.
type EnvironmentWorkloadIntent struct {
	AccountID       string                                     `json:"account_id"`
	AppID           string                                     `json:"app_id"`
	EnvironmentID   string                                     `json:"environment_id"`
	JobID           string                                     `json:"job_id,omitempty"`
	Source          *api.EnvironmentWorkloadSource             `json:"source"`
	SourceRevision  string                                     `json:"source_revision,omitempty"`
	Schedule        *api.EnvironmentJobSchedule                `json:"schedule,omitempty"`
	Variables       map[string]string                          `json:"variables"`
	Runtime         map[string]json.RawMessage                 `json:"runtime"`
	ServiceBindings map[string]EnvironmentScopedServiceBinding `json:"service_bindings"`
	CreatedAt       time.Time                                  `json:"created_at"`
	UpdatedAt       time.Time                                  `json:"updated_at"`
}

type EnvironmentWorkloadIntentStore interface {
	EnvironmentWorkloadIntent(context.Context, string, string, string) (EnvironmentWorkloadIntent, error)
	EnvironmentWorkloadIntentByJob(context.Context, string, string) (EnvironmentWorkloadIntent, error)
	PutEnvironmentWorkloadIntent(context.Context, EnvironmentWorkloadIntent) (EnvironmentWorkloadIntent, error)
}

type environmentWorkloadIntentKey struct{ AppID, EnvironmentID string }

type gitOpsSourceBaseline struct {
	ID         string                              `json:"id"`
	Kind       DeploymentKind                      `json:"kind"`
	Image      string                              `json:"image"`
	SourceURL  string                              `json:"source_url"`
	CommitSHA  string                              `json:"commit_sha"`
	SourceRoot string                              `json:"source_root"`
	Inputs     EnvironmentWorkloadDeploymentInputs `json:"inputs"`
	Managed    bool                                `json:"managed,omitempty"`
}

func cloneWorkloadIntent(row EnvironmentWorkloadIntent) EnvironmentWorkloadIntent {
	row.Variables = cloneStringMap(row.Variables)
	bindings := map[string]EnvironmentScopedServiceBinding{}
	for key, value := range row.ServiceBindings {
		bindings[key] = value
	}
	row.ServiceBindings = bindings
	if row.Source != nil {
		source := *row.Source
		row.Source = &source
	}
	if row.Schedule != nil {
		schedule := *row.Schedule
		if schedule.SchedulePolicy != nil {
			policy := *schedule.SchedulePolicy
			schedule.SchedulePolicy = &policy
		}
		if schedule.FailureRules != nil {
			rules := *schedule.FailureRules
			rules.Rules = append([]workpolicy.FailureRule(nil), rules.Rules...)
			for i := range rules.Rules {
				rules.Rules[i].ExitCodes = append([]int(nil), rules.Rules[i].ExitCodes...)
				rules.Rules[i].OutcomeCodes = append([]string(nil), rules.Rules[i].OutcomeCodes...)
				rules.Rules[i].HTTPStatuses = append([]int(nil), rules.Rules[i].HTTPStatuses...)
			}
			schedule.FailureRules = &rules
		}
		row.Schedule = &schedule
	}
	runtime := map[string]json.RawMessage{}
	for key, value := range row.Runtime {
		runtime[key] = append(json.RawMessage(nil), value...)
	}
	row.Runtime = runtime
	return row
}

func sortedEnvironmentWorkloadNames(workloads map[string]api.EnvironmentWorkload) []string {
	names := make([]string, 0, len(workloads))
	for name := range workloads {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func environmentGitOpsAppForWorkload(snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, appID string) (string, api.EnvironmentWorkload, gitOpsIntentApp, bool) {
	resources := make(map[string]string, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		resources[resource.Resource] = resource.AppID
	}
	var app gitOpsIntentApp
	for _, candidate := range snapshot.Apps {
		if candidate.ID == appID {
			app = candidate
			break
		}
	}
	if app.ID == "" {
		return "", api.EnvironmentWorkload{}, gitOpsIntentApp{}, false
	}
	for _, name := range sortedEnvironmentWorkloadNames(desired.Definition.Workloads) {
		resource := "workload/" + name
		if resources[resource] == appID {
			return resource, desired.Definition.Workloads[name], app, true
		}
	}
	return "", api.EnvironmentWorkload{}, gitOpsIntentApp{}, false
}

func workloadIntentFields(row EnvironmentWorkloadIntent) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	for key, value := range row.Variables {
		fields["variables/"+key], _ = json.Marshal(value)
	}
	for key, value := range row.ServiceBindings {
		fields["service_bindings/"+key], _ = json.Marshal(value)
	}
	for key, value := range row.Runtime {
		fields["runtime/"+key] = value
	}
	if row.Source != nil {
		fields["source"], _ = json.Marshal(row.Source)
	}
	if row.SourceRevision != "" {
		fields["source_revision"], _ = json.Marshal(row.SourceRevision)
	}
	if row.Schedule != nil {
		fields["schedule"], _ = json.Marshal(row.Schedule)
	}
	return fields
}

func workloadIntentChangedPaths(before, after EnvironmentWorkloadIntent) []string {
	old, next := workloadIntentFields(before), workloadIntentFields(after)
	paths := []string{}
	for key, value := range next {
		canonical, _ := canonicalGitOpsValue(value)
		prior, _ := canonicalGitOpsValue(old[key])
		if !bytes.Equal(canonical, prior) {
			paths = append(paths, key)
		}
	}
	for key := range old {
		if _, exists := next[key]; !exists {
			paths = append(paths, key)
		}
	}
	if before.SourceRevision != after.SourceRevision {
		paths = append(paths, "source")
	}
	return paths
}

func runtimeManifestValues(manifest AppManifest) map[string]json.RawMessage {
	// App intent stores grace periods in seconds; the runtime contract uses a
	// Go duration. Keep the inherited value in the same units as declared intent.
	raw, _ := json.Marshal(manifest)
	var runtime api.AppManifest
	_ = json.Unmarshal(raw, &runtime)
	runtime.StopGracePeriod = time.Duration(manifest.StopGracePeriodS) * time.Second
	values := map[string]json.RawMessage{}
	v, t := reflect.ValueOf(runtime), reflect.TypeOf(runtime)
	for i := 0; i < t.NumField(); i++ {
		key := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if key != "" && key != "-" && key != "env" && key != "env_secrets" {
			values[key], _ = json.Marshal(v.Field(i).Interface())
		}
	}
	return values
}

func validateWorkloadIntent(row EnvironmentWorkloadIntent, app App, environment string, plan api.Plan) (EnvironmentWorkloadIntent, error) {
	row = cloneWorkloadIntent(row)
	if err := validateEnvironmentServiceBindings(row.AppID, row.ServiceBindings); err != nil {
		return row, err
	}
	if row.SourceRevision != "" && (!environmentCommitRE.MatchString(row.SourceRevision) || row.Source == nil || row.Source.Kind == "image") {
		return row, ErrInvalidArgument
	}
	raw, _ := json.Marshal(row.Runtime)
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "intent", Environment: environment, Workloads: map[string]api.EnvironmentWorkload{"workload": {Source: row.Source, Runtime: raw, Variables: row.Variables, Schedule: row.Schedule}}})
	if err != nil {
		return row, fmt.Errorf("%w: invalid scoped workload intent", ErrInvalidArgument)
	}
	for _, binding := range row.ServiceBindings {
		if _, exists := row.Variables[binding.EnvKey]; exists {
			return row, ErrInvalidArgument
		}
	}
	w := desired.Definition.Workloads["workload"]
	if w.Source != nil && w.Source.Kind == "function" && (app.Type != AppTypeFunction || app.Runtime != w.Source.Runtime) {
		return row, ErrInvalidArgument
	}
	row.Source = w.Source
	row.Schedule = w.Schedule
	row.Variables = cloneStringMap(w.Variables)
	_ = json.Unmarshal(w.Runtime, &row.Runtime)
	values := runtimeManifestValues(app.Manifest)
	for key, value := range row.Runtime {
		values[key] = value
	}
	merged, _ := json.Marshal(values)
	var manifest api.AppManifest
	if json.Unmarshal(merged, &manifest) != nil || manifest.ValidateLifecyclePlan(plan) != nil {
		return row, ErrInvalidArgument
	}
	mode := manifest.EffectiveExecutionMode()
	if app.WorkloadClass == WorkloadClassWorker && mode != api.ExecutionModeWorker || app.WorkloadClass == WorkloadClassJob && mode != api.ExecutionModeJob || app.WorkloadClass == WorkloadClassHTTP && (mode == api.ExecutionModeWorker || mode == api.ExecutionModeJob) {
		return row, ErrInvalidArgument
	}
	return cloneWorkloadIntent(row), nil
}

// Adoption can preserve an unset nullable setting. A full replacement write
// may carry that unchanged baseline, but must not introduce new null settings.
func validateWorkloadIntentWrite(row, previous EnvironmentWorkloadIntent, app App, environment string, plan api.Plan) (EnvironmentWorkloadIntent, error) {
	row = cloneWorkloadIntent(row)
	unset := map[string]json.RawMessage{}
	known := runtimeManifestValues(app.Manifest)
	for key, value := range row.Runtime {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && bytes.Equal(bytes.TrimSpace(previous.Runtime[key]), []byte("null")) {
			if _, supported := known[key]; !supported {
				return row, ErrInvalidArgument
			}
			unset[key] = value
			delete(row.Runtime, key)
		}
	}
	row, err := validateWorkloadIntent(row, app, environment, plan)
	if err != nil {
		return row, err
	}
	for key, value := range unset {
		row.Runtime[key] = value
	}
	return row, nil
}

func gitOpsWorkloadField(path string) bool {
	return path == "source" || path == "source_revision" || path == "schedule" || strings.HasPrefix(path, "runtime/") || strings.HasPrefix(path, "service_bindings/") || strings.HasPrefix(path, "variables/")
}

func gitOpsWorkloadCandidateField(path string) bool {
	return gitOpsWorkloadField(path) || strings.HasPrefix(path, "queue_bindings/") || strings.HasPrefix(path, "secret_refs/")
}

// gitOpsWorkloadRequiresQualification mirrors the durable runtime fence in
// EnvironmentGitOpsUnqualifiedWorkloads. Variable and secret freshness use
// runtime-config receipts instead of this workload-qualification blocker.
func gitOpsWorkloadRequiresQualification(path string) bool {
	return path == "source" || path == "source_revision" || path == "schedule" ||
		strings.HasPrefix(path, "runtime/") || strings.HasPrefix(path, "service_bindings/") || strings.HasPrefix(path, "queue_bindings/")
}

func changedWorkloadIntents(snapshot gitOpsIntentSnapshot, plan environmentsync.Plan, ids map[string]string, preserve bool) map[string]EnvironmentWorkloadIntent {
	rows := map[string]EnvironmentWorkloadIntent{}
	for _, app := range snapshot.Apps {
		if app.WorkloadIntent != nil {
			rows[app.ID] = cloneWorkloadIntent(*app.WorkloadIntent)
		}
	}
	changed := map[string]EnvironmentWorkloadIntent{}
	for _, change := range plan.Changes {
		if !gitOpsWorkloadField(change.Path) || preserve && change.Action != "adopt" || !preserve && (change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden") {
			continue
		}
		id := ids[change.Resource]
		row := cloneWorkloadIntent(rows[id])
		row.AppID = id
		row.EnvironmentID = snapshot.EnvironmentID
		value := change.After
		if preserve {
			value = change.Before
		}
		if strings.HasPrefix(change.Path, "service_bindings/") {
			row = changeEnvironmentServiceBinding(row, change, value, ids)
			rows[id], changed[id] = row, row
			continue
		}
		if strings.HasPrefix(change.Path, "variables/") {
			key := strings.TrimPrefix(change.Path, "variables/")
			if row.Variables == nil {
				row.Variables = map[string]string{}
			}
			if change.Action == "remove" {
				delete(row.Variables, key)
			} else {
				var decoded string
				if json.Unmarshal(value, &decoded) != nil {
					continue
				}
				row.Variables[key] = decoded
			}
			rows[id], changed[id] = row, row
			continue
		}
		switch change.Path {
		case "source":
			row.Source = nil
			if change.Action != "remove" && !bytes.Equal(value, json.RawMessage("null")) {
				_ = json.Unmarshal(value, &row.Source)
			}
		case "source_revision":
			row.SourceRevision = ""
			if change.Action != "remove" {
				_ = json.Unmarshal(value, &row.SourceRevision)
			}
		case "schedule":
			row.Schedule = nil
			if change.Action != "remove" {
				_ = json.Unmarshal(value, &row.Schedule)
			}
		default:
			key := strings.TrimPrefix(change.Path, "runtime/")
			if change.Action == "remove" {
				delete(row.Runtime, key)
			} else {
				row.Runtime[key] = append(json.RawMessage(nil), value...)
			}
		}
		rows[id] = row
		changed[id] = row
	}
	return changed
}
