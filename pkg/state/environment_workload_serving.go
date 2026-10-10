package state

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// EnvironmentWorkloadServingRoute identifies one app route whose deployment
// weights were changed by the serving phase. Each app has a distinct route
// generation so its gateway acknowledgements cannot satisfy another app.
type EnvironmentWorkloadServingRoute struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
	Generation   int64  `json:"generation"`
	Cutover      bool   `json:"cutover"`
}

// EnvironmentWorkloadServingReceipt is durable progress for post-activation
// serving. HTTP routes require every gateway acknowledgement; queue consumers
// require a completed invocation for every reviewed queue consumer; scheduled
// Jobs require one successful, revision-current occurrence after activation.
type EnvironmentWorkloadServingReceipt struct {
	GraphID                string                                      `json:"graph_id"`
	SourceID               string                                      `json:"-"`
	SourceGeneration       int64                                       `json:"-"`
	IntentVersion          int64                                       `json:"-"`
	RevisionID             string                                      `json:"-"`
	PlanHash               string                                      `json:"-"`
	ReleaseSetID           string                                      `json:"release_set_id"`
	ReleaseCreatedAt       time.Time                                   `json:"release_created_at,omitempty"`
	ExpectedGateways       []string                                    `json:"expected_gateways"`
	Routes                 []EnvironmentWorkloadServingRoute           `json:"routes"`
	Acknowledgements       map[int64][]string                          `json:"acknowledgements"`
	ExpectedQueueConsumers []EnvironmentWorkloadServingQueueConsumer   `json:"-"`
	QueueAcknowledgements  []EnvironmentWorkloadServingQueueAck        `json:"queue_acknowledgements,omitempty"`
	ExpectedScheduledJobs  []EnvironmentWorkloadServingScheduledJob    `json:"-"`
	ScheduledJobAcks       []EnvironmentWorkloadServingScheduledJobAck `json:"scheduled_job_acknowledgements,omitempty"`
	Serving                bool                                        `json:"serving"`
	ServedAt               *time.Time                                  `json:"served_at,omitempty"`
}

type EnvironmentWorkloadServingScheduledJob struct {
	AppID           string                                     `json:"app_id"`
	JobID           string                                     `json:"job_id"`
	ServiceBindings map[string]EnvironmentScopedServiceBinding `json:"-"`
}

// EnvironmentWorkloadServingScheduledJobAck identifies the successful
// scheduled occurrence that proves the active GitOps Job actually executed.
type EnvironmentWorkloadServingScheduledJobAck struct {
	AppID           string                                     `json:"app_id"`
	JobID           string                                     `json:"job_id"`
	JobRunID        string                                     `json:"job_run_id"`
	OccurrenceID    string                                     `json:"occurrence_id"`
	AcknowledgedAt  time.Time                                  `json:"acknowledged_at"`
	ServiceBindings map[string]EnvironmentScopedServiceBinding `json:"service_bindings,omitempty"`
}

// EnvironmentWorkloadServingQueueAck is a durable proof that a reviewed queue
// consumer completed an invocation on the deployment selected for the active
// environment release.
type EnvironmentWorkloadServingQueueAck struct {
	Mode         string    `json:"mode"`
	AppID        string    `json:"app_id"`
	BindingID    string    `json:"binding_id"`
	TriggerID    string    `json:"trigger_id"`
	DeploymentID string    `json:"deployment_id"`
	InvocationID string    `json:"invocation_id"`
	Acknowledged time.Time `json:"acknowledged_at"`
}

type EnvironmentWorkloadServingQueueConsumer struct {
	Mode         string
	Resource     string
	AppID        string
	DeploymentID string
	BindingName  string
	BindingID    string
	TriggerID    string
}

// EnvironmentWorkloadQueueServingAcknowledgement is emitted only after the
// queue invocation is durably completed; push mode additionally requires its
// trigger receipt. The selected deployment comes from the internal gateway
// response, not the queue payload.
type EnvironmentWorkloadQueueServingAcknowledgement struct {
	Mode         string
	AppID        string
	Scope        string
	BindingID    string
	TriggerID    string
	DeploymentID string
	InvocationID string
}

type EnvironmentGitOpsQueueServingAckStore interface {
	RecordEnvironmentGitOpsQueueServingAcknowledgement(context.Context, EnvironmentWorkloadQueueServingAcknowledgement) error
}

// EnvironmentGitOpsPullQueueCompletionStore atomically completes a generic
// pull-queue invocation and records its serving evidence when it belongs to
// the active reviewed workload graph. Implementations fence the completion
// with the invocation's claim attempt.
type EnvironmentGitOpsPullQueueCompletionStore interface {
	CompleteEnvironmentGitOpsPullQueueDelivery(context.Context, string, int, string, json.RawMessage) error
}

