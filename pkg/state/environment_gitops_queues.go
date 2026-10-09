package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type gitOpsQueueIdentity struct {
	Resource  string `json:"resource"`
	Path      string `json:"path"`
	BindingID string `json:"binding_id"`
}

type gitOpsQueueConsumer struct {
	ID                   string          `json:"id"`
	Slug                 string          `json:"slug"`
	Enabled              bool            `json:"enabled"`
	Config               json.RawMessage `json:"config"`
	BatchSize            int32           `json:"batch_size"`
	BatchWindow          int32           `json:"batch_window"`
	MaxAttempts          int32           `json:"max_attempts"`
	PayloadMax           int32           `json:"payload_max"`
	BrokerPoisonStrategy string          `json:"broker_poison_strategy"`
	FilterCriteria       json.RawMessage `json:"filter_criteria"`
}

type gitOpsQueueIntent struct {
	ID        string                      `json:"id"`
	Name      string                      `json:"name"`
	Intent    api.EnvironmentQueueBinding `json:"intent"`
	RetiredAt *time.Time                  `json:"retired_at"`
	Consumers []gitOpsQueueConsumer       `json:"consumers"`
}

// Resource IDs participate in the reviewed plan hash but never enter Git.
func gitOpsQueueResource(resource, path string) string { return resource + "/" + path }

func gitOpsQueueContract(binding QueueBinding) api.EnvironmentQueueBinding {
	enabled := binding.Enabled
	value := api.EnvironmentQueueBinding{QueueName: binding.QueueName, Mode: binding.Mode, WorkloadClass: string(binding.WorkloadClass), Enabled: &enabled, MaxConcurrency: binding.MaxConcurrency}
	var retry api.RetryPolicyDTO
	if json.Unmarshal(binding.RetryPolicyJSON, &retry) == nil && retry != (api.RetryPolicyDTO{}) {
		value.RetryPolicy = &retry
	}
	return value
}

func observeGitOpsQueues(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	byID, byName := map[string]gitOpsQueueIntent{}, map[string]gitOpsQueueIntent{}
	mapped := map[string]string{}
	for _, binding := range app.QueueBindings {
		byID[binding.ID], byName[binding.Name] = binding, binding
	}
	for _, identity := range snapshot.QueueBindings {
		if identity.Resource == resource {
			mapped[identity.Path] = identity.BindingID
		}
	}
	for path, id := range mapped {
		if binding, ok := byID[id]; !ok || binding.Name != strings.TrimPrefix(path, "queue_bindings/") {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#"+path+": original queue identity is unavailable; reviewed recovery is required")
		}
	}
	for name, binding := range byName {
		path := "queue_bindings/" + name
		// Persisted mappings fence name reuse even if an external writer changes a label.
		if id, ok := mapped[path]; ok && id != binding.ID {
			continue
		}
		out.State.ResourceIDs[gitOpsQueueResource(resource, path)] = binding.ID
		if len(binding.Consumers) == 1 {
			out.State.ResourceIDs[gitOpsQueueResource(resource, path)+"/consumer"] = binding.Consumers[0].ID
		}
		if binding.RetiredAt != nil {
			if gitOpsQueueRetirementReviewed(snapshot, desired, resource, path, binding.ID) {
				// Explicit absence still requires adoption before recovery. Merely
				// putting the old name back in Git cannot release accepted work.
				out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: path, Value: json.RawMessage("null")})
				if _, recovering := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")].QueueBindings[name]; recovering && !gitOpsRetiredQueueProjectionMatches(binding, snapshot.Plan) {
					out.State.Unsupported = append(out.State.Unsupported, resource+"#"+path+": retained queue consumer projection requires repair")
				}
			} else if gitOpsQueueWantedOrOwned(snapshot, desired, resource, path) {
				out.State.Unsupported = append(out.State.Unsupported, resource+"#"+path+": queue is retired; reviewed recovery is required")
			}
			continue
		}
		value := binding.Intent
		if value.RetryPolicy != nil && *value.RetryPolicy == (api.RetryPolicyDTO{}) {
			value.RetryPolicy = nil
		}
		raw, _ := json.Marshal(value)
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: path, Value: raw})
		if gitOpsQueueWantedOrOwned(snapshot, desired, resource, path) && !gitOpsQueueProjectionMatches(binding, snapshot.Plan) {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#"+path+": queue consumer projection requires repair")
		}
	}
	validateGitOpsDesiredQueues(out, snapshot, desired, resource, app)
}

