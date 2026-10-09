// adr: 568 — private graph windows preserve every original execution frame.
package sched

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationGraphVMM struct {
	*qualificationRuntimeVMM
	boots, retired []string
	captures       []string
	captureProofs  map[string]state.EnvironmentQualificationSnapshot
	callerSpec     AppSpec
	callerID       string
	captureFailure string
	captureErr     error
}

func (v *qualificationGraphVMM) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, spec AppSpec) (*WakeOutcome, error) {
	v.boots = append(v.boots, frame.Resource)
	if frame.AppID == v.callerID {
		v.callerSpec = spec
	}
	out, err := v.qualificationRuntimeVMM.CreateEnvironmentQualification(ctx, frame, spec)
	if out != nil {
		out.HostIP = fmt.Sprintf("10.100.0.%d", len(v.boots)+1)
		out.Netns = "qualification-" + frame.InstanceID
	}
	return out, err
}

func (v *qualificationGraphVMM) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	v.retired = append(v.retired, frame.Resource)
	evidence, err := v.qualificationRuntimeVMM.RetireEnvironmentQualification(ctx, frame)
	// Each VM has its own native producer and immutable retirement receipt.
	evidence.Retirement.ReceiptID, evidence.Retirement.NativeGeneration = uuid.NewString(), uuid.NewString()
	if capture, ok := v.captureProofs[frame.InstanceID]; ok {
		evidence.Retirement.ReceiptID = capture.CaptureID
		evidence.Retirement.NativeGeneration = capture.NativeGeneration
		evidence.Retirement.KernelBootID = capture.KernelBootID
	}
	return evidence, err
}

func (v *qualificationGraphVMM) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	if frame.Resource == v.captureFailure {
		return EnvironmentQualificationSnapshotEvidence{}, v.captureErr
	}
	v.captures = append(v.captures, frame.Resource)
	capture := uuid.NewSHA1(uuid.Nil, []byte(frame.InstanceID+"-retirement")).String()
	snapshot := state.Snapshot{Tier: state.SnapshotTierWarm, StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, capture)}
	proof := state.EnvironmentQualificationSnapshot{
		CaptureID: capture, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), FCVersion: "test-fc", StorageKey: snapshot.StorageKey,
		VMStateStorageKey: state.SnapshotVMStateKey(snapshot), DriveStorageKey: state.SnapshotDriveKey(snapshot),
		BackingStorageKey: state.SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048,
	}
	if v.captureProofs == nil {
		v.captureProofs = map[string]state.EnvironmentQualificationSnapshot{}
	}
	v.captureProofs[frame.InstanceID] = proof
	return EnvironmentQualificationSnapshotEvidence{Execution: frame, Snapshot: proof}, nil
}

func claimedQualificationGraphFixture(t *testing.T) (*state.MemStore, []state.EnvironmentWorkloadQualificationRequest) {
	return claimedQualificationGraphFixtureWithTransport(t, "")
}

func claimedQualificationGraphFixtureWithTransport(t *testing.T, transport api.ServiceBindingTransport) (*state.MemStore, []state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	store, _, requests := queuedQualificationExecutionFixtureWithBindingsAndTransport(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}}, transport, api.ExecutionModeRequest, api.ExecutionModeService)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "test-fc")
	claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), requests[0].GraphID, engine.defaultLocalNodeID, "scheduler", time.Minute)
	if err != nil || len(claimed) != len(requests) {
		t.Fatalf("atomic qualification graph claim: %+v %v", claimed, err)
	}
	return store, claimed
}

func TestEnvironmentQualificationGraphKeepsDependenciesUntilCallerRetires(t *testing.T) {
	for _, failVisitor := range []bool{false, true} {
		t.Run(fmt.Sprint(failVisitor), func(t *testing.T) {
			store, requests := claimedQualificationGraphFixture(t)
			v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: requests[0].AppID}
			e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
			visitorErr := errors.New("smoke failed")
			err := e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
				if len(instances) != 2 || fmt.Sprint(v.boots) != "[workload/api2 workload/api]" || len(v.retired) != 0 {
					t.Fatal("dependency execution ordering", v.boots, v.retired)
				}
				caller := instances["workload/api"]
				route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend"})
				if err != nil || route.Target.InstanceID != instances["workload/api2"].ID || route.Target.DeploymentID != requests[1].DeploymentID || route.Caller.CleanupToken != "" || route.Target.CleanupToken != "" || route.Port != 8087 {
					t.Fatalf("private frame resolution: %+v %v", route, err)
				}
				want := "http://10.100.0.1:10081" + api.EnvironmentQualificationServicePrefix + requests[0].GraphID + "/backend"
				found := false
				for _, env := range v.callerSpec.APIEnv {
					if env.Key == "BACKEND_URL" {
						found = env.Value == want
					}
				}
				if !found {
					t.Fatal("private binding was not delivered")
				}
				if failVisitor {
					return visitorErr
				}
				return nil
			})
			if failVisitor && !errors.Is(err, visitorErr) || !failVisitor && err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(v.retired) != "[workload/api workload/api2]" || e.ledger.ResidentRAM() != 0 {
				t.Fatal("graph cleanup ordering", v.retired)
			}
			for _, request := range requests {
				ins, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
				dep, depErr := store.DeploymentByID(t.Context(), request.DeploymentID)
				if err != nil || ins.State != string(state.StateStopped) || depErr != nil || dep.Status != state.DeploySnapshotting || !dep.EnvironmentWorkloadHeld() {
					t.Fatal("graph window activated or leaked runtime", ins, dep, err, depErr)
				}
			}
		})
	}
}

