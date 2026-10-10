// adr: 568 — durable dispatch consumes current owner authority, not notifications.
package sched

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func passingQualificationSmokeEvidence(requests []state.EnvironmentWorkloadQualificationRequest, instances map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
	requestByResource := make(map[string]state.EnvironmentWorkloadQualificationRequest, len(requests))
	for _, request := range requests {
		requestByResource[request.Resource] = request
	}
	resources := make([]string, 0, len(instances))
	for resource := range instances {
		resources = append(resources, resource)
	}
	slices.Sort(resources)
	evidence := make([]state.EnvironmentQualificationSmokeEvidence, 0, len(resources))
	for _, resource := range resources {
		request, ok := requestByResource[resource]
		if !ok {
			return nil, fmt.Errorf("missing qualification request for %s", resource)
		}
		policy, policySHA256, err := state.EnvironmentQualificationSmokePolicyFor(request)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, state.EnvironmentQualificationSmokeEvidence{Resource: resource, InstanceID: instances[resource].ID,
			PolicyID: policy.ID, PolicySHA256: policySHA256, ResultSHA256: strings.Repeat("b", 64), Passed: true})
	}
	return evidence, nil
}

// Interpose only after the real durable scan; claim/admission/runtime fences
// still use the real store. This models an old page or simultaneous scanners.
type delayedQualificationDispatchStore struct {
	*state.MemStore
	afterList func()
}

func (s *delayedQualificationDispatchStore) ListEnvironmentWorkloadQualificationsForDispatch(ctx context.Context, nodeID, cursor string, limit int) ([]string, error) {
	ids, err := s.MemStore.ListEnvironmentWorkloadQualificationsForDispatch(ctx, nodeID, cursor, limit)
	if err == nil && s.afterList != nil {
		s.afterList()
	}
	return ids, err
}

// One physical generation and stable receipt per original frame. Sharing the
// legacy one-attempt fake's receipt across candidates must be rejected by state.
func newDispatchQualificationVMM(vmm *fakeVMM) *qualificationRuntimeVMM {
	v := newQualificationRuntimeVMM(vmm)
	v.changeEvidence = func(evidence *EnvironmentQualificationRetirementEvidence) {
		evidence.Retirement.ReceiptID = uuid.NewSHA1(uuid.Nil, []byte(evidence.Execution.InstanceID+"-retirement")).String()
		evidence.Retirement.NativeGeneration = uuid.MustParse(evidence.Execution.InstanceID).String()
	}
	return v
}

type qualificationGraphHTTPProbeVMM struct {
	*qualificationRestoreRuntimeVMM
}

func (v *qualificationGraphHTTPProbeVMM) ProbeEnvironmentQualificationHTTP(_ context.Context,
	_ state.EnvironmentQualificationExecution, request state.EnvironmentWorkloadQualificationRequest,
	_ state.Instance) (EnvironmentQualificationHTTPProbeResult, error) {
	if request.ExecutionMode != api.ExecutionModeRequest || request.Resource != "workload/api" {
		return EnvironmentQualificationHTTPProbeResult{}, state.ErrConflict
	}
	return EnvironmentQualificationHTTPProbeResult{StatusCode: 204, Completed: true}, nil
}

func TestEnvironmentQualificationDispatchPagesRetireBeforeAdvancingWithoutActivation(t *testing.T) {
	store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeRequest)
	v, notif := newDispatchQualificationVMM(&fakeVMM{}), &fakeNotifier{}
	e := newEngine(t, store, v, notif, "test-fc")
	ids, err := store.ListEnvironmentWorkloadQualificationsForDispatch(t.Context(), e.defaultLocalNodeID, "", 3)
	if err != nil || len(ids) != 3 {
		t.Fatal("fixture lacks complete queued cohort", err)
	}
	injected := errors.New("isolated check failed")
	visits := []string{}
	claimed := map[string]state.EnvironmentWorkloadQualificationRequest{}
	visit := func(ctx context.Context, request state.EnvironmentWorkloadQualificationRequest, ins state.Instance) error {
		visits = append(visits, request.ID)
		claimed[request.ID] = request
		if ins.ID != request.ReservedInstanceID || ins.State != string(state.StateRunning) || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
			return errors.New("runtime visitor has wrong reservation")
		}
		deadline, ok := ctx.Deadline()
		if !ok || request.LeaseUntil == nil || deadline.After(*request.LeaseUntil) {
			return errors.New("runtime visitor exceeds claim lease")
		}
		if request.ID == ids[0] {
			return injected
		}
		return nil
	}
	page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", "", 2, visit)
	if !errors.Is(err, injected) || page.Examined != 2 || page.Claimed != 2 || page.Executed != 1 || page.Skipped != 0 || page.NextCursor != ids[1] {
		t.Fatal("failed member starved neighbor or lost page progress", page, err)
	}
	for _, id := range ids[:2] {
		assertQualificationRetired(t, store, e, claimed[id], v, 2)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", page.NextCursor, 2, visit)
	if err != nil || page.Examined != 1 || page.Executed != 1 || page.NextCursor != "" || !reflect.DeepEqual(visits, ids) {
		t.Fatal("page cursor lost or repeated committed work", page, visits, err)
	}
	assertQualificationRetired(t, store, e, claimed[ids[2]], v, 3)
	restarted := newEngine(t, store, v, notif, "test-fc")
	page, err = restarted.DispatchEnvironmentWorkloadQualifications(t.Context(), restarted.defaultLocalNodeID, "restarted", "", 2, visit)
	if err != nil || page.Examined != 0 || v.coldBoots != 3 || v.snapshots != 0 || notif.count(db.NotifyDeploymentReady) != 0 {
		t.Fatal("restart replayed active lease or claimed activation evidence", page, err)
	}
	for _, request := range requests {
		if err := store.MarkDeploymentLive(t.Context(), request.DeploymentID); err == nil {
			t.Fatal("dispatch lifted candidate hold")
		}
		dep, err := store.DeploymentByID(t.Context(), request.DeploymentID)
		if err != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeploySnapshotting {
			t.Fatal("dispatch changed held deployment identity or phase", err)
		}
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" {
		t.Fatal("successful visitor became complete environment convergence", err)
	}
}