// EnvironmentGitOpsWorkloadServingStore performs the separately fenced route
// cutover after a graph is qualified and active, then records gateway acks.
type EnvironmentGitOpsWorkloadServingStore interface {
	PrepareEnvironmentGitOpsWorkloadServing(context.Context, EnvironmentGitOpsLease, environmentsync.Plan, []string) (EnvironmentWorkloadServingReceipt, bool, error)
	AcknowledgeEnvironmentGitOpsWorkloadServing(context.Context, EnvironmentGitOpsLease, string, int64, string) (EnvironmentWorkloadServingReceipt, bool, error)
}

func cloneEnvironmentWorkloadServingReceipt(in EnvironmentWorkloadServingReceipt) EnvironmentWorkloadServingReceipt {
	out := in
	out.ExpectedGateways = slices.Clone(in.ExpectedGateways)
	out.Routes = slices.Clone(in.Routes)
	out.ExpectedQueueConsumers = slices.Clone(in.ExpectedQueueConsumers)
	out.QueueAcknowledgements = slices.Clone(in.QueueAcknowledgements)
	out.ExpectedScheduledJobs = slices.Clone(in.ExpectedScheduledJobs)
	for i := range out.ExpectedScheduledJobs {
		out.ExpectedScheduledJobs[i].ServiceBindings = maps.Clone(in.ExpectedScheduledJobs[i].ServiceBindings)
	}
	out.ScheduledJobAcks = slices.Clone(in.ScheduledJobAcks)
	for i := range out.ScheduledJobAcks {
		out.ScheduledJobAcks[i].ServiceBindings = maps.Clone(in.ScheduledJobAcks[i].ServiceBindings)
	}
	out.Acknowledgements = make(map[int64][]string, len(in.Acknowledgements))
	for generation, nodes := range in.Acknowledgements {
		out.Acknowledgements[generation] = slices.Clone(nodes)
	}
	if in.ServedAt != nil {
		servedAt := *in.ServedAt
		out.ServedAt = &servedAt
	}
	return out
}

func environmentWorkloadServingReceiptMatches(receipt EnvironmentWorkloadServingReceipt, graph EnvironmentWorkloadGraph,
	releaseSetID string, gateways []string, targets map[string]string) bool {
	if !receipt.Serving || receipt.GraphID != graph.ID || receipt.ReleaseSetID != releaseSetID || receipt.ServedAt == nil ||
		!environmentGraphSupportsProductionServingForTargets(graph, targets) {
		return false
	}
	expectedRoutes := make(map[string]EnvironmentWorkloadServingRoute)
	for _, member := range graph.Members {
		deploymentID, ok := environmentGraphMemberServingDeployment(member, targets)
		if !ok {
			return false
		}
		if member.ExecutionMode == api.ExecutionModeRequest || member.ExecutionMode == api.ExecutionModeService {
			expectedRoutes[member.AppID] = EnvironmentWorkloadServingRoute{AppID: member.AppID, DeploymentID: deploymentID,
				Cutover: member.CandidateDeploymentID != ""}
		}
	}
	if len(expectedRoutes) != len(receipt.Routes) || len(expectedRoutes) == 0 && len(receipt.ExpectedGateways) != 0 ||
		len(expectedRoutes) > 0 && (len(gateways) == 0 || !slices.Equal(receipt.ExpectedGateways, gateways)) {
		return false
	}
	for _, route := range receipt.Routes {
		wanted, ok := expectedRoutes[route.AppID]
		if !ok || wanted.DeploymentID != route.DeploymentID || wanted.Cutover != route.Cutover || route.Generation <= 0 {
			return false
		}
		if !slices.Equal(receipt.Acknowledgements[route.Generation], gateways) {
			return false
		}
	}
	expectedConsumers, ok := environmentGraphServingQueueConsumers(graph, targets)
	if !ok || len(expectedConsumers) != len(receipt.QueueAcknowledgements) {
		return false
	}
	acks := make(map[string]EnvironmentWorkloadServingQueueAck, len(receipt.QueueAcknowledgements))
	for _, acknowledgement := range receipt.QueueAcknowledgements {
		if acknowledgement.Mode == "" || acknowledgement.BindingID == "" || acknowledgement.InvocationID == "" || acknowledgement.Acknowledged.IsZero() {
			return false
		}
		if _, duplicate := acks[acknowledgement.BindingID]; duplicate {
			return false
		}
		acks[acknowledgement.BindingID] = acknowledgement
	}
	for _, consumer := range expectedConsumers {
		acknowledgement, ok := acks[consumer.BindingID]
		if !ok || acknowledgement.Mode != consumer.Mode || acknowledgement.AppID != consumer.AppID || acknowledgement.TriggerID != consumer.TriggerID ||
			acknowledgement.DeploymentID != consumer.DeploymentID {
			return false
		}
	}
	return environmentWorkloadServingScheduledJobAcksMatch(receipt, environmentGitOpsScheduledJobMembers(graph))
}

