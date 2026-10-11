package state

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCanonicalStateUUIDAcceptsBothStoreRepresentations(t *testing.T) {
	id := uuid.NewString()
	compact := strings.ReplaceAll(id, "-", "")
	if !canonicalStateUUID(id) || !canonicalStateUUID(compact) {
		t.Fatal("canonical PostgreSQL and MemStore UUID representations must both be accepted")
	}
	for _, value := range []string{strings.ToUpper(compact), strings.ToUpper(id), strings.ReplaceAll(id, "-", "x"), uuid.Nil.String()} {
		if canonicalStateUUID(value) {
			t.Fatalf("noncanonical or nil UUID accepted: %q", value)
		}
	}
}

func TestEnvironmentWorkloadServingReceiptRequiresExactGraphAndEveryGateway(t *testing.T) {
	graphID, sourceID, revisionID, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appA, appB := uuid.NewString(), uuid.NewString()
	deploymentA, deploymentB := uuid.NewString(), uuid.NewString()
	graph := EnvironmentWorkloadGraph{ID: graphID, SourceID: sourceID, RevisionID: revisionID, Generation: 4, IntentVersion: 9,
		PlanHash: "approved-plan", Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{
			{Resource: "workload/api", AppID: appA, CandidateDeploymentID: deploymentA, ExecutionMode: "request"},
			{Resource: "workload/catalog", AppID: appB, RetainedDeployments: []string{deploymentB}, ExecutionMode: "service"},
		}}
	gateways := []string{"edge-a", "edge-b"}
	receipt := EnvironmentWorkloadServingReceipt{
		GraphID: graphID, SourceID: sourceID, SourceGeneration: 4, IntentVersion: 9, RevisionID: revisionID,
		PlanHash: "approved-plan", ReleaseSetID: releaseID, ExpectedGateways: gateways,
		Routes: []EnvironmentWorkloadServingRoute{
			{AppID: appA, DeploymentID: deploymentA, Generation: 101, Cutover: true},
			{AppID: appB, DeploymentID: deploymentB, Generation: 102},
		}, Acknowledgements: map[int64][]string{101: gateways, 102: {"edge-a"}},
		Serving: true,
	}
	servedAt := time.Now().UTC()
	receipt.ServedAt = &servedAt
	targets := map[string]string{"workload/api": deploymentA, "workload/catalog": deploymentB}
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("one missing route acknowledgement satisfied serving")
	}
	receipt.Acknowledgements[102] = []string{"edge-a", "edge-b"}
	if !environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("complete exact graph acknowledgements did not satisfy serving")
	}
	receipt.Routes[0].Cutover = false
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("candidate route without cutover authorization satisfied serving")
	}
	receipt.Routes[0].Cutover = true
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, []string{"edge-a"}, targets) {
		t.Fatal("receipt from an old gateway roster satisfied current serving")
	}
	if environmentWorkloadServingReceiptMatches(receipt, graph, uuid.NewString(), gateways, targets) {
		t.Fatal("receipt from a different release set satisfied serving")
	}
	targets["workload/api"] = uuid.NewString()
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("receipt for a different deployment satisfied serving")
	}
}

