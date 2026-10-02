package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// EnvironmentWorkloadIntent is scoped customer intent. Recording it does not
// qualify a deployment or prove that its serving graph uses these settings.
type EnvironmentWorkloadIntent struct {
	AccountID     string                         `json:"account_id"`
	AppID         string                         `json:"app_id"`
	EnvironmentID string                         `json:"environment_id"`
	Source        *api.EnvironmentWorkloadSource `json:"source"`
	Runtime       map[string]json.RawMessage     `json:"runtime"`
	CreatedAt     time.Time                      `json:"created_at"`
	UpdatedAt     time.Time                      `json:"updated_at"`
}

type EnvironmentWorkloadIntentStore interface {
	EnvironmentWorkloadIntent(context.Context, string, string, string) (EnvironmentWorkloadIntent, error)
	PutEnvironmentWorkloadIntent(context.Context, EnvironmentWorkloadIntent) (EnvironmentWorkloadIntent, error)
}

type environmentWorkloadIntentKey struct{ AppID, EnvironmentID string }

type gitOpsSourceBaseline struct {
	ID    string         `json:"id"`
	Kind  DeploymentKind `json:"kind"`
	Image string         `json:"image"`
}

func cloneWorkloadIntent(row EnvironmentWorkloadIntent) EnvironmentWorkloadIntent {
	if row.Source != nil {
		source := *row.Source
		row.Source = &source
	}
	runtime := map[string]json.RawMessage{}
	for key, value := range row.Runtime {
		runtime[key] = append(json.RawMessage(nil), value...)
	}
	row.Runtime = runtime
	return row
}

func workloadIntentFields(row EnvironmentWorkloadIntent) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	for key, value := range row.Runtime {
		fields["runtime/"+key] = value
	}
	if row.Source != nil {
		fields["source"], _ = json.Marshal(row.Source)
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
	raw, _ := json.Marshal(row.Runtime)
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "intent", Environment: environment, Workloads: map[string]api.EnvironmentWorkload{"workload": {Source: row.Source, Runtime: raw}}})
	if err != nil {
		return row, fmt.Errorf("%w: invalid scoped workload intent", ErrInvalidArgument)
	}
	w := desired.Definition.Workloads["workload"]
	row.Source = w.Source
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
	return path == "source" || strings.HasPrefix(path, "runtime/")
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
		if change.Path == "source" {
			row.Source = nil
			if change.Action != "remove" && !bytes.Equal(value, json.RawMessage("null")) {
				_ = json.Unmarshal(value, &row.Source)
			}
		} else {
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
