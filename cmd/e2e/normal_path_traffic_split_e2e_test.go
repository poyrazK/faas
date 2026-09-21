package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func createNormalPathExplicitTrafficDeployment(
	t *testing.T,
	f *normalPathFixture,
	version string,
	trafficPercent int,
) (state.Deployment, state.Instance) {
	t.Helper()

	digestByte := "a"
	if version == "candidate" {
		digestByte = "b"
	}
	deployment, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:                  f.app.ID,
		Kind:                   state.DeploymentKindImage,
		ImageDigest:            "sha256:" + strings.Repeat(digestByte, 64),
		TrafficPercent:         trafficPercent,
		TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatalf("create %s traffic deployment: %v", version, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, deployment.ID); err != nil {
		t.Fatalf("mark %s traffic deployment live: %v", version, err)
	}
	instance, err := f.store.CreateInstance(f.ctx, f.app.ID, deployment.ID,
		string(state.StateRunning), 256, f.nodeID, "")
	if err != nil {
		t.Fatalf("create %s traffic instance: %v", version, err)
	}
	return deployment, instance
}

func updateNormalPathTraffic(t *testing.T, f *normalPathFixture, deploymentID string, percent int) api.DeploymentResponse {
	t.Helper()
	body, statusCode := doReq(t, f.h, f.key, http.MethodPatch,
		"/v1/deployments/"+deploymentID+"/traffic",
		api.UpdateDeploymentTrafficRequest{TrafficPercent: percent})
	if statusCode != http.StatusOK {
		t.Fatalf("update deployment %s traffic to %d: status=%d body=%s",
			deploymentID, percent, statusCode, body)
	}
	var response api.DeploymentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode traffic update for deployment %s: %v body=%s", deploymentID, err, body)
	}
	if response.TrafficPercent != percent {
		t.Fatalf("traffic update for deployment %s returned %d, want %d",
			deploymentID, response.TrafficPercent, percent)
	}
	return response
}