func TestEnvironmentGraphProductionServingAdapterSupportsHTTPAndScopedPushWorkers(t *testing.T) {
	enabled := true
	workerBindingID, workerTriggerID := uuid.NewString(), uuid.NewString()
	httpBindingID, httpTriggerID := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name   string
		member EnvironmentWorkloadGraphMember
		want   bool
	}{
		{name: "request", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request"}, want: true},
		{name: "HTTP function with reviewed push binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request", Function: true,
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: httpBindingID, TriggerID: httpTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "http", Enabled: &enabled,
				}},
			}}, want: true},
		{name: "service", member: EnvironmentWorkloadGraphMember{ExecutionMode: "service"}, want: true},
		{name: "request with private service dependency", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request", ServiceBindingsConfigured: true}},
		{name: "worker", member: EnvironmentWorkloadGraphMember{ExecutionMode: "worker"}},
		{name: "worker with reviewed push binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "worker",
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: workerBindingID, TriggerID: workerTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}}, want: true},
		{name: "worker with private service dependency", member: EnvironmentWorkloadGraphMember{ExecutionMode: "worker", ServiceBindingsConfigured: true,
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: workerBindingID, TriggerID: workerTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}}},
		{name: "worker push missing trigger identity", member: EnvironmentWorkloadGraphMember{ExecutionMode: "worker",
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: workerBindingID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}}},
		{name: "worker pull binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "worker",
			QueueModes: map[string]string{"events": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: workerBindingID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}}, want: true},
		{name: "job", member: EnvironmentWorkloadGraphMember{ExecutionMode: "job"}},
		{name: "request with queue consumer", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request", QueueModes: map[string]string{"events": "push"}}},
		{name: "non-function HTTP queue binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request",
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: httpBindingID, TriggerID: httpTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "http", Enabled: &enabled,
				}},
			}}},
		{name: "HTTP function pull binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request", Function: true,
			QueueModes: map[string]string{"events": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: httpBindingID, TriggerID: httpTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "pull", WorkloadClass: "http", Enabled: &enabled,
				}},
			}}},
		{name: "HTTP function with worker-class binding", member: EnvironmentWorkloadGraphMember{ExecutionMode: "request", Function: true,
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: httpBindingID, TriggerID: httpTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			member := tc.member
			if member.Resource == "" {
				member.Resource = "workload/test"
			}
			if member.AppID == "" {
				member.AppID = uuid.NewString()
			}
			graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{member}}
			if got := environmentGraphSupportsProductionServing(graph); got != tc.want {
				t.Fatalf("production serving support = %t, want %t", got, tc.want)
			}
		})
	}
	if environmentGraphSupportsProductionServing(EnvironmentWorkloadGraph{}) {
		t.Fatal("empty graph vacuously satisfied the production serving adapter")
	}
}

func TestEnvironmentGraphProductionServingSupportsPinnedScopedServiceBindings(t *testing.T) {
	enabled := true
	callerID, targetID := uuid.NewString(), uuid.NewString()
	callerDeploymentID, targetDeploymentID := uuid.NewString(), uuid.NewString()
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{
		{Resource: "workload/api", AppID: callerID, CandidateDeploymentID: callerDeploymentID, ExecutionMode: api.ExecutionModeRequest,
			ServiceBindingsConfigured: true, ServiceBindings: map[string]EnvironmentScopedServiceBinding{
				"database": {Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: targetID},
			}},
		{Resource: "workload/database", AppID: targetID, CandidateDeploymentID: targetDeploymentID, ExecutionMode: api.ExecutionModeService},
	}}
	targets := map[string]string{"workload/api": callerDeploymentID, "workload/database": targetDeploymentID}
	if !environmentGraphSupportsProductionServing(graph) || !environmentGraphSupportsProductionServingForTargets(graph, targets) {
		t.Fatal("exact request-to-service binding was not supported")
	}
	workerGraph := clonePreparationGraph(graph)
	workerGraph.Members[0].ExecutionMode = api.ExecutionModeWorker
	workerGraph.Members[0].QueueModes = map[string]string{"events": "pull"}
	workerGraph.Members[0].QueueBindings = map[string]EnvironmentScopedQueueBinding{
		"events": {BindingID: uuid.NewString(), Contract: api.EnvironmentQueueBinding{
			QueueName: "events", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
		}},
	}
	if !environmentGraphSupportsProductionServingForTargets(workerGraph, targets) {
		t.Fatal("queue-backed worker with a pinned request/service dependency was not supported")
	}
	if blockers := environmentGraphProductionAdapterBlockers(workerGraph); slices.Contains(blockers, "environment_service_bound_worker_adapter_missing") ||
		slices.Contains(blockers, "environment_workload_production_execution_adapter_missing") {
		t.Fatalf("supported service-bound worker retained a production adapter blocker: %v", blockers)
	}
	workerGraph.Members[0].QueueModes = nil
	workerGraph.Members[0].QueueBindings = nil
	if environmentGraphSupportsProductionServingForTargets(workerGraph, targets) {
		t.Fatal("service-bound worker without a reviewed queue consumer satisfied serving")
	}

	mutations := []struct {
		name string
		edit func(*EnvironmentWorkloadGraph, map[string]string)
	}{
		{name: "target app changed", edit: func(graph *EnvironmentWorkloadGraph, _ map[string]string) {
			graph.Members[0].ServiceBindings["database"] = EnvironmentScopedServiceBinding{Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: uuid.NewString()}
		}},
		{name: "target mode unsupported", edit: func(graph *EnvironmentWorkloadGraph, _ map[string]string) {
			graph.Members[1].ExecutionMode = api.ExecutionModeWorker
		}},
		{name: "caller and target rollout identities diverge", edit: func(graph *EnvironmentWorkloadGraph, _ map[string]string) {
			graph.Members[1].CandidateDeploymentID = ""
			graph.Members[1].RetainedDeployments = []string{targetDeploymentID}
		}},
		{name: "env keys overlap", edit: func(graph *EnvironmentWorkloadGraph, _ map[string]string) {
			graph.Members[0].ServiceBindings["analytics"] = EnvironmentScopedServiceBinding{Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: graph.Members[1].AppID}
		}},
		{name: "caller deployment not in release", edit: func(_ *EnvironmentWorkloadGraph, targets map[string]string) {
			targets["workload/api"] = uuid.NewString()
		}},
		{name: "configured flag without frozen binding", edit: func(graph *EnvironmentWorkloadGraph, _ map[string]string) {
			graph.Members[0].ServiceBindings = nil
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			mutated := clonePreparationGraph(graph)
			mutatedTargets := maps.Clone(targets)
			tc.edit(&mutated, mutatedTargets)
			if environmentGraphSupportsProductionServingForTargets(mutated, mutatedTargets) {
				t.Fatal("unsupported or stale service-binding contract satisfied serving")
			}
		})
	}
}