func environmentGraphSupportsProductionServing(graph EnvironmentWorkloadGraph) bool {
	return environmentGraphSupportsProductionServingForTargets(graph, nil)
}

func environmentGraphSupportsProductionServingForTargets(graph EnvironmentWorkloadGraph, targets map[string]string) bool {
	if !environmentGraphServiceBindingsSupported(graph, targets) {
		return false
	}
	_, ok := environmentGraphServingQueueConsumers(graph, targets)
	return ok
}

// environmentGraphServiceBindingsSupported validates the reviewed logical and
// physical target identities frozen into the graph. Production route lookup
// independently rechecks these identities against the active release set.
func environmentGraphServiceBindingsSupported(graph EnvironmentWorkloadGraph, targets map[string]string) bool {
	if len(graph.Members) == 0 {
		return false
	}
	members := make(map[string]EnvironmentWorkloadGraphMember, len(graph.Members))
	for _, member := range graph.Members {
		if member.Resource == "" || member.AppID == "" {
			return false
		}
		if _, duplicate := members[member.Resource]; duplicate {
			return false
		}
		members[member.Resource] = member
	}
	for _, member := range graph.Members {
		if member.ServiceBindingsConfigured != (len(member.ServiceBindings) != 0) {
			return false
		}
		if len(member.ServiceBindings) == 0 {
			continue
		}
		switch member.ExecutionMode {
		case api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker:
		case api.ExecutionModeJob:
			if !member.ScheduleConfigured || !member.JobSmokeConfigured || !canonicalStateUUID(member.JobID) ||
				member.QueueBindingsConfigured || len(member.QueueModes) != 0 || len(member.QueueBindings) != 0 {
				return false
			}
		default:
			return false
		}
		seenEnvKeys := make(map[string]struct{}, len(member.ServiceBindings))
		for name, binding := range member.ServiceBindings {
			if !api.ValidAppSlug(name) || !api.ValidAppSlug(binding.Workload) || api.ValidateEnvKey(binding.EnvKey) != nil ||
				!canonicalStateUUID(binding.TargetAppID) || member.AppID == binding.TargetAppID {
				return false
			}
			if _, duplicate := seenEnvKeys[binding.EnvKey]; duplicate {
				return false
			}
			seenEnvKeys[binding.EnvKey] = struct{}{}
			target, exists := members["workload/"+binding.Workload]
			if !exists || target.AppID != binding.TargetAppID ||
				(target.ExecutionMode != api.ExecutionModeRequest && target.ExecutionMode != api.ExecutionModeService) ||
				(member.CandidateDeploymentID == "") != (target.CandidateDeploymentID == "") {
				return false
			}
			if targets != nil {
				if _, ok := environmentGraphMemberServingDeployment(target, targets); !ok {
					return false
				}
			}
		}
	}
	// A malformed resource spelling must not alias another workload through a
	// prefix or case-folded lookup.
	for resource := range members {
		if !strings.HasPrefix(resource, "workload/") || !api.ValidAppSlug(strings.TrimPrefix(resource, "workload/")) {
			return false
		}
	}
	return true
}