func TestEnvironmentQualificationGraphSupportsHTTPSScopedBindings(t *testing.T) {
	store, requests := claimedQualificationGraphFixtureWithTransport(t, api.ServiceBindingTransportHTTPS)
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: requests[0].AppID}
	var resolvedTransport api.ServiceBindingTransport
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxyForTransport(
		func(_ context.Context, _ string, transport api.ServiceBindingTransport) (string, error) {
			resolvedTransport = transport
			return "https://gateway.internal:443", nil
		})
	err := e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
		caller := instances["workload/api"]
		route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
			NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend"})
		if err != nil || !route.RequireHTTPS || route.Target.InstanceID != instances["workload/api2"].ID {
			t.Fatalf("HTTPS private route: %+v %v", route, err)
		}
		found := false
		for _, env := range v.callerSpec.APIEnv {
			if env.Key == "BACKEND_URL" {
				found = env.Value == "https://api2.internal"+api.EnvironmentQualificationServicePrefix+requests[0].GraphID+"/backend"
			}
		}
		if !found {
			t.Fatal("HTTPS scoped binding did not use the target's verified internal alias")
		}
		aliases := store
		if allowed, err := aliases.EnvironmentQualificationServiceAliasAllowed(ctx, caller.AppID, "api2"); err != nil || !allowed {
			t.Fatalf("declared scoped alias = %v, %v", allowed, err)
		}
		if allowed, err := aliases.EnvironmentQualificationServiceAliasAllowed(ctx, caller.AppID, "other"); err != nil || allowed {
			t.Fatalf("undeclared scoped alias = %v, %v", allowed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedTransport != api.ServiceBindingTransportHTTPS {
		t.Fatalf("service proxy transport = %q, want HTTPS", resolvedTransport)
	}
	if allowed, err := store.EnvironmentQualificationServiceAliasAllowed(t.Context(), requests[0].AppID, "api2"); err != nil || allowed {
		t.Fatalf("alias remained discoverable after graph retirement: %v, %v", allowed, err)
	}
}

func TestEnvironmentQualificationGraphRejectsInvalidHTTPSListenerBeforeBoot(t *testing.T) {
	for _, base := range []string{
		"https://gateway.example.com:443",
		"https://gateway.internal:444",
		"http://gateway.internal:443",
		"https://-gateway.internal:443",
		"https://gateway..internal:443",
		"https://gateway.internal:443?unexpected=1",
		"https://user@gateway.internal:443",
	} {
		t.Run(base, func(t *testing.T) {
			store, requests := claimedQualificationGraphFixtureWithTransport(t, api.ServiceBindingTransportHTTPS)
			v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: requests[0].AppID}
			engine := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) {
				return base, nil
			})
			err := engine.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(context.Context, map[string]state.Instance) error {
				t.Fatal("invalid HTTPS listener reached the graph visitor")
				return nil
			})
			if !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) || len(v.boots) != 0 {
				t.Fatalf("invalid HTTPS listener %q: boots=%v err=%v", base, v.boots, err)
			}
		})
	}
}

func TestEnvironmentQualificationGraphRejectsIncompleteOrForgedCohortBeforeEffects(t *testing.T) {
	for _, change := range []string{"missing", "duplicate", "forged", "no_proxy", "single_runtime"} {
		t.Run(change, func(t *testing.T) {
			store, requests := claimedQualificationGraphFixture(t)
			v := newQualificationRuntimeVMM(&fakeVMM{})
			e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
			if change != "no_proxy" {
				e.WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
			}
			visit := func(context.Context, map[string]state.Instance) error {
				t.Fatal("invalid graph reached visitor")
				return nil
			}
			var err error
			switch change {
			case "missing":
				requests = requests[:1]
			case "duplicate":
				requests[1] = requests[0]
			case "forged":
				requests[1].LeaseToken = "not-issued"
			case "single_runtime":
				err = e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), requests[0], func(context.Context, state.Instance) error { t.Fatal("binding ran without cohort"); return nil })
			}
			if change != "single_runtime" {
				err = e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, visit)
			}
			if err == nil || v.coldBoots != 0 || v.destroys != 0 || e.ledger.ResidentRAM() != 0 {
				t.Fatal("invalid graph performed effects", err)
			}
		})
	}
}

type qualificationUnsupportedProtocolStore struct{ *state.MemStore }

func (s qualificationUnsupportedProtocolStore) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.MemStore.AppByID(ctx, id)
	app.AppProtocol = api.AppProtocolGRPC
	return app, err
}

func TestEnvironmentQualificationGraphRejectsUnsupportedProtocolBeforeEffects(t *testing.T) {
	store, requests := claimedQualificationGraphFixture(t)
	v := newQualificationRuntimeVMM(&fakeVMM{})
	e := newEngine(t, qualificationUnsupportedProtocolStore{store}, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	err := e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(context.Context, map[string]state.Instance) error {
		t.Fatal("HTTP1 graph bridge reached a gRPC target")
		return nil
	})
	if !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) || v.coldBoots != 0 || e.ledger.ResidentRAM() != 0 {
		t.Fatal("unsupported graph performed effects", err)
	}
}