func waitForNormalPathTrafficResponse(t *testing.T, f *normalPathFixture, want string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(deadline) {
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil,
			map[string]string{"Authorization": "Bearer " + f.key})
		lastBody, lastStatus = body, statusCode
		if statusCode == http.StatusOK && string(body) == want {
			return body
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s/ did not return %q within %s; last status=%d body=%q", f.host, want, timeout, lastStatus, lastBody)
	return nil
}

func waitForNormalPathTrafficInstance(
	t *testing.T,
	f *normalPathFixture,
	prefix string,
	wantInstanceID string,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastInstance string
	var lastStatus int
	for i := 0; time.Now().Before(deadline); i++ {
		path := fmt.Sprintf("/%s/probe/%d", strings.TrimPrefix(prefix, "/"), i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil,
			map[string]string{"Authorization": "Bearer " + f.key})
		lastStatus = statusCode
		capture, ok := normalPathCaptureForURI(f.vmmd, path)
		if ok {
			lastInstance = capture.Init.GetInstance()
			if statusCode == http.StatusOK && lastInstance == wantInstanceID {
				return
			}
		} else if statusCode == http.StatusOK {
			lastInstance = "<not captured>"
		}
		if statusCode != http.StatusOK && len(body) == 0 {
			lastInstance = fmt.Sprintf("<status %d>", statusCode)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("traffic probe %q did not reach instance %q within %s; last status=%d instance=%q",
		prefix, wantInstanceID, timeout, lastStatus, lastInstance)
}

func assertNormalPathTrafficSample(
	t *testing.T,
	f *normalPathFixture,
	prefix string,
	requests int,
	wantBodyByInstance map[string]string,
) map[string]int {
	t.Helper()
	seen := make(map[string]int)
	for i := 0; i < requests; i++ {
		path := fmt.Sprintf("/%s/sample/%d", strings.TrimPrefix(prefix, "/"), i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil,
			map[string]string{"Authorization": "Bearer " + f.key})
		if statusCode != http.StatusOK {
			t.Fatalf("traffic sample %s status=%d body=%q, want 200", path, statusCode, body)
		}
		capture, ok := normalPathCaptureForURI(f.vmmd, path)
		if !ok {
			t.Fatalf("traffic sample %s did not reach VMMD", path)
		}
		wantBody, ok := wantBodyByInstance[capture.Init.GetInstance()]
		if !ok {
			t.Fatalf("traffic sample %s used unexpected instance %q", path, capture.Init.GetInstance())
		}
		if string(body) != wantBody {
			t.Fatalf("traffic sample %s body=%q for instance %q, want %q",
				path, body, capture.Init.GetInstance(), wantBody)
		}
		seen[capture.Init.GetInstance()]++
	}
	return seen
}

// TestE2E_NormalPath_TrafficSplitUpdatesAndRollsBack covers the control-plane
// to data-plane contract for general-path traffic splitting. It starts with a
// live 0% candidate, changes weights through the public API, proves the real
// gateway refreshes through pg_notify, and promotes each side back to 100%.
func TestE2E_NormalPath_TrafficSplitUpdatesAndRollsBack(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "normal-traffic-split", api.PlanPro)
	if f == nil {
		return
	}

	stableDeployment, stableInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "stable")
	candidateDeployment, candidateInstance := createNormalPathExplicitTrafficDeployment(
		t, f, "candidate", 0)
	f.vmmd.SetVersion(stableInstance.ID, "stable")
	f.vmmd.SetVersion(candidateInstance.ID, "candidate")
	notifyNormalPathDeploymentChanged(t, f, candidateDeployment.ID)
	notifyNormalPathInstanceChanged(t, f, stableInstance.ID, string(state.StateRunning))
	notifyNormalPathInstanceChanged(t, f, candidateInstance.ID, string(state.StateRunning))

	waitForNormalPathTrafficResponse(t, f, "normal-path:stable\n", 10*time.Second)
	initial := assertNormalPathTrafficSample(t, f, "traffic-split/zero", 8, map[string]string{
		stableInstance.ID:    "normal-path:stable\n",
		candidateInstance.ID: "normal-path:candidate\n",
	})
	if initial[candidateInstance.ID] != 0 || initial[stableInstance.ID] != 8 {
		t.Fatalf("zero-percent candidate received traffic: stable=%d candidate=%d",
			initial[stableInstance.ID], initial[candidateInstance.ID])
	}

	updateNormalPathTraffic(t, f, candidateDeployment.ID, 25)
	waitForNormalPathTrafficInstance(t, f, "traffic-split/25", candidateInstance.ID, 10*time.Second)
	split := assertNormalPathTrafficSample(t, f, "traffic-split/25", 100, map[string]string{
		stableInstance.ID:    "normal-path:stable\n",
		candidateInstance.ID: "normal-path:candidate\n",
	})
	if split[stableInstance.ID] < 65 || split[stableInstance.ID] > 85 ||
		split[candidateInstance.ID] < 15 || split[candidateInstance.ID] > 35 {
		t.Fatalf("25%% traffic split outside tolerance: stable=%d candidate=%d",
			split[stableInstance.ID], split[candidateInstance.ID])
	}

	stableAfterSplit, err := f.store.DeploymentByID(f.ctx, stableDeployment.ID)
	if err != nil {
		t.Fatalf("reload stable deployment after split: %v", err)
	}
	if stableAfterSplit.TrafficPercent != 75 {
		t.Fatalf("stable deployment traffic=%d after candidate 25%%, want 75", stableAfterSplit.TrafficPercent)
	}

	updateNormalPathTraffic(t, f, candidateDeployment.ID, 100)
	waitForNormalPathTrafficInstance(t, f, "traffic-split/candidate-full", candidateInstance.ID, 10*time.Second)
	fullCandidate := assertNormalPathTrafficSample(t, f, "traffic-split/candidate-full", 10, map[string]string{
		stableInstance.ID:    "normal-path:stable\n",
		candidateInstance.ID: "normal-path:candidate\n",
	})
	if fullCandidate[candidateInstance.ID] != 10 || fullCandidate[stableInstance.ID] != 0 {
		t.Fatalf("candidate 100%% traffic still reached stable: stable=%d candidate=%d",
			fullCandidate[stableInstance.ID], fullCandidate[candidateInstance.ID])
	}

	updateNormalPathTraffic(t, f, stableDeployment.ID, 100)
	waitForNormalPathTrafficInstance(t, f, "traffic-split/rollback", stableInstance.ID, 10*time.Second)
	rollback := assertNormalPathTrafficSample(t, f, "traffic-split/rollback", 10, map[string]string{
		stableInstance.ID:    "normal-path:stable\n",
		candidateInstance.ID: "normal-path:candidate\n",
	})
	if rollback[stableInstance.ID] != 10 || rollback[candidateInstance.ID] != 0 {
		t.Fatalf("rollback traffic still reached candidate: stable=%d candidate=%d",
			rollback[stableInstance.ID], rollback[candidateInstance.ID])
	}
}
