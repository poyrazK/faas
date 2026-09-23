package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestE2E_ServiceRollout_TwoGatewayRecoveryAndDrain uses the real apid,
// schedd, two gatewayd-internal processes, PostgreSQL notifications, and the
// normal-path fake vmmd. The second gateway starts late on purpose: a missing
// ACK must retain the predecessor; restarting schedd must replay the durable
// handoff; a request selected before the route change must finish before the
// predecessor can be superseded.
func TestE2E_ServiceRollout_TwoGatewayRecoveryAndDrain(t *testing.T) {
	const primaryName = state.DefaultLocalNodeName
	const secondaryName = "rollout-gateway-b"
	const slug = "service-handoff-two-gateways"
	f := newNormalPathFixtureWithRequest(t, slug, api.PlanPro, 0,
		api.CreateAppRequest{
			Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false),
			ExecutionMode:   api.ExecutionModeService,
			ServiceReplicas: &api.ServiceReplicas{Min: 1, Max: 1, Desired: 1},
		}, "FAAS_E2E_GATEWAY_NODE_NAME="+primaryName)
	if f == nil {
		return // pgtest skips when PostgreSQL is unavailable.
	}

	primary, err := f.store.ComputeNodeByName(f.ctx, primaryName)
	if err != nil {
		t.Fatal(err)
	}
	role := "compute-only"
	primary.Role = &role
	primaryGatewayTarget := "tcp://" + strings.TrimPrefix(f.h.GatewayURL, "http://")
	primary.GatewayTargetURL = &primaryGatewayTarget
	primary.TargetURL = "unix://" + f.h.VMMDSock
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, primary); err != nil {
		t.Fatalf("register primary gateway: %v", err)
	}
	secondary := primary
	secondary.ID = ""
	secondary.Name = secondaryName
	// The registry promises a serving gateway, but its process is not running
	// yet. The first route generation must therefore time out safely.
	missingURL := "tcp://127.0.0.1:1"
	secondary.GatewayTargetURL = &missingURL
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("register missing secondary gateway: %v", err)
	}

	stable, stableInstance := createServiceHandoffDeployment(t, f, "stable", false, time.Time{})
	f.vmmd.SetVersion(stableInstance.ID, "stable")
	setServiceHandoffInflight(f.vmmd, stableInstance.ID, 0)
	if status, body := probeServiceHandoffGateway(t, f.h.GatewayURL, f.host, 8*time.Second); status != http.StatusOK || string(body) != "normal-path:stable\n" {
		t.Fatalf("initial stable route = %d %q", status, body)
	}

	gate := f.vmmd.InstallRequestGate(stableInstance.ID, 1)
	defer gate.Release()
	longDone := make(chan error, 1)
	go func() {
		status, body, err := requestServiceHandoffGateway(f.h.GatewayURL, f.host, "/long", 90*time.Second)
		if err != nil {
			longDone <- err
			return
		}
		if status != http.StatusOK || string(body) != "normal-path:stable\n" {
			longDone <- fmt.Errorf("long request = %d %q, want stable 200", status, body)
			return
		}
		longDone <- nil
	}()
	if !gate.WaitArrived(10 * time.Second) {
		t.Fatal("stable request did not enter fake vmmd")
	}
	setServiceHandoffInflight(f.vmmd, stableInstance.ID, 1)

	started := time.Now().UTC()
	candidate, candidateInstance := createServiceHandoffDeployment(t, f, "candidate", true, started)
	f.vmmd.SetVersion(candidateInstance.ID, "candidate")
	setServiceHandoffInflight(f.vmmd, candidateInstance.ID, 0)
	notifyNormalPathInstanceChanged(t, f, candidateInstance.ID, string(state.StateRunning))
	notifyNormalPathDeploymentChanged(t, f, candidate.ID)

	waitServiceHandoff(t, f, candidate.ID, 25*time.Second, func(d state.Deployment) bool {
		return d.ServiceRolloutHandoff.LastError == "route_convergence_timeout" &&
			containsServiceHandoffGateway(d.ServiceRolloutHandoff.AcknowledgedGateways, primaryName) &&
			containsServiceHandoffGateway(d.ServiceRolloutHandoff.MissingGateways, secondaryName)
	})
	assertServiceHandoffPredecessorLive(t, f, stable.ID, longDone)
	if status, body := probeServiceHandoffGateway(t, f.h.GatewayURL, f.host, 8*time.Second); status != http.StatusOK || string(body) != "normal-path:candidate\n" {
		t.Fatalf("primary route during missing secondary ACK = %d %q, want candidate 200", status, body)
	}

	// A restarted scheduler must find the unfinished rollout in durable state.
	if err := f.h.RestartSchedd(); err != nil {
		t.Fatalf("restart schedd during blocked handoff: %v", err)
	}
	secondaryURL := f.h.StartAdditionalGateway(secondaryName)
	secondaryGatewayTarget := "tcp://" + strings.TrimPrefix(secondaryURL, "http://")
	secondary.GatewayTargetURL = &secondaryGatewayTarget
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("publish recovered secondary gateway: %v", err)
	}

	waitServiceHandoff(t, f, candidate.ID, 50*time.Second, func(d state.Deployment) bool {
		h := d.ServiceRolloutHandoff
		return h.Phase == state.ServiceRolloutPhaseDraining &&
			containsServiceHandoffGateway(h.AcknowledgedGateways, primaryName) &&
			containsServiceHandoffGateway(h.AcknowledgedGateways, secondaryName)
	})
	assertServiceHandoffPredecessorLive(t, f, stable.ID, longDone)
	for name, url := range map[string]string{primaryName: f.h.GatewayURL, secondaryName: secondaryURL} {
		status, body := probeServiceHandoffGateway(t, url, f.host, 8*time.Second)
		if status != http.StatusOK || string(body) != "normal-path:candidate\n" {
			t.Fatalf("%s did not adopt candidate route: %d %q", name, status, body)
		}
	}

	gate.Release()
	select {
	case err := <-longDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pre-cutover request did not complete after release")
	}
	setServiceHandoffInflight(f.vmmd, stableInstance.ID, 0)
	waitServiceHandoff(t, f, candidate.ID, 40*time.Second, func(d state.Deployment) bool {
		return d.RolloutState == "complete" && d.ServiceRolloutHandoff.Phase == state.ServiceRolloutPhaseComplete
	})
	old, err := f.store.DeploymentByID(f.ctx, stable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != state.DeploySuperseded {
		t.Fatalf("predecessor status = %s, want superseded after request drain", old.Status)
	}
}

