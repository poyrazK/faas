package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createNormalPathRunningSibling(t *testing.T, f *normalPathFixture, deploymentID string) state.Instance {
	t.Helper()
	instance, err := f.store.CreateInstance(f.ctx, f.app.ID, deploymentID,
		string(state.StateRunning), 256, f.nodeID, "")
	if err != nil {
		t.Fatalf("create running sibling: %v", err)
	}
	return instance
}

func waitForNormalPathBodyPrefix(t *testing.T, f *normalPathFixture, prefix string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(deadline) {
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
		lastBody, lastStatus = body, statusCode
		if statusCode == http.StatusOK && strings.HasPrefix(string(body), prefix) {
			return body
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s/ did not return a body with prefix %q within %s; last status=%d body=%q",
		f.host, prefix, timeout, lastStatus, lastBody)
	return nil
}

func normalPathCaptureForURI(vmmd *e2etest.FakeVMMD, requestURI string) (e2etest.RequestCapture, bool) {
	for _, capture := range vmmd.Requests() {
		if capture.Init.GetRequestUri() == requestURI {
			return capture, true
		}
	}
	return e2etest.RequestCapture{}, false
}

// TestE2E_NormalPath_TrafficSpreadsAcrossLiveInstances covers the real
// Postgres-backed target hydration and round-robin path. Existing tests prove
// that one instance can serve traffic; this proves a second RUNNING sibling
// is actually routable instead of being silently ignored by the gateway.
func TestE2E_NormalPath_TrafficSpreadsAcrossLiveInstances(t *testing.T) {
	f := newNormalPathFixture(t, "normal-multi-distribution")
	if f == nil {
		return
	}
	deployment, first := createNormalPathLiveDeployment(t, f, f.app.ID, "multi-a")
	second := createNormalPathRunningSibling(t, f, deployment.ID)
	f.vmmd.SetVersion(first.ID, "multi-a")
	f.vmmd.SetVersion(second.ID, "multi-b")
	notifyNormalPathInstanceChanged(t, f, second.ID, string(state.StateRunning))
	waitForNormalPathBodyPrefix(t, f, "normal-path:", 10*time.Second)

	wantBody := map[string]string{
		first.ID:  "normal-path:multi-a\n",
		second.ID: "normal-path:multi-b\n",
	}
	seen := map[string]int{}
	for i := 0; i < 8; i++ {
		path := fmt.Sprintf("/multi-distribution/%d", i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		if statusCode != http.StatusOK {
			t.Fatalf("request %s status=%d body=%q, want 200", path, statusCode, body)
		}
		capture, ok := normalPathCaptureForURI(f.vmmd, path)
		if !ok {
			t.Fatalf("request %s did not reach VMMD", path)
		}
		want, ok := wantBody[capture.Init.GetInstance()]
		if !ok {
			t.Fatalf("request %s used unexpected instance %q", path, capture.Init.GetInstance())
		}
		if string(body) != want {
			t.Fatalf("request %s body=%q for instance %q, want %q", path, body, capture.Init.GetInstance(), want)
		}
		seen[capture.Init.GetInstance()]++
	}
	if seen[first.ID] == 0 || seen[second.ID] == 0 {
		t.Fatalf("traffic did not reach both live instances: first=%d second=%d", seen[first.ID], seen[second.ID])
	}
}

// TestE2E_NormalPath_StaleInstanceFailsOverAndReplacementRejoins proves the
// failure/recovery loop around a multi-instance target set. A transport-level
// VMMD outage must evict only the dead sibling, preserve the healthy sibling,
// and allow a newly RUNNING replacement to rejoin through pg_notify.
func TestE2E_NormalPath_StaleInstanceFailsOverAndReplacementRejoins(t *testing.T) {
	f := newNormalPathFixture(t, "normal-multi-failover")
	if f == nil {
		return
	}
	deployment, stale := createNormalPathLiveDeployment(t, f, f.app.ID, "multi-stale")
	healthy := createNormalPathRunningSibling(t, f, deployment.ID)
	replacement := state.Instance{}
	f.vmmd.SetVersion(stale.ID, "multi-stale")
	f.vmmd.SetVersion(healthy.ID, "multi-healthy")
	notifyNormalPathInstanceChanged(t, f, healthy.ID, string(state.StateRunning))
	waitForNormalPathBodyPrefix(t, f, "normal-path:", 10*time.Second)

	f.vmmd.FailAll(stale.ID, status.Error(codes.Unavailable, "simulated stale sibling"))
	sawStaleFailure := false
	for i := 0; i < 4; i++ {
		path := fmt.Sprintf("/multi-failover/trigger/%d", i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		if statusCode == http.StatusServiceUnavailable {
			assertAppUnavailableProblem(t, body)
			sawStaleFailure = true
			break
		}
		if statusCode != http.StatusOK || string(body) != "normal-path:multi-healthy\n" {
			t.Fatalf("pre-failover request %s status=%d body=%q, want healthy 200", path, statusCode, body)
		}
	}
	if !sawStaleFailure {
		t.Fatal("round-robin traffic never selected the intentionally stale sibling")
	}

	for i := 0; i < 6; i++ {
		path := fmt.Sprintf("/multi-failover/healthy/%d", i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		if statusCode != http.StatusOK || string(body) != "normal-path:multi-healthy\n" {
			t.Fatalf("post-failover request %s status=%d body=%q, want healthy 200", path, statusCode, body)
		}
		capture, ok := normalPathCaptureForURI(f.vmmd, path)
		if !ok || capture.Init.GetInstance() != healthy.ID {
			t.Fatalf("post-failover request %s used instance=%q, want healthy %q", path, requestInstance(capture.Init), healthy.ID)
		}
	}

	replacement = createNormalPathRunningSibling(t, f, deployment.ID)
	f.vmmd.SetVersion(replacement.ID, "multi-replacement")
	notifyNormalPathInstanceChanged(t, f, replacement.ID, string(state.StateRunning))

	seenReplacement := false
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		path := fmt.Sprintf("/multi-failover/recovered/%d", i)
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		if statusCode != http.StatusOK {
			t.Fatalf("recovery request %s status=%d body=%q, want 200", path, statusCode, body)
		}
		capture, ok := normalPathCaptureForURI(f.vmmd, path)
		if !ok {
			t.Fatalf("recovery request %s did not reach VMMD", path)
		}
		switch capture.Init.GetInstance() {
		case healthy.ID:
			if string(body) != "normal-path:multi-healthy\n" {
				t.Fatalf("healthy recovery body=%q, want healthy response", body)
			}
		case replacement.ID:
			if string(body) != "normal-path:multi-replacement\n" {
				t.Fatalf("replacement recovery body=%q, want replacement response", body)
			}
			seenReplacement = true
		default:
			t.Fatalf("recovery request %s used stale/unexpected instance %q", path, capture.Init.GetInstance())
		}
		if seenReplacement {
			return
		}
	}
	t.Fatalf("replacement instance %q never rejoined the live target set", replacement.ID)
}