func TestEnvironmentWorkloadServingRequiresRouteAndQueueProofForServiceBoundWorker(t *testing.T) {
	enabled := true
	workerApp, serviceApp := uuid.NewString(), uuid.NewString()
	workerDeployment, serviceDeployment := uuid.NewString(), uuid.NewString()
	bindingID, invocationID, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{
		{Resource: "workload/worker", AppID: workerApp, CandidateDeploymentID: workerDeployment, ExecutionMode: api.ExecutionModeWorker,
			ServiceBindingsConfigured: true, ServiceBindings: map[string]EnvironmentScopedServiceBinding{
				"api": {Workload: "api", EnvKey: "API_URL", TargetAppID: serviceApp},
			}, QueueModes: map[string]string{"events": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: bindingID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}},
		{Resource: "workload/api", AppID: serviceApp, CandidateDeploymentID: serviceDeployment, ExecutionMode: api.ExecutionModeService},
	}}
	targets := map[string]string{"workload/worker": workerDeployment, "workload/api": serviceDeployment}
	gateways := []string{"edge-a"}
	servedAt, acknowledgedAt := time.Now().UTC(), time.Now().UTC()
	receipt := EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: releaseID, ExpectedGateways: gateways,
		Routes:           []EnvironmentWorkloadServingRoute{{AppID: serviceApp, DeploymentID: serviceDeployment, Generation: 17, Cutover: true}},
		Acknowledgements: map[int64][]string{17: slices.Clone(gateways)}, Serving: true, ServedAt: &servedAt,
		QueueAcknowledgements: []EnvironmentWorkloadServingQueueAck{{Mode: "pull", AppID: workerApp, BindingID: bindingID,
			DeploymentID: workerDeployment, InvocationID: invocationID, Acknowledged: acknowledgedAt}}}
	if !environmentGraphSupportsProductionServingForTargets(graph, targets) ||
		!environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("service-bound worker with both exact serving proofs was rejected")
	}
	receipt.Acknowledgements[17] = nil
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("worker queue proof hid a missing target-service route acknowledgement")
	}
	receipt.Acknowledgements[17] = slices.Clone(gateways)
	receipt.QueueAcknowledgements = nil
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("target-service route proof hid the worker's missing queue acknowledgement")
	}
}