func createServiceHandoffDeployment(t *testing.T, f *normalPathFixture, version string, rollout bool, started time.Time) (state.Deployment, state.Instance) {
	t.Helper()
	seed := "a"
	if rollout {
		seed = "b"
	}
	d := state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat(seed, 64),
	}
	if rollout {
		d.Status = state.DeployLive
		d.TrafficPercentExplicit = true
		d.TrafficPercent = 0
		d.RolloutState = "rolling_out"
		d.RolloutStartedAt = &started
		d.CreatedAt = started
	}
	dep, err := f.store.CreateDeployment(f.ctx, d)
	if err != nil {
		t.Fatalf("create %s service deployment: %v", version, err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	instance, err := f.store.CreateInstanceWithMode(f.ctx, f.app.ID, dep.ID,
		string(state.StateRunning), e2etest.FakeSnapshotRAMMB, f.nodeID, "", string(state.InstanceModeService))
	if err != nil {
		t.Fatalf("create %s service replica: %v", version, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark %s deployment live: %v", version, err)
	}
	return dep, instance
}

func setServiceHandoffInflight(vmmd *e2etest.FakeVMMD, instanceID string, inflight int64) {
	vmmd.SetInstanceStats(instanceID, &vmmdpb.InstanceStats{
		Instance: instanceID, LeaseUid: 20000, HostIp: "127.0.0.1",
		ResidentBytes:    wrapperspb.Int64(int64(e2etest.FakeSnapshotRAMMB) << 20),
		InflightRequests: inflight,
	})
}

func requestServiceHandoffGateway(url, host, path string, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func probeServiceHandoffGateway(t *testing.T, url, host string, timeout time.Duration) (int, []byte) {
	t.Helper()
	status, body, err := requestServiceHandoffGateway(url, host, "/", timeout)
	if err != nil {
		t.Fatalf("probe gateway %s: %v", url, err)
	}
	return status, body
}

func waitServiceHandoff(t *testing.T, f *normalPathFixture, deploymentID string, timeout time.Duration, ready func(state.Deployment) bool) state.Deployment {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last state.Deployment
	for time.Now().Before(deadline) {
		d, err := f.store.DeploymentByID(f.ctx, deploymentID)
		if err != nil {
			t.Fatalf("read service rollout: %v", err)
		}
		last = d
		if ready(d) {
			return d
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service rollout did not reach expected state in %s; last status=%s rollout=%s handoff=%+v", timeout, last.Status, last.RolloutState, last.ServiceRolloutHandoff)
	return last
}

func assertServiceHandoffPredecessorLive(t *testing.T, f *normalPathFixture, predecessorID string, longDone <-chan error) {
	t.Helper()
	dep, err := f.store.DeploymentByID(f.ctx, predecessorID)
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != state.DeployLive {
		t.Fatalf("predecessor retired before route/drain barriers: %s", dep.Status)
	}
	select {
	case err := <-longDone:
		t.Fatalf("pre-cutover request finished before release: %v", err)
	default:
	}
}

func containsServiceHandoffGateway(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