func TestEnvironmentQualificationGraphDispatchClaimsExecutesAndRetiresPrivateCohort(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	var callerID string
	for _, request := range requests {
		if request.Resource == "workload/api" {
			callerID = request.AppID
		}
	}
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: callerID}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	visited := false
	claimedInstances := map[string]state.Instance{}
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "graph-scheduler", "", 2,
		func(ctx context.Context, instances map[string]state.Instance) error {
			visited = true
			claimedInstances = instances
			if len(instances) != 2 || fmt.Sprint(v.boots) != "[workload/api2 workload/api]" || len(v.retired) != 0 {
				t.Fatal("binding graph did not execute dependency first", instances, v.boots, v.retired)
			}
			for resource, instance := range instances {
				if instance.NodeID != e.defaultLocalNodeID {
					return fmt.Errorf("qualification graph member %s escaped its dispatch node", resource)
				}
			}
			caller := instances["workload/api"]
			route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
				NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend"})
			if err != nil || route.Target.InstanceID != instances["workload/api2"].ID || route.Port != 8087 {
				return fmt.Errorf("private graph route: %+v: %w", route, err)
			}
			found := false
			for _, entry := range v.callerSpec.APIEnv {
				if entry.Key == "BACKEND_URL" {
					found = entry.Value == "http://10.100.0.1:10081"+api.EnvironmentQualificationServicePrefix+requests[0].GraphID+"/backend"
				}
			}
			if !found {
				return errors.New("private service URL was not delivered to the reviewed caller")
			}
			return nil
		})
	if err != nil || !visited || page.Examined != 1 || page.Claimed != 1 || page.Executed != 1 || page.Skipped != 0 || page.NextCursor != "" {
		t.Fatalf("binding graph dispatch: %+v %v", page, err)
	}
	if fmt.Sprint(v.retired) != "[workload/api workload/api2]" || e.ledger.ResidentRAM() != 0 {
		t.Fatal("binding graph runtime was not retired in reverse dependency order", v.retired)
	}
	if fmt.Sprint(v.captures) != "[workload/api2 workload/api]" {
		t.Fatalf("graph capture did not run for every member in dependency order: %v", v.captures)
	}
	for _, request := range requests {
		dep, err := store.DeploymentByID(t.Context(), request.DeploymentID)
		attempt, ok := claimedInstances[request.Resource]
		ins, instanceErr := store.InstanceByID(t.Context(), attempt.ID)
		if !ok || err != nil || instanceErr != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeploySnapshotting || ins.State != string(state.StateStopped) {
			t.Fatal("qualification graph execution activated or leaked a member", dep, ins, err, instanceErr)
		}
		if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), attempt.ID); err != nil {
			t.Fatalf("successful graph visit did not persist capture for %s: %v", request.Resource, err)
		}
	}
}

func TestEnvironmentQualificationGraphDispatchRunsJobBeforeServiceRuntimes(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeJob, api.ExecutionModeService)
	var jobRequest, serviceRequest state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		switch request.ExecutionMode {
		case api.ExecutionModeJob:
			jobRequest = request
		case api.ExecutionModeService:
			serviceRequest = request
		}
	}
	if jobRequest.ID == "" || serviceRequest.ID == "" {
		t.Fatalf("fixture omitted mixed graph members: %+v", requests)
	}

	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	jobBoot := &qualificationJobBootStub{}
	jobExit := &qualificationJobExitStub{result: JobExitResult{ExitCode: 0, ErrorClass: "succeeded"}}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithJobVmmClient(jobBoot).WithJobExitWaiter(jobExit)
	visited := false
	var serviceInstances map[string]state.Instance
	var jobReceipt state.EnvironmentQualificationJobSmokeReceipt
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "mixed-graph-scheduler", "", 1,
		func(ctx context.Context, instances map[string]state.Instance) error {
			visited = true
			serviceInstances = instances
			serviceInstance := instances[serviceRequest.Resource]
			if len(instances) != 1 || serviceInstance.ID == "" ||
				len(v.boots) != 1 || v.boots[0] != serviceRequest.Resource || len(v.captures) != 0 || len(v.retired) != 1 || v.retired[0] != jobRequest.Resource {
				return fmt.Errorf("job and service execution overlapped or selected the wrong runtime cohort: instances=%v boots=%v captures=%v retired=%v",
					instances, v.boots, v.captures, v.retired)
			}
			var err error
			jobReceipt, err = store.EnvironmentQualificationJobSmokeReceipt(ctx, jobRequest.ID, jobRequest.Attempt+1)
			if err != nil || jobReceipt.GraphID != jobRequest.GraphID || jobReceipt.InstanceID == "" || jobReceipt.Resource != jobRequest.Resource {
				return errors.Join(fmt.Errorf("service runtime started without a matching retired job receipt: receipt=%+v", jobReceipt), err)
			}
			jobInstance, err := store.InstanceByID(ctx, jobReceipt.InstanceID)
			if err != nil || jobInstance.State != string(state.StateStopped) {
				return errors.Join(fmt.Errorf("service runtime started before the job instance retired: instance=%+v", jobInstance), err)
			}
			return nil
		})
	if err != nil || !visited || page.Examined != 1 || page.Claimed != 1 || page.Executed != 1 || page.Skipped != 0 || page.NextCursor != jobRequest.GraphID {
		t.Fatalf("mixed job/service graph dispatch: page=%+v visited=%t err=%v", page, visited, err)
	}
	if jobReceipt.InstanceID == "" || jobBoot.spec.InstanceID != jobReceipt.InstanceID || jobExit.spec.InstanceID != jobReceipt.InstanceID ||
		jobBoot.spec.QualificationExecution == nil || jobBoot.spec.QualificationExecution.RequestID != jobRequest.ID {
		t.Fatalf("job did not use its reviewed private attempt: boot=%+v exit=%+v", jobBoot.spec, jobExit.spec)
	}
	if fmt.Sprint(v.retired) != "["+jobRequest.Resource+" "+serviceRequest.Resource+"]" ||
		fmt.Sprint(v.captures) != "["+serviceRequest.Resource+"]" || e.ledger.ResidentRAM() != 0 {
		t.Fatalf("mixed graph retirement/capture order or ledger: retired=%v captures=%v ram=%d", v.retired, v.captures, e.ledger.ResidentRAM())
	}
	for _, request := range requests {
		dep, err := store.DeploymentByID(t.Context(), request.DeploymentID)
		instanceID := serviceInstances[request.Resource].ID
		if request.ExecutionMode == api.ExecutionModeJob {
			instanceID = jobReceipt.InstanceID
		}
		ins, instanceErr := store.InstanceByID(t.Context(), instanceID)
		if err != nil || instanceErr != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeploySnapshotting || ins.State != string(state.StateStopped) {
			t.Fatalf("mixed graph activated or leaked a member: request=%s deployment=%+v instance=%+v err=%v instanceErr=%v",
				request.ID, dep, ins, err, instanceErr)
		}
		if request.ID == jobRequest.ID {
			if receipt, err := store.EnvironmentQualificationJobSmokeReceipt(t.Context(), request.ID, request.Attempt+1); err != nil || receipt.InstanceID != instanceID {
				t.Fatalf("job pass receipt missing after retirement: %v", err)
			}
		} else if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), instanceID); err != nil {
			t.Fatalf("service graph capture missing: %v", err)
		}
	}
}