func TestEnvironmentWorkloadQueueServingReceiptRequiresExactActivePushConsumer(t *testing.T) {
	appID, deploymentID, bindingID, triggerID, invocationID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	enabled := true
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/worker", AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: api.ExecutionModeWorker,
		QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
			"events": {BindingID: bindingID, TriggerID: triggerID, Contract: api.EnvironmentQueueBinding{
				QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
			}},
		},
	}}}
	releaseID := uuid.NewString()
	servedAt, acknowledgedAt := time.Now().UTC(), time.Now().UTC()
	receipt := EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: releaseID, Serving: true, ServedAt: &servedAt,
		QueueAcknowledgements: []EnvironmentWorkloadServingQueueAck{{Mode: "push", AppID: appID, BindingID: bindingID, TriggerID: triggerID,
			DeploymentID: deploymentID, InvocationID: invocationID, Acknowledged: acknowledgedAt}}}
	targets := map[string]string{"workload/worker": deploymentID}
	if !environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("exact push consumer acknowledgement did not satisfy queue serving")
	}
	receipt.QueueAcknowledgements[0].TriggerID = uuid.NewString()
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("acknowledgement from a different trigger satisfied serving")
	}
	receipt.QueueAcknowledgements[0].TriggerID = triggerID
	receipt.QueueAcknowledgements[0].DeploymentID = uuid.NewString()
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("acknowledgement from a different deployment satisfied serving")
	}
	receipt.QueueAcknowledgements[0].DeploymentID = deploymentID
	targets["workload/worker"] = uuid.NewString()
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("acknowledgement from a superseded release target satisfied serving")
	}
}

func TestEnvironmentWorkloadQueueServingReceiptRequiresExactActivePullConsumer(t *testing.T) {
	appID, deploymentID, bindingID, invocationID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	enabled := true
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/worker", AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: api.ExecutionModeWorker,
		QueueModes: map[string]string{"events": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
			"events": {BindingID: bindingID, Contract: api.EnvironmentQueueBinding{
				QueueName: "events", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
			}},
		},
	}}}
	releaseID := uuid.NewString()
	servedAt, acknowledgedAt := time.Now().UTC(), time.Now().UTC()
	receipt := EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: releaseID, Serving: true, ServedAt: &servedAt,
		QueueAcknowledgements: []EnvironmentWorkloadServingQueueAck{{Mode: "pull", AppID: appID, BindingID: bindingID,
			DeploymentID: deploymentID, InvocationID: invocationID, Acknowledged: acknowledgedAt}}}
	targets := map[string]string{"workload/worker": deploymentID}
	if !environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("exact pull consumer acknowledgement did not satisfy queue serving")
	}
	receipt.QueueAcknowledgements[0].Mode = "push"
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("push acknowledgement satisfied a pull queue binding")
	}
	receipt.QueueAcknowledgements[0].Mode = "pull"
	receipt.QueueAcknowledgements[0].TriggerID = uuid.NewString()
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, nil, targets) {
		t.Fatal("pull acknowledgement with a trigger identity satisfied serving")
	}
}