// environmentGraphServingQueueConsumers accepts request/service members with
// no enabled queue binding, HTTP functions with reviewed push bindings, and
// worker members whose enabled queue contracts are reviewed push or pull
// bindings with immutable binding identities, plus scheduled Jobs whose
// reviewed schedule and smoke contract are bound to a managed Job record.
func environmentGraphServingQueueConsumers(graph EnvironmentWorkloadGraph, targets map[string]string) ([]EnvironmentWorkloadServingQueueConsumer, bool) {
	if len(graph.Members) == 0 {
		return nil, false
	}
	consumers := []EnvironmentWorkloadServingQueueConsumer{}
	seenResources, seenApps, seenBindings := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, member := range graph.Members {
		if member.Resource == "" || member.AppID == "" {
			return nil, false
		}
		if _, duplicate := seenResources[member.Resource]; duplicate {
			return nil, false
		}
		if _, duplicate := seenApps[member.AppID]; duplicate {
			return nil, false
		}
		seenResources[member.Resource], seenApps[member.AppID] = struct{}{}, struct{}{}
		deploymentID := ""
		if targets != nil {
			var ok bool
			deploymentID, ok = environmentGraphMemberServingDeployment(member, targets)
			if !ok {
				return nil, false
			}
		}
		switch member.ExecutionMode {
		case api.ExecutionModeRequest:
			if member.Function {
				if !environmentGraphServingHTTPFunctionQueueConsumers(member, deploymentID, seenBindings, &consumers) {
					return nil, false
				}
				break
			}
			if len(member.QueueModes) != 0 {
				return nil, false
			}
			for _, binding := range member.QueueBindings {
				if binding.Contract.Enabled != nil && *binding.Contract.Enabled {
					return nil, false
				}
			}
		case api.ExecutionModeService:
			if len(member.QueueModes) != 0 {
				return nil, false
			}
			for _, binding := range member.QueueBindings {
				if binding.Contract.Enabled != nil && *binding.Contract.Enabled {
					return nil, false
				}
			}
		case api.ExecutionModeWorker:
			if len(member.QueueModes) == 0 || len(member.QueueBindings) == 0 {
				return nil, false
			}
			enabledBindings := 0
			for name, binding := range member.QueueBindings {
				if binding.Contract.Enabled == nil || !*binding.Contract.Enabled {
					continue
				}
				enabledBindings++
				mode := binding.Contract.Mode
				if !api.ValidQueueBindingName(name) || (mode != "push" && mode != "pull") || binding.Contract.WorkloadClass != "worker" ||
					member.QueueModes[name] != mode || !canonicalStateUUID(binding.BindingID) ||
					(mode == "push" && !canonicalStateUUID(binding.TriggerID)) || (mode == "pull" && binding.TriggerID != "") {
					return nil, false
				}
				if _, duplicate := seenBindings[binding.BindingID]; duplicate {
					return nil, false
				}
				seenBindings[binding.BindingID] = struct{}{}
				consumers = append(consumers, EnvironmentWorkloadServingQueueConsumer{Mode: mode, Resource: member.Resource, AppID: member.AppID,
					DeploymentID: deploymentID, BindingName: name, BindingID: binding.BindingID, TriggerID: binding.TriggerID})
			}
			if enabledBindings == 0 || enabledBindings != len(member.QueueModes) {
				return nil, false
			}
			for name, mode := range member.QueueModes {
				binding, exists := member.QueueBindings[name]
				if (mode != "push" && mode != "pull") || !exists || binding.Contract.Mode != mode || binding.Contract.Enabled == nil || !*binding.Contract.Enabled {
					return nil, false
				}
			}
		case api.ExecutionModeJob:
			if !member.ScheduleConfigured || !member.JobSmokeConfigured || !canonicalStateUUID(member.JobID) ||
				member.QueueBindingsConfigured || len(member.QueueModes) != 0 || len(member.QueueBindings) != 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return consumers, true
}

func environmentGraphServingHTTPFunctionQueueConsumers(member EnvironmentWorkloadGraphMember, deploymentID string,
	seenBindings map[string]struct{}, consumers *[]EnvironmentWorkloadServingQueueConsumer) bool {
	enabledBindings := 0
	for name, binding := range member.QueueBindings {
		if binding.Contract.Enabled == nil || !*binding.Contract.Enabled {
			continue
		}
		enabledBindings++
		if !api.ValidQueueBindingName(name) || binding.Contract.Mode != "push" || binding.Contract.WorkloadClass != "http" ||
			member.QueueModes[name] != "push" || !canonicalStateUUID(binding.BindingID) || !canonicalStateUUID(binding.TriggerID) {
			return false
		}
		if _, duplicate := seenBindings[binding.BindingID]; duplicate {
			return false
		}
		seenBindings[binding.BindingID] = struct{}{}
		*consumers = append(*consumers, EnvironmentWorkloadServingQueueConsumer{Mode: "push", Resource: member.Resource, AppID: member.AppID,
			DeploymentID: deploymentID, BindingName: name, BindingID: binding.BindingID, TriggerID: binding.TriggerID})
	}
	if enabledBindings != len(member.QueueModes) {
		return false
	}
	for name, mode := range member.QueueModes {
		binding, exists := member.QueueBindings[name]
		if mode != "push" || !exists || binding.Contract.Enabled == nil || !*binding.Contract.Enabled {
			return false
		}
	}
	return true
}

func environmentGraphMemberServingDeployment(member EnvironmentWorkloadGraphMember, targets map[string]string) (string, bool) {
	deploymentID := targets[member.Resource]
	if deploymentID == "" || (member.CandidateDeploymentID != "" && deploymentID != member.CandidateDeploymentID) ||
		(member.CandidateDeploymentID == "" && !slices.Contains(member.RetainedDeployments, deploymentID)) {
		return "", false
	}
	return deploymentID, true
}

func canonicalStateUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return false
	}
	if parsed.String() == value {
		return true
	}
	// MemStore uses the UUID's lowercase 32-hex representation for IDs while
	// PgStore exposes the standard hyphenated form. Both identify the same
	// canonical 128-bit value; reject other accepted-but-noncanonical UUID
	// spellings so the two stores enforce the same identity boundary.
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func environmentWorkloadServingReceiptAcknowledged(receipt EnvironmentWorkloadServingReceipt) bool {
	if len(receipt.Routes) == 0 && len(receipt.ExpectedQueueConsumers) == 0 && len(receipt.ExpectedScheduledJobs) == 0 {
		return false
	}
	if len(receipt.Routes) > 0 {
		if len(receipt.ExpectedGateways) == 0 {
			return false
		}
		for _, route := range receipt.Routes {
			if !slices.Equal(receipt.Acknowledgements[route.Generation], receipt.ExpectedGateways) {
				return false
			}
		}
	}
	acks := make(map[string]EnvironmentWorkloadServingQueueAck, len(receipt.QueueAcknowledgements))
	for _, acknowledgement := range receipt.QueueAcknowledgements {
		if acknowledgement.Mode == "" || acknowledgement.BindingID == "" || acknowledgement.InvocationID == "" || acknowledgement.Acknowledged.IsZero() {
			return false
		}
		if _, duplicate := acks[acknowledgement.BindingID]; duplicate {
			return false
		}
		acks[acknowledgement.BindingID] = acknowledgement
	}
	if len(acks) != len(receipt.ExpectedQueueConsumers) {
		return false
	}
	for _, consumer := range receipt.ExpectedQueueConsumers {
		acknowledgement, ok := acks[consumer.BindingID]
		if !ok || acknowledgement.Mode != consumer.Mode || acknowledgement.AppID != consumer.AppID || acknowledgement.TriggerID != consumer.TriggerID ||
			acknowledgement.DeploymentID != consumer.DeploymentID {
			return false
		}
	}
	return environmentWorkloadServingScheduledJobAcksMatch(receipt, receipt.ExpectedScheduledJobs)
}

func environmentWorkloadServingScheduledJobAcksMatch(receipt EnvironmentWorkloadServingReceipt,
	expected []EnvironmentWorkloadServingScheduledJob) bool {
	if len(expected) != len(receipt.ScheduledJobAcks) {
		return false
	}
	if len(expected) == 0 {
		return true
	}
	if receipt.ReleaseCreatedAt.IsZero() {
		return false
	}
	expectedByApp := make(map[string]EnvironmentWorkloadServingScheduledJob, len(expected))
	for _, job := range expected {
		if !canonicalStateUUID(job.AppID) || !canonicalStateUUID(job.JobID) {
			return false
		}
		if _, duplicate := expectedByApp[job.AppID]; duplicate {
			return false
		}
		expectedByApp[job.AppID] = job
	}
	seenApps, seenRuns, seenOccurrences := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, acknowledgement := range receipt.ScheduledJobAcks {
		if !canonicalStateUUID(acknowledgement.AppID) || !canonicalStateUUID(acknowledgement.JobID) ||
			!canonicalStateUUID(acknowledgement.JobRunID) || !canonicalStateUUID(acknowledgement.OccurrenceID) ||
			acknowledgement.AcknowledgedAt.IsZero() || acknowledgement.AcknowledgedAt.Before(receipt.ReleaseCreatedAt) {
			return false
		}
		wanted, exists := expectedByApp[acknowledgement.AppID]
		if !exists || wanted.JobID != acknowledgement.JobID || !maps.Equal(wanted.ServiceBindings, acknowledgement.ServiceBindings) {
			return false
		}
		if _, duplicate := seenApps[acknowledgement.AppID]; duplicate {
			return false
		}
		if _, duplicate := seenRuns[acknowledgement.JobRunID]; duplicate {
			return false
		}
		if _, duplicate := seenOccurrences[acknowledgement.OccurrenceID]; duplicate {
			return false
		}
		seenApps[acknowledgement.AppID] = struct{}{}
		seenRuns[acknowledgement.JobRunID] = struct{}{}
		seenOccurrences[acknowledgement.OccurrenceID] = struct{}{}
	}
	return len(seenApps) == len(expectedByApp)
}
