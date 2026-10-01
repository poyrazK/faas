package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
			if gitOpsQueueWantedOrOwned(snapshot, desired, resource, path) {
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
	for name, intent := range workload.QueueBindings {
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
		if _, wanted := workload.QueueBindings[strings.TrimPrefix(owner.Path, "queue_bindings/")]; !wanted {
			out.State.Unsupported = append(out.State.Unsupported, owner.Key()+": queue pruning requires a reviewed disposition for retained work")
		}
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

func gitOpsQueueMutationAllowed(change environmentsync.Change) error {
	if change.Action != "create" && change.Action != "update" {
		return fmt.Errorf("%w: queue removal requires reviewed retention", ErrConflict)
	}
	return nil
}

func gitOpsQueuePatch(binding QueueBinding) UpdateQueueBindingParams {
	retry := []byte(binding.RetryPolicyJSON)
	return UpdateQueueBindingParams{QueueName: &binding.QueueName, Mode: &binding.Mode, WorkloadClass: &binding.WorkloadClass, Enabled: &binding.Enabled, MaxConcurrency: &binding.MaxConcurrency, RetryPolicyJSON: &retry}
}

func queueBindingIntentEqual(before, after QueueBinding) bool {
	return bytes.Equal(mustGitOpsJSON(gitOpsQueueContract(before)), mustGitOpsJSON(gitOpsQueueContract(after)))
}