func TestEnvironmentQualificationGraphDispatchHoldsServiceBoundJobUntilRouteIsReady(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}},
		api.ExecutionModeJob, api.ExecutionModeService)
	var jobRequest, serviceRequest state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		if request.ExecutionMode == api.ExecutionModeJob {
			jobRequest = request
		} else {
			serviceRequest = request
		}
	}
	if jobRequest.ID == "" || serviceRequest.ID == "" {
		t.Fatalf("fixture omitted mixed graph members: %+v", requests)
	}

	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	jobBoot := &qualificationJobBootStub{}
	jobBoot.releaseCheck = func(ctx context.Context, spec JobStartSpec) error {
		route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
			NodeID: spec.NodeID, HostIP: "10.0.0.2", GraphID: jobRequest.GraphID, Binding: "backend",
		})
		if err != nil || route.Caller.InstanceID != spec.InstanceID || route.Caller.RequestID != jobRequest.ID ||
			route.Target.RequestID != serviceRequest.ID || route.Target.Resource != serviceRequest.Resource {
			return fmt.Errorf("held Job was released before its exact private route was ready: route=%+v err=%w", route, errors.Join(err, state.ErrConflict))
		}
		return nil
	}
	jobExit := &qualificationJobExitStub{result: JobExitResult{ExitCode: 0, ErrorClass: "succeeded"}}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithJobVmmClient(jobBoot).WithJobExitWaiter(jobExit).
		WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "job-service-graph-scheduler", "", 1,
		func(context.Context, map[string]state.Instance) error { return nil })
	if err != nil || page.Examined != 1 || page.Claimed != 1 || page.Executed != 1 || jobBoot.releaseCalls != 1 {
		t.Fatalf("service-bound held Job dispatch: page=%+v releases=%d err=%v", page, jobBoot.releaseCalls, err)
	}
	wantURL := "http://10.100.0.1:10081" + api.EnvironmentQualificationServicePrefix + jobRequest.GraphID + "/backend"
	if !jobBoot.spec.StartHeld || jobBoot.spec.Env["BACKEND_URL"] != wantURL || len(v.boots) != 1 || v.boots[0] != serviceRequest.Resource {
		t.Fatalf("service-bound Job boot did not use its ready dependency and reviewed alias: job=%+v boots=%v", jobBoot.spec, v.boots)
	}
	if _, err := store.EnvironmentQualificationJobSmokeReceipt(t.Context(), jobRequest.ID, jobRequest.Attempt+1); err != nil {
		t.Fatalf("service-bound Job qualification receipt missing: %v", err)
	}
}

func TestEnvironmentQualificationGraphDispatchStopsAfterFailedJob(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeJob, api.ExecutionModeService)
	var jobRequest, serviceRequest state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		if request.ExecutionMode == api.ExecutionModeJob {
			jobRequest = request
		} else {
			serviceRequest = request
		}
	}
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	jobExit := &qualificationJobExitStub{result: JobExitResult{ExitCode: 1, ErrorClass: "task_failed"}}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithJobVmmClient(&qualificationJobBootStub{}).WithJobExitWaiter(jobExit)
	visited := false
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "failed-job-scheduler", "", 1,
		func(context.Context, map[string]state.Instance) error {
			visited = true
			return nil
		})
	if err == nil || visited || page.Examined != 1 || page.Claimed != 1 || page.Executed != 0 || page.Skipped != 0 ||
		len(v.boots) != 0 || len(v.captures) != 0 || len(v.retired) != 1 || v.retired[0] != jobRequest.Resource || e.ledger.ResidentRAM() != 0 {
		t.Fatalf("failed job did not stop mixed graph before service effects: page=%+v visited=%t boots=%v captures=%v retired=%v ram=%d err=%v",
			page, visited, v.boots, v.captures, v.retired, e.ledger.ResidentRAM(), err)
	}
	if _, err := store.EnvironmentQualificationJobSmokeReceipt(t.Context(), jobRequest.ID, jobRequest.Attempt+1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed job created a pass receipt: %v", err)
	}
	if _, err := store.EnvironmentQualificationExecution(t.Context(), serviceRequest.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("service member was admitted after failed job: %v", err)
	}
}