func TestMemStoreGitOpsPullQueueCompletionFencesClaimAttempt(t *testing.T) {
	store := NewMemStore()
	projectID, accountID, environmentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	sourceID, revisionID, graphID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, bindingID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	invocationID, releaseID := uuid.NewString(), uuid.NewString()
	enabled := true
	source := EnvironmentGitSource{ID: sourceID, AccountID: accountID, ProjectID: projectID, EnvironmentID: environmentID,
		EnvironmentSlug: "staging", Spec: api.EnvironmentGitSourceSpec{Mode: "enforce"}, Generation: 3, IntentVersion: 5,
		ApprovedRevisionID: revisionID}
	graph := EnvironmentWorkloadGraph{ID: graphID, SourceID: sourceID, RevisionID: revisionID, Generation: source.Generation,
		IntentVersion: source.IntentVersion, Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
			Resource: "workload/processor", AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: api.ExecutionModeWorker,
			QueueModes: map[string]string{"events": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: bindingID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}},
		}}
	store.mu.Lock()
	store.environmentGitOps = map[string]*environmentGitOpsMemory{}
	store.environmentWorkloadServingReceipts = map[string]EnvironmentWorkloadServingReceipt{}
	store.environmentGitOps[sourceID] = &environmentGitOpsMemory{source: source,
		graphs: map[string]EnvironmentWorkloadGraph{"current": graph}}
	store.activeProjectReleaseSets[releaseKey(projectID, "staging")] = releaseID
	store.projectReleaseSets[releaseID] = ProjectReleaseSet{ID: releaseID, ProjectID: projectID, EnvironmentSlug: "staging",
		Active: true, Members: []ProjectReleaseMember{{AppID: appID, DeploymentID: deploymentID}}}
	store.deployments[deploymentID] = Deployment{ID: deploymentID, AppID: appID, Scope: "staging", Status: DeployLive}
	store.invocations[invocationID] = Invocation{ID: invocationID, AppID: appID, AccountID: accountID,
		Source: InvocationQueue, QueueBindingID: bindingID, DeploymentScope: "staging", State: InvocationDispatching,
		Attempts: 2, QuotaReserved: true}
	consumers, ok := environmentGraphServingQueueConsumers(graph, map[string]string{"workload/processor": deploymentID})
	if !ok || len(consumers) != 1 {
		store.mu.Unlock()
		t.Fatal("pull graph did not produce exactly one serving consumer")
	}
	store.environmentWorkloadServingReceipts[graphID] = EnvironmentWorkloadServingReceipt{GraphID: graphID, ReleaseSetID: releaseID,
		ExpectedQueueConsumers: consumers, Acknowledgements: map[int64][]string{}}
	store.mu.Unlock()
	if err := store.CompleteEnvironmentGitOpsPullQueueDelivery(t.Context(), invocationID, 1, uuid.NewString(), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale attempt completion error = %v, want ErrNotFound", err)
	}
	current, err := store.InvocationByID(t.Context(), invocationID)
	if err != nil || current.State != InvocationDispatching || !current.QuotaReserved {
		t.Fatalf("stale attempt changed invocation: %+v, err=%v", current, err)
	}
	result := []byte(`{"ok":true}`)
	if err := store.CompleteEnvironmentGitOpsPullQueueDelivery(t.Context(), invocationID, 2, deploymentID, result); err != nil {
		t.Fatalf("current attempt completion: %v", err)
	}
	current, err = store.InvocationByID(t.Context(), invocationID)
	if err != nil || current.State != InvocationCompleted || current.QuotaReserved || string(current.Result) != string(result) {
		t.Fatalf("current attempt did not complete invocation: %+v, err=%v", current, err)
	}
	store.mu.Lock()
	acknowledgement, acknowledged := store.environmentWorkloadQueueServingAcks[graphID][bindingID]
	receipt := store.environmentWorkloadServingReceipts[graphID]
	store.mu.Unlock()
	if !acknowledged || acknowledgement.Mode != "pull" || acknowledgement.TriggerID != "" ||
		acknowledgement.InvocationID != invocationID || acknowledgement.DeploymentID != deploymentID || !receipt.Serving {
		t.Fatalf("pull completion did not atomically record its active serving proof: ack=%+v receipt=%+v", acknowledgement, receipt)
	}
}

func TestMixedHTTPAndPushGraphRequiresBothServingReceipts(t *testing.T) {
	apiApp, workerApp := uuid.NewString(), uuid.NewString()
	functionApp := uuid.NewString()
	apiDeployment, workerDeployment, functionDeployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	bindingID, triggerID, invocationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	functionBindingID, functionTriggerID, functionInvocationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	enabled := true
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{
		{Resource: "workload/api", AppID: apiApp, CandidateDeploymentID: apiDeployment, ExecutionMode: api.ExecutionModeRequest},
		{Resource: "workload/function", AppID: functionApp, CandidateDeploymentID: functionDeployment, ExecutionMode: api.ExecutionModeRequest, Function: true,
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: functionBindingID, TriggerID: functionTriggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "http", Enabled: &enabled,
				}},
			}},
		{Resource: "workload/worker", AppID: workerApp, CandidateDeploymentID: workerDeployment, ExecutionMode: api.ExecutionModeWorker,
			QueueModes: map[string]string{"events": "push"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"events": {BindingID: bindingID, TriggerID: triggerID, Contract: api.EnvironmentQueueBinding{
					QueueName: "events", Mode: "push", WorkloadClass: "worker", Enabled: &enabled,
				}},
			}},
	}}
	gateways := []string{"edge-a", "edge-b"}
	releaseID, servedAt := uuid.NewString(), time.Now().UTC()
	receipt := EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: releaseID, ExpectedGateways: gateways,
		Routes: []EnvironmentWorkloadServingRoute{
			{AppID: apiApp, DeploymentID: apiDeployment, Generation: 77, Cutover: true},
			{AppID: functionApp, DeploymentID: functionDeployment, Generation: 78, Cutover: true},
		},
		Acknowledgements: map[int64][]string{77: slices.Clone(gateways), 78: slices.Clone(gateways)}, Serving: true, ServedAt: &servedAt,
		QueueAcknowledgements: []EnvironmentWorkloadServingQueueAck{
			{Mode: "push", AppID: functionApp, BindingID: functionBindingID, TriggerID: functionTriggerID,
				DeploymentID: functionDeployment, InvocationID: functionInvocationID, Acknowledged: time.Now().UTC()},
			{Mode: "push", AppID: workerApp, BindingID: bindingID, TriggerID: triggerID,
				DeploymentID: workerDeployment, InvocationID: invocationID, Acknowledged: time.Now().UTC()},
		}}
	targets := map[string]string{"workload/api": apiDeployment, "workload/function": functionDeployment, "workload/worker": workerDeployment}
	if !environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("complete route, HTTP function, and worker queue receipts did not satisfy mixed graph serving")
	}
	receipt.Acknowledgements[77] = []string{"edge-a"}
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("queue acknowledgement hid a missing route acknowledgement")
	}
	receipt.Acknowledgements[77] = slices.Clone(gateways)
	receipt.QueueAcknowledgements = nil
	if environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, targets) {
		t.Fatal("route acknowledgements hid missing queue acknowledgements")
	}
}