func gitOpsQueueRetirementReviewed(snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource, path, id string) bool {
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	name := strings.TrimPrefix(path, "queue_bindings/")
	if _, wanted := workload.QueueBindings[name]; wanted {
		return gitOpsQueueRecoveryMatches(workload.QueueRecoveries[name], id)
	}
	return snapshot.Prune && desired.Definition.QueuePruningPolicy == "retain"
}

func gitOpsQueueRecoveryMatches(recovery, id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && recovery == parsed.String()
}

func gitOpsRetiredQueueProjectionMatches(binding gitOpsQueueIntent, plan api.Plan) bool {
	// Retirement changes only Enabled. Keep the original consumer namespace
	// and require its full projection before it may deliver retained work again.
	if len(binding.Consumers) == 1 && binding.Consumers[0].Enabled {
		return false
	}
	binding.Intent.Enabled = new(bool)
	return gitOpsQueueProjectionMatches(binding, plan)
}

func gitOpsQueueWantedOrOwned(snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource, path string) bool {
	for _, field := range desired.Fields {
		if field.Resource == resource && field.Path == path {
			return true
		}
	}
	for _, owner := range snapshot.Owners {
		if owner.Resource == resource && owner.Path == path {
			return true
		}
	}
	return false
}

func validateGitOpsDesiredQueues(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	if app.WorkloadClass == WorkloadClassJob {
		for _, binding := range app.QueueBindings {
			if binding.Intent.Enabled != nil && !*binding.Intent.Enabled {
				continue
			}
			// A reviewed disable is a safe way to retire an existing consumer
			// before the Job graph is applied. Otherwise an enabled live binding
			// blocks this plan even when Git omits the binding and would retain it.
			if wanted, exists := workload.QueueBindings[binding.Name]; exists && wanted.Enabled != nil && !*wanted.Enabled {
				continue
			}
			appendGitOpsQueueUnsupported(out, resource+"#queue_bindings/"+binding.Name+": environment_job_queue_binding_execution_unsupported")
		}
	}
	for name, intent := range workload.QueueBindings {
		// Job queue execution has no production adapter yet. Keep a disabled
		// binding available for a reviewed reservation, but block enabling it
		// during planning rather than waiting for qualification/activation to
		// discover that no executor can acknowledge its messages.
		if app.WorkloadClass == WorkloadClassJob && (intent.Enabled == nil || *intent.Enabled) {
			appendGitOpsQueueUnsupported(out, resource+"#queue_bindings/"+name+": environment_job_queue_binding_execution_unsupported")
			continue
		}
		if id := workload.QueueRecoveries[name]; id != "" && !gitOpsQueueRecoveryMatches(id, out.State.ResourceIDs[gitOpsQueueResource(resource, "queue_bindings/"+name)]) {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#queue_bindings/"+name+": recovery must name the original scoped binding UUID")
		}
		binding, err := decodeGitOpsQueue("queue_bindings/"+name, mustGitOpsJSON(intent), snapshot.EnvironmentID, snapshot.Environment, app.ID, "")
		if err == nil {
			err = validateQueueBindingConsumer(binding, app.Type, app.WorkloadClass)
		}
		limits, _ := api.LimitsFor(snapshot.Plan)
		if err == nil && binding.Mode == "push" {
			_, err = queueConsumerForBinding(binding, limits)
		}
		if err != nil {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#queue_bindings/"+name+": queue binding is not supported by this workload or plan")
		}
	}
	for _, owner := range snapshot.Owners {
		if owner.Resource != resource || owner.Manager != snapshot.SourceID || !strings.HasPrefix(owner.Path, "queue_bindings/") {
			continue
		}
		if _, wanted := workload.QueueBindings[strings.TrimPrefix(owner.Path, "queue_bindings/")]; !wanted && desired.Definition.QueuePruningPolicy != "retain" {
			out.State.Unsupported = append(out.State.Unsupported, owner.Key()+": queue pruning requires a reviewed disposition for retained work")
		}
	}
}

func appendGitOpsQueueUnsupported(out *EnvironmentGitOpsObservation, blocker string) {
	if !slices.Contains(out.State.Unsupported, blocker) {
		out.State.Unsupported = append(out.State.Unsupported, blocker)
	}
}

func mustGitOpsJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func decodeGitOpsQueue(path string, raw json.RawMessage, environmentID, scope, appID, accountID string) (QueueBinding, error) {
	var value api.EnvironmentQueueBinding
	if json.Unmarshal(raw, &value) != nil || value.Enabled == nil || !strings.HasPrefix(path, "queue_bindings/") {
		return QueueBinding{}, ErrInvalidArgument
	}
	retry := []byte(`{}`)
	if value.RetryPolicy != nil {
		retry, _ = json.Marshal(value.RetryPolicy)
	}
	return QueueBinding{AccountID: accountID, AppID: appID, EnvironmentID: environmentID, DeploymentScope: scope, Name: strings.TrimPrefix(path, "queue_bindings/"), QueueName: value.QueueName,
		Mode: value.Mode, WorkloadClass: WorkloadClass(value.WorkloadClass), Enabled: *value.Enabled, MaxConcurrency: value.MaxConcurrency, RetryPolicyJSON: retry}, nil
}

func gitOpsQueueProjectionMatches(binding gitOpsQueueIntent, plan api.Plan) bool {
	if len(binding.Consumers) > 1 {
		return false
	}
	if len(binding.Consumers) == 1 {
		consumer := binding.Consumers[0]
		filter := bytes.TrimSpace(consumer.FilterCriteria)
		if consumer.BrokerPoisonStrategy != "commit" || len(filter) > 0 && !bytes.Equal(filter, []byte("null")) {
			return false
		}
	}
	if binding.Intent.Mode != "push" {
		return len(binding.Consumers) == 0 || !binding.Consumers[0].Enabled
	}
	if len(binding.Consumers) != 1 {
		return false
	}
	consumer := binding.Consumers[0]
	row, err := decodeGitOpsQueue("queue_bindings/"+binding.Name, mustGitOpsJSON(binding.Intent), "", "", "", "")
	if err != nil {
		return false
	}
	row.ID = binding.ID
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return false
	}
	definition, err := queueConsumerForBinding(row, limits)
	if err != nil {
		return false
	}
	left, _ := canonicalGitOpsValue(consumer.Config)
	right, _ := canonicalGitOpsValue(definition.Config)
	return consumer.Slug == row.QueueName && consumer.Enabled == row.Enabled && bytes.Equal(left, right) && consumer.BatchSize == definition.BatchSize &&
		consumer.BatchWindow == definition.BatchWindow && consumer.MaxAttempts == definition.MaxAttempts && consumer.PayloadMax == definition.PayloadMax
}

func gitOpsQueueMutationAllowed(change environmentsync.Change, definition api.EnvironmentDefinition) error {
	if change.Action == "remove" && definition.QueuePruningPolicy == "retain" {
		return nil
	}
	if change.Action != "create" && change.Action != "update" {
		return fmt.Errorf("%w: queue removal requires reviewed retention", ErrConflict)
	}
	return nil
}

// Retire removed consumers before admitting replacements. All operations still
// commit as one intent transaction, including quota checks and notifications.
func gitOpsQueueRetireFirst(changes []environmentsync.Change) []environmentsync.Change {
	out := make([]environmentsync.Change, 0, len(changes))
	for _, change := range changes {
		if change.Action == "remove" && strings.HasPrefix(change.Path, "queue_bindings/") {
			out = append(out, change)
		}
	}
	for _, change := range changes {
		if change.Action != "remove" || !strings.HasPrefix(change.Path, "queue_bindings/") {
			out = append(out, change)
		}
	}
	return out
}

func gitOpsQueuePatch(binding QueueBinding) UpdateQueueBindingParams {
	retry := []byte(binding.RetryPolicyJSON)
	return UpdateQueueBindingParams{QueueName: &binding.QueueName, Mode: &binding.Mode, WorkloadClass: &binding.WorkloadClass, Enabled: &binding.Enabled, MaxConcurrency: &binding.MaxConcurrency, RetryPolicyJSON: &retry}
}

func queueBindingIntentEqual(before, after QueueBinding) bool {
	return bytes.Equal(mustGitOpsJSON(gitOpsQueueContract(before)), mustGitOpsJSON(gitOpsQueueContract(after)))
}