func TestEnvironmentQualificationGraphRejectsServiceBindingToJobBeforeBoot(t *testing.T) {
	graphID := uuid.NewString()
	requests := []state.EnvironmentWorkloadQualificationRequest{
		{ID: uuid.NewString(), GraphID: graphID, AppID: uuid.NewString(), Resource: "workload/api", ExecutionMode: api.ExecutionModeService,
			FrozenInputs: state.EnvironmentWorkloadRuntime{ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{
				"backend": {Workload: "api2", EnvKey: "BACKEND_URL", TargetAppID: "job-app"},
			}}},
		{ID: uuid.NewString(), GraphID: graphID, AppID: "job-app", Resource: "workload/api2", ExecutionMode: api.ExecutionModeJob,
			FrozenInputs: state.EnvironmentWorkloadRuntime{}},
	}
	if _, err := qualificationGraphExecutionOrder(requests); !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("service binding to a job target passed graph preflight: %v", err)
	}
}

func TestEnvironmentQualificationHTTPGraphDispatchSupportsUnboundWorkloadsAndStopsAfterReceipt(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	vmm := &qualificationGraphHTTPProbeVMM{qualificationRestoreRuntimeVMM: &qualificationRestoreRuntimeVMM{
		qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}),
	}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(t.Context(), engine.defaultLocalNodeID, "http-graph-scheduler", "", 1)
	if err != nil || page.Examined != 1 || page.Claimed != 1 || page.Executed != 1 || page.Restored != 1 || page.Skipped != 0 {
		t.Fatalf("unbound HTTP graph dispatch: %+v %v", page, err)
	}
	receipt, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), requests[0].ID, 1)
	if err != nil || receipt.GraphID != requests[0].GraphID || receipt.Resource != requests[0].Resource {
		t.Fatalf("unbound HTTP graph smoke receipt: %+v %v", receipt, err)
	}
	graphDiscovery := state.EnvironmentGitOpsQualificationGraphDispatchStore(store)
	if ids, err := graphDiscovery.ListEnvironmentWorkloadQualificationGraphsForDispatch(t.Context(), engine.defaultLocalNodeID, "", 1); err != nil || len(ids) != 0 {
		t.Fatalf("completed graph remained discoverable: %v %v", ids, err)
	}
	discovery := state.EnvironmentGitOpsQualificationDiscoveryStore(store)
	if ids, err := discovery.ListEnvironmentWorkloadQualificationsForDispatch(t.Context(), engine.defaultLocalNodeID, "", 1); err != nil || len(ids) != 0 {
		t.Fatalf("completed member remained discoverable: %v %v", ids, err)
	}
	page, err = engine.DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(t.Context(), engine.defaultLocalNodeID, "http-graph-scheduler", "", 1)
	if err != nil || page.Examined != 0 || page.Claimed != 0 || vmm.boots != 1 || vmm.restores != 1 {
		t.Fatalf("completed graph was executed again: %+v boots=%d restores=%d err=%v", page, vmm.boots, vmm.restores, err)
	}
}

type qualificationWrongRouteStore struct {
	*state.MemStore
	wrongAt    int32
	routeCalls atomic.Int32
}

func (s *qualificationWrongRouteStore) ResolveEnvironmentQualificationService(ctx context.Context,
	request state.EnvironmentQualificationServiceRequest) (state.EnvironmentQualificationServiceRoute, error) {
	route, err := s.MemStore.ResolveEnvironmentQualificationService(ctx, request)
	if err == nil && s.routeCalls.Add(1) == s.wrongAt {
		route.Target.InstanceID = uuid.NewString()
	}
	return route, err
}

func TestEnvironmentQualificationGraphDispatchRejectsMismatchedScopedRoute(t *testing.T) {
	for _, wrongAt := range []int32{1, 2} {
		t.Run(fmt.Sprintf("route_%d", wrongAt), func(t *testing.T) {
			store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
				map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
			wrapped := &qualificationWrongRouteStore{MemStore: store, wrongAt: wrongAt}
			engine := newEngine(t, wrapped, &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})},
				&fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
				func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
			var smokeVisited bool
			page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID,
				"graph-scheduler", "", 1, func(context.Context, map[string]state.Instance) error { return nil },
				func(ctx context.Context, instances map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
					smokeVisited = true
					return engine.ProbeEnvironmentQualificationGraph(ctx, instances)
				})
			if !errors.Is(err, state.ErrConflict) || page.Claimed != 1 || page.Executed != 0 || page.Restored != 0 ||
				wrapped.routeCalls.Load() != wrongAt || smokeVisited || engine.ledger.ResidentRAM() != 0 {
				t.Fatalf("mismatched scoped route reached capture/smoke completion: page=%+v route_calls=%d smoke_visited=%t ram=%d err=%v",
					page, wrapped.routeCalls.Load(), smokeVisited, engine.ledger.ResidentRAM(), err)
			}
			for _, request := range requests {
				if _, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, request.Attempt+1); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("mismatched scoped route persisted smoke evidence: %v", err)
				}
			}
			if wrongAt == 1 {
				for _, request := range requests {
					if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), request.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("mismatched source route captured a graph member: %v", err)
					}
				}
			}
		})
	}
}