func TestMemStoreServingWeightsRequireOneExactLiveTarget(t *testing.T) {
	appID, targetID, oldID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	source := EnvironmentGitSource{EnvironmentSlug: "production"}
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/api", AppID: appID, CandidateDeploymentID: targetID, ExecutionMode: "request",
	}}}
	store := &MemStore{deployments: map[string]Deployment{
		targetID: {ID: targetID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 100},
		oldID:    {ID: oldID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 0},
	}}
	candidateRoute := []EnvironmentWorkloadServingRoute{{AppID: appID, DeploymentID: targetID, Cutover: true}}
	targets := map[string]string{"workload/api": targetID}
	if !store.environmentWorkloadServingWeightsMatchLocked(source, graph, targets) {
		t.Fatal("100% exact target with dark predecessor was not serving")
	}
	if !store.environmentWorkloadServingCutoverWeightsMatchLocked(source, candidateRoute) {
		t.Fatal("converged candidate traffic did not match its acknowledged generation")
	}
	old := store.deployments[oldID]
	old.TrafficPercent = 1
	store.deployments[oldID] = old
	if store.environmentWorkloadServingWeightsMatchLocked(source, graph, targets) {
		t.Fatal("stale weighted predecessor did not block serving evidence")
	}
	if store.environmentWorkloadServingCutoverWeightsMatchLocked(source, candidateRoute) {
		t.Fatal("candidate traffic drift did not invalidate prior route acknowledgements")
	}
	old.TrafficPercent = 0
	store.deployments[oldID] = old
	target := store.deployments[targetID]
	target.TrafficPercent = 0
	store.deployments[targetID] = target
	if store.environmentWorkloadServingWeightsMatchLocked(source, graph, targets) {
		t.Fatal("dark target did not block serving evidence")
	}

	retainedGraph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/api", AppID: appID, RetainedDeployments: []string{oldID}, ExecutionMode: "request",
	}}}
	store.deployments[oldID] = Deployment{ID: oldID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 30}
	store.deployments[targetID] = Deployment{ID: targetID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 70}
	retainedRoute := []EnvironmentWorkloadServingRoute{{AppID: appID, DeploymentID: oldID}}
	if !store.environmentWorkloadServingCutoverWeightsMatchLocked(source, retainedRoute) {
		t.Fatal("retained traffic policy incorrectly invalidated candidate cutover evidence")
	}
	if !store.environmentWorkloadServingWeightsMatchLocked(source, retainedGraph, map[string]string{"workload/api": oldID}) {
		t.Fatal("live retained target with an existing traffic share was not serving")
	}
	store.deployments[oldID] = Deployment{ID: oldID, AppID: appID, Scope: "production", Status: DeployLive, TrafficPercent: 0}
	if store.environmentWorkloadServingWeightsMatchLocked(source, retainedGraph, map[string]string{"workload/api": oldID}) {
		t.Fatal("retained target without traffic was reported serving")
	}
}