func TestEnvironmentQualificationGraphDispatchRunsRestoreSmokeAfterRetiringCaptureCohort(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	var source, restored map[string]state.Instance
	vmm.beforeRestore = func(frame state.EnvironmentQualificationExecution) error {
		if frame.CaptureInstanceID == "" || frame.InstanceID == frame.CaptureInstanceID {
			return fmt.Errorf("restore did not receive a distinct capture identity: %w", state.ErrConflict)
		}
		for _, request := range requests {
			captured, exists := source[request.Resource]
			if !exists {
				return fmt.Errorf("source runtime for %s is missing: %w", request.Resource, state.ErrConflict)
			}
			status, err := store.EnvironmentQualificationExecution(t.Context(), captured.ID)
			if err != nil || status.CaptureInstanceID != "" || status.RetiredAt == nil || status.Retirement == nil || status.Retirement.Kind != state.QualificationNativeRetired {
				return fmt.Errorf("capture cohort still owns a runtime when restore started: %+v %w", status, errors.Join(err, state.ErrConflict))
			}
			if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), captured.ID); err != nil {
				return fmt.Errorf("restore started without the source capture receipt: %w", err)
			}
		}
		return nil
	}

	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(_ context.Context, instances map[string]state.Instance) error {
			source = instances
			return nil
		}, func(ctx context.Context, instances map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
			restored = instances
			if len(instances) != len(requests) {
				return nil, fmt.Errorf("restored cohort has %d members, want %d", len(instances), len(requests))
			}
			caller, dependency := instances["workload/api"], instances["workload/api2"]
			route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
				NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend",
			})
			if err != nil || route.Caller.InstanceID != caller.ID || route.Target.InstanceID != dependency.ID || route.Port != 8087 {
				return nil, fmt.Errorf("restored smoke binding did not resolve to the new dependency: %+v %w", route, errors.Join(err, state.ErrConflict))
			}
			return passingQualificationSmokeEvidence(requests, instances)
		})
	if err != nil || page.Examined != 1 || page.Claimed != 1 || page.Executed != 1 || page.Restored != 1 || page.Skipped != 0 {
		t.Fatalf("capture/retire/restore/smoke dispatch: page=%+v err=%v", page, err)
	}
	if len(source) != len(requests) || len(restored) != len(requests) || vmm.boots != len(requests) || vmm.restores != len(requests) {
		t.Fatalf("graph phases did not run complete cohorts: source=%d restored=%d boots=%d restores=%d", len(source), len(restored), vmm.boots, vmm.restores)
	}
	if len(vmm.retiredArtifacts) != len(requests) {
		t.Fatalf("successful restore smoke did not retire every capture cohort: %v", vmm.retiredArtifacts)
	}
	for _, request := range requests {
		capture, target := source[request.Resource], restored[request.Resource]
		if capture.ID == target.ID || capture.WakeID == target.WakeID || capture.NodeID != target.NodeID {
			t.Fatalf("restore did not use a distinct same-node runtime for %s: capture=%+v target=%+v", request.Resource, capture, target)
		}
		retiredCapture, err := store.InstanceByID(t.Context(), capture.ID)
		if err != nil || retiredCapture.State != string(state.StateStopped) {
			t.Fatalf("capture runtime was not retired after source phase for %s: %+v %v", request.Resource, retiredCapture, err)
		}
		retiredTarget, err := store.InstanceByID(t.Context(), target.ID)
		if err != nil || retiredTarget.State != string(state.StateStopped) {
			t.Fatalf("restore runtime was not retired after smoke phase for %s: %+v %v", request.Resource, retiredTarget, err)
		}
		status, err := store.EnvironmentQualificationExecution(t.Context(), target.ID)
		if err != nil || status.CaptureInstanceID != capture.ID || status.RetiredAt == nil || status.Retirement == nil || status.Retirement.Kind != state.QualificationNativeRetired {
			t.Fatalf("restore target retirement is not independent for %s: %+v %v", request.Resource, status, err)
		}
		restoreReceipt, receiptErr := store.EnvironmentQualificationRestoreReceipt(t.Context(), request.ID, status.Execution.Attempt)
		if receiptErr != nil || restoreReceipt.CaptureInstanceID != capture.ID || restoreReceipt.InstanceID != target.ID ||
			restoreReceipt.RequestID != request.ID || restoreReceipt.Attempt != status.Execution.Attempt || restoreReceipt.RecordedAt.IsZero() {
			t.Fatalf("successful restore did not persist attempt-bound restore evidence for %s: %+v %v", request.Resource, restoreReceipt, receiptErr)
		}
		smokeReceipt, smokeErr := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, status.Execution.Attempt)
		if smokeErr != nil || smokeReceipt.GraphID != request.GraphID || smokeReceipt.CaptureInstanceID != capture.ID ||
			smokeReceipt.InstanceID != target.ID || smokeReceipt.RecordedAt.IsZero() {
			t.Fatalf("successful restored graph visitor did not persist smoke evidence for %s: %+v %v", request.Resource, smokeReceipt, smokeErr)
		}
		if request.Resource == "workload/api" {
			wantURL := "http://10.100.0.1:10081" + api.EnvironmentQualificationServicePrefix + requests[0].GraphID + "/backend"
			found := false
			for _, entry := range vmm.restoreSpecs[request.Resource].APIEnv {
				if entry.Key == "BACKEND_URL" && entry.Value == wantURL {
					found = true
				}
			}
			if !found {
				t.Fatalf("restored %s did not receive the scoped reviewed service URL", request.Resource)
			}
		}
		deployment, err := store.DeploymentByID(t.Context(), request.DeploymentID)
		if err != nil || !deployment.EnvironmentWorkloadHeld() || deployment.Status != state.DeploySnapshotting {
			t.Fatalf("restore smoke changed deployment activation state: %+v %v", deployment, err)
		}
	}
}

func TestEnvironmentQualificationRestoreReceiptDoesNotDependOnSmokeVisitorSuccess(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	smokeErr := errors.New("restored application smoke failed")
	var source map[string]state.Instance
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(_ context.Context, instances map[string]state.Instance) error { source = instances; return nil },
		func(context.Context, map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
			return nil, smokeErr
		})
	if !errors.Is(err, smokeErr) || page.Restored != 0 || page.Executed != 0 || engine.ledger.ResidentRAM() != 0 || len(vmm.retiredArtifacts) != 0 {
		t.Fatalf("failed smoke was reported as successful restore dispatch or leaked a target: page=%+v ram=%d err=%v", page, engine.ledger.ResidentRAM(), err)
	}
	for _, request := range requests {
		capture := source[request.Resource]
		status, statusErr := store.EnvironmentQualificationExecution(t.Context(), capture.ID)
		receipt, err := store.EnvironmentQualificationRestoreReceipt(t.Context(), request.ID, status.Execution.Attempt)
		if statusErr != nil || err != nil || receipt.RequestID != request.ID || receipt.CaptureInstanceID != capture.ID || receipt.InstanceID == capture.ID {
			t.Fatalf("successful native restore evidence was coupled to smoke result for %s: %+v %v", request.Resource, receipt, err)
		}
		if _, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, status.Execution.Attempt); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed restored visitor created isolated smoke evidence for %s: %v", request.Resource, err)
		}
	}
}

func TestEnvironmentQualificationNoopSmokeVisitorCannotCreateEvidence(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	var source map[string]state.Instance
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(_ context.Context, instances map[string]state.Instance) error { source = instances; return nil },
		func(context.Context, map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
			return nil, nil
		})
	if !errors.Is(err, state.ErrConflict) || page.Restored != 0 || page.Executed != 0 || engine.ledger.ResidentRAM() != 0 || len(vmm.retiredArtifacts) != 0 {
		t.Fatalf("no-op visitor was accepted as smoke or leaked a target: page=%+v ram=%d err=%v", page, engine.ledger.ResidentRAM(), err)
	}
	for _, request := range requests {
		capture := source[request.Resource]
		status, statusErr := store.EnvironmentQualificationExecution(t.Context(), capture.ID)
		if statusErr != nil {
			t.Fatalf("read capture execution: %v", statusErr)
		}
		if _, err := store.EnvironmentQualificationRestoreReceipt(t.Context(), request.ID, status.Execution.Attempt); err != nil {
			t.Fatalf("native restore evidence was lost with missing smoke evidence: %v", err)
		}
		if _, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, status.Execution.Attempt); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("no-op visitor created a smoke receipt: %v", err)
		}
	}
}

func TestEnvironmentQualificationGraphRestoreDispatchRefusesMissingRestoreCapabilityBeforeClaim(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(context.Context, map[string]state.Instance) error { return nil },
		func(context.Context, map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
			return nil, nil
		})
	ids, listErr := store.ListEnvironmentWorkloadQualificationGraphsForDispatch(t.Context(), engine.defaultLocalNodeID, "", 1)
	if !errors.Is(err, state.ErrConflict) || listErr != nil || len(ids) != 1 || ids[0] != requests[0].GraphID ||
		page.Examined != 0 || page.Claimed != 0 || page.Executed != 0 || page.Restored != 0 || vmm.coldBoots != 0 || engine.ledger.ResidentRAM() != 0 {
		t.Fatalf("restore dispatch without native capability claimed or booted a graph: page=%+v ids=%v boots=%d ram=%d err=%v listErr=%v",
			page, ids, vmm.coldBoots, engine.ledger.ResidentRAM(), err, listErr)
	}
}

func TestEnvironmentQualificationGraphRuntimeRejectsWorkerAndJobBeforeVMEffects(t *testing.T) {
	store, _, queued := queuedQualificationExecutionFixtureWithBindings(t, nil, api.ExecutionModeRequest)
	for _, mode := range []string{api.ExecutionModeWorker, api.ExecutionModeJob} {
		t.Run(mode, func(t *testing.T) {
			request := queued[0]
			request.ExecutionMode = mode
			vmm := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
			visited := false
			err := engine.WithEnvironmentQualificationGraphRuntimes(t.Context(), []state.EnvironmentWorkloadQualificationRequest{request}, func(context.Context, map[string]state.Instance) error {
				visited = true
				return nil
			})
			if !errors.Is(err, state.ErrConflict) || visited || vmm.coldBoots != 0 || len(vmm.boots) != 0 ||
				len(vmm.captures) != 0 || len(vmm.retired) != 0 || engine.ledger.ResidentRAM() != 0 {
				t.Fatalf("tampered %s graph claim reached VM effects: visited=%t boots=%v captures=%v retired=%v ram=%d err=%v",
					mode, visited, vmm.boots, vmm.captures, vmm.retired, engine.ledger.ResidentRAM(), err)
			}
		})
	}
}

func TestEnvironmentQualificationGraphCaptureFailureKeepsPartialCohortUnqualified(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	var callerID string
	for _, request := range requests {
		if request.Resource == "workload/api" {
			callerID = request.AppID
		}
	}
	captureErr := errors.New("capture publication failed")
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: callerID,
		captureFailure: "workload/api", captureErr: captureErr}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	instances := map[string]state.Instance{}
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(_ context.Context, live map[string]state.Instance) error {
			instances = live
			return nil
		})
	if !errors.Is(err, captureErr) || page.Executed != 0 || len(v.captures) != 1 || v.captures[0] != "workload/api2" {
		t.Fatalf("partial capture was accepted as a complete graph: page=%+v captures=%v err=%v", page, v.captures, err)
	}
	if fmt.Sprint(v.retired) != "[workload/api workload/api2]" || e.ledger.ResidentRAM() != 0 {
		t.Fatalf("capture failure did not retire the whole cohort: retired=%v ram=%d", v.retired, e.ledger.ResidentRAM())
	}
	if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), instances["workload/api2"].ID); err != nil {
		t.Fatalf("completed member capture was not retained: %v", err)
	}
	if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), instances["workload/api"].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed member received a capture receipt: %v", err)
	}
}

func TestEnvironmentQualificationGraphVisitorFailureDoesNotCapture(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	var callerID string
	for _, request := range requests {
		if request.Resource == "workload/api" {
			callerID = request.AppID
		}
	}
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: callerID}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	visitorErr := errors.New("isolated graph smoke failed")
	instances := map[string]state.Instance{}
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(_ context.Context, live map[string]state.Instance) error {
			instances = live
			return visitorErr
		})
	if !errors.Is(err, visitorErr) || page.Claimed != 1 || page.Executed != 0 || len(v.captures) != 0 {
		t.Fatalf("failed graph behavior check reached capture: page=%+v captures=%v err=%v", page, v.captures, err)
	}
	if fmt.Sprint(v.retired) != "[workload/api workload/api2]" || e.ledger.ResidentRAM() != 0 {
		t.Fatalf("failed graph visitor did not retire the whole cohort: retired=%v ram=%d", v.retired, e.ledger.ResidentRAM())
	}
	for _, request := range requests {
		if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), instances[request.Resource].ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed graph visitor left capture evidence for %s: %v", request.Resource, err)
		}
	}
}

func TestEnvironmentQualificationGraphDispatchRequiresCaptureBeforeClaim(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, api.ExecutionModeRequest, api.ExecutionModeService)
	v := newQualificationRuntimeVMM(&fakeVMM{})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	page, err := e.DispatchEnvironmentWorkloadQualificationGraphs(t.Context(), e.defaultLocalNodeID, "graph-scheduler", "", 1,
		func(context.Context, map[string]state.Instance) error { return nil })
	ids, listErr := store.ListEnvironmentWorkloadQualificationGraphsForDispatch(t.Context(), e.defaultLocalNodeID, "", 1)
	if !errors.Is(err, state.ErrConflict) || listErr != nil || len(ids) != 1 || ids[0] != requests[0].GraphID ||
		page.Examined != 0 || page.Claimed != 0 || page.Executed != 0 || v.coldBoots != 0 || e.ledger.ResidentRAM() != 0 {
		t.Fatalf("dispatch without durable capture support claimed or booted a graph: page=%+v ids=%v boots=%d ram=%d err=%v listErr=%v",
			page, ids, v.coldBoots, e.ledger.ResidentRAM(), err, listErr)
	}
}

func TestEnvironmentQualificationDispatchRefusesStalePagesBeforeClaim(t *testing.T) {
	for _, change := range []string{"owner_transfer", "source_revocation", "other_claim"} {
		t.Run(change, func(t *testing.T) {
			store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
			request := requests[0]
			v := newDispatchQualificationVMM(&fakeVMM{})
			delayed := &delayedQualificationDispatchStore{MemStore: store}
			e := newEngine(t, delayed, v, &fakeNotifier{}, "test-fc")
			nodeB, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "new-owner", Active: true, AdmissionCeilingMB: 4096})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetAppNodeID(t.Context(), request.AppID, e.defaultLocalNodeID); err != nil {
				t.Fatal(err)
			}
			delayed.afterList = func() {
				switch change {
				case "owner_transfer":
					err = store.ReassignAppOwner(t.Context(), request.AppID, e.defaultLocalNodeID, nodeB.ID)
				case "source_revocation":
					_, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
				case "other_claim":
					_, err = store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), request.ID, e.defaultLocalNodeID, "winner", time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "stale-scanner", "", 1,
				func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
					t.Fatal("stale page booted a VM")
					return nil
				})
			if err != nil || page.Examined != 1 || page.Skipped != 1 || page.Claimed != 0 || page.Executed != 0 || page.NextCursor != request.ID || v.coldBoots != 0 || v.destroys != 0 {
				t.Fatal("advisory page became claim/native authority", page, err)
			}
			if change == "owner_transfer" {
				claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), request.ID, nodeB.ID, "new-owner", time.Minute)
				if err != nil || claimed.Attempt != 1 {
					t.Fatal("stale owner consumed an attempt", claimed.Attempt, err)
				}
			}
		})
	}
}

func TestEnvironmentQualificationDispatchRacingScannersBootOnce(t *testing.T) {
	store, _, _ := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	delayed := &delayedQualificationDispatchStore{MemStore: store, afterList: func() { ready.Done(); <-start }}
	vms := []*qualificationRuntimeVMM{newDispatchQualificationVMM(&fakeVMM{}), newDispatchQualificationVMM(&fakeVMM{})}
	engines := []*Engine{newEngine(t, delayed, vms[0], &fakeNotifier{}, "test-fc"), newEngine(t, delayed, vms[1], &fakeNotifier{}, "test-fc")}
	var visits atomic.Int32
	type outcome struct {
		page EnvironmentQualificationDispatchPage
		err  error
	}
	results := make(chan outcome, 2)
	for _, e := range engines {
		go func() {
			page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, uuid.NewString(), "", 1,
				func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
					visits.Add(1)
					return nil
				})
			results <- outcome{page, err}
		}()
	}
	ready.Wait()
	close(start)
	claimed, executed, skipped := 0, 0, 0
	for range engines {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		claimed += r.page.Claimed
		executed += r.page.Executed
		skipped += r.page.Skipped
	}
	if claimed != 1 || executed != 1 || skipped != 1 || visits.Load() != 1 || vms[0].coldBoots+vms[1].coldBoots != 1 || engines[0].ledger.ResidentRAM()+engines[1].ledger.ResidentRAM() != 0 {
		t.Fatal("racing durable consumers duplicated native work", claimed, executed, skipped)
	}
}

func TestEnvironmentQualificationDispatchCancellationLeavesNeighborsQueued(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeService)
	v := newDispatchQualificationVMM(&fakeVMM{})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	ctx, cancel := context.WithCancel(t.Context())
	page, err := e.DispatchEnvironmentWorkloadQualifications(ctx, e.defaultLocalNodeID, "cancelled", "", 2,
		func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
			cancel()
			return nil
		})
	if !errors.Is(err, context.Canceled) || page.Examined != 1 || page.Claimed != 1 || page.Executed != 0 || page.NextCursor == "" || e.ledger.ResidentRAM() != 0 || v.destroys != 1 {
		t.Fatal("cancelled consumer acquired its neighbor or skipped retirement", page, err)
	}
	ids := []string{requests[0].ID, requests[1].ID}
	slices.Sort(ids)
	if page.NextCursor != ids[0] {
		t.Fatal("cancellation advanced past unclaimed work", page.NextCursor)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "resume", page.NextCursor, 2,
		func(_ context.Context, request state.EnvironmentWorkloadQualificationRequest, _ state.Instance) error {
			if request.ID != ids[1] || request.Attempt != 1 {
				return errors.New("cancelled consumer changed unvisited attempt")
			}
			return nil
		})
	if err != nil || page.Examined != 1 || page.Executed != 1 || page.NextCursor != "" {
		t.Fatal("resume lost queued neighbor", page, err)
	}
}

func TestEnvironmentQualificationDispatchUncertainRetirementKeepsCapacity(t *testing.T) {
	store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeService)
	injected := errors.New("native retirement unavailable")
	v := newDispatchQualificationVMM(&fakeVMM{destroyErr: injected})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	frames := map[string]state.EnvironmentQualificationExecution{}
	page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", "", 2,
		func(ctx context.Context, request state.EnvironmentWorkloadQualificationRequest, ins state.Instance) error {
			status, err := store.EnvironmentQualificationExecution(ctx, ins.ID)
			frames[request.ID] = status.Execution
			return err
		})
	if !errors.Is(err, injected) || page.Examined != 2 || page.Claimed != 2 || page.Executed != 0 || e.ledger.ResidentRAM() != 2*(512+api.PerVMOverheadMB) {
		t.Fatal("consumer acknowledged uncertain retirement or starved neighbor", page, err)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "again", "", 2,
		func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
			t.Fatal("uncertain attempt replayed")
			return nil
		})
	if err != nil || page.Examined != 0 || v.coldBoots != 2 {
		t.Fatal("unfinished original execution reentered dispatch", page, err)
	}
	if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
		t.Fatal(err)
	}
	v.destroyErr = nil
	recovery, err := e.RecoverEnvironmentQualificationExecutions(t.Context(), e.defaultLocalNodeID, "", 3)
	if err != nil || recovery.Retired != 2 || e.ledger.ResidentRAM() != 0 {
		t.Fatal("original retirement could not release retained capacity", recovery, err)
	}
	for _, request := range requests {
		status, err := store.EnvironmentQualificationExecution(t.Context(), frames[request.ID].InstanceID)
		if err != nil || status.Execution != frames[request.ID] || status.RetiredAt == nil {
			t.Fatal("recovery replaced original consumer frame", err)
		}
	}
}

func TestEnvironmentQualificationDispatchPreflightDoesNotClaimWork(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	legacy := &fakeVMM{}
	e := newEngine(t, store, legacy, &fakeNotifier{}, "test-fc")
	visit := func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
		t.Fatal("preflight failure ran visitor")
		return nil
	}
	if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "old-node", "", 1, visit); !errors.Is(err, state.ErrConflict) || legacy.coldBoots != 0 {
		t.Fatal("generic VMM consumed durable qualification work", err)
	}
	e = newEngine(t, store, newDispatchQualificationVMM(&fakeVMM{}), &fakeNotifier{}, "test-fc")
	for _, args := range []struct {
		node, worker, cursor string
		limit                int
	}{
		{"", "worker", "", 1}, {uuid.Nil.String(), "worker", "", 1}, {uuid.NewString(), "worker", "", 1},
		{e.defaultLocalNodeID, "", "", 1}, {e.defaultLocalNodeID, " worker ", "", 1},
		{e.defaultLocalNodeID, strings.Repeat("w", api.EnvironmentGitOpsQualificationWorkerIDMaxBytes+1), "", 1},
		{e.defaultLocalNodeID, "worker", "bad-cursor", 1}, {e.defaultLocalNodeID, "worker", uuid.Nil.String(), 1},
		{e.defaultLocalNodeID, "worker", "", 0}, {e.defaultLocalNodeID, "worker", "", api.EnvironmentGitOpsQualificationDispatchBatchMax + 1},
	} {
		if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), args.node, args.worker, args.cursor, args.limit, visit); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid or foreign consumer accepted", err)
		}
	}
	if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "worker", "", 1, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("consumer without checker reserved work", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.DispatchEnvironmentWorkloadQualifications(ctx, e.defaultLocalNodeID, "cancelled", "", 1, visit); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled consumer claimed work", err)
	}
	claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID, e.defaultLocalNodeID, "first-valid", time.Minute)
	if err != nil || claimed.Attempt != 1 {
		t.Fatal("preflight rejection changed durable queue authority", claimed.Attempt, err)
	}
}
