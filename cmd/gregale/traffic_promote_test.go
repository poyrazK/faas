package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdTrafficPromoteResolvesRevisionAndSetsOneHundredPercent(t *testing.T) {
	const deploymentID = "0123456789abcdef0123456789abcdef"
	var patchHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-app/deployments":
			writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{{
				ID: deploymentID, Revision: 44, Status: statusLive, TrafficPercent: 0,
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/"+deploymentID:
			writeJSONTest(w, api.DeploymentResponse{ID: deploymentID, Revision: 44, Status: statusLive, TrafficPercent: 0})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/deployments/"+deploymentID+"/traffic":
			patchHits.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read promotion body: %v", err)
			}
			if got, want := string(body), `{"traffic_percent":100}`; got != want {
				t.Fatalf("promotion body = %q, want %q", got, want)
			}
			writeJSONTest(w, api.DeploymentResponse{ID: deploymentID, Revision: 44, Status: statusLive, TrafficPercent: 100})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdTraffic([]string{"promote", "--app", "my-app", "--deployment", "v44"}); code != 0 {
		t.Fatalf("traffic promote exit = %d, want 0", code)
	}
	if patchHits.Load() != 1 {
		t.Fatalf("promotion PATCH hits = %d, want 1", patchHits.Load())
	}
	if want := "Promoted v44: 0% → 100% production traffic."; !strings.Contains(stdout.String(), want) {
		t.Fatalf("promotion output missing %q\nfull output: %s", want, stdout.String())
	}
}

func TestCmdTrafficPromoteAlreadyAtOneHundredIsIdempotentJSON(t *testing.T) {
	const deploymentID = "0123456789abcdef0123456789abcdef"
	var patchHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/"+deploymentID:
			writeJSONTest(w, api.DeploymentResponse{ID: deploymentID, Revision: 44, Status: statusLive, TrafficPercent: 100})
		case r.Method == http.MethodPatch:
			patchHits.Add(1)
			http.Error(w, "unexpected mutation", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	previousJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = previousJSON }()
	if code := cmdTrafficPromote([]string{"--deployment", deploymentID}); code != 0 {
		t.Fatalf("traffic promote retry exit = %d, want 0", code)
	}
	if patchHits.Load() != 0 {
		t.Fatalf("idempotent promotion made %d PATCH requests, want 0", patchHits.Load())
	}
	var receipt TrafficPromotionReceipt
	if err := json.Unmarshal([]byte(stdout.String()), &receipt); err != nil {
		t.Fatalf("decode promotion receipt: %v\noutput: %s", err, stdout.String())
	}
	if !receipt.AlreadyPromoted || receipt.FromPercent != 100 || receipt.ToPercent != 100 {
		t.Fatalf("promotion receipt = %+v", receipt)
	}
	if receipt.Deployment.Revision != 44 || receipt.Deployment.TrafficPercent != 100 {
		t.Fatalf("promotion deployment = %+v", receipt.Deployment)
	}
}

func TestCmdTrafficPromoteRejectsNonLiveDeploymentBeforePatch(t *testing.T) {
	const deploymentID = "0123456789abcdef0123456789abcdef"
	var patchHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchHits.Add(1)
		}
		writeJSONTest(w, api.DeploymentResponse{ID: deploymentID, Revision: 44, Status: "building", TrafficPercent: 0})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	oldErr := osStderr
	osStderr = io.Discard
	defer func() { osStderr = oldErr }()
	if code := cmdTrafficPromote([]string{"--deployment", deploymentID}); code == 0 {
		t.Fatal("traffic promote accepted a non-live deployment")
	}
	if patchHits.Load() != 0 {
		t.Fatalf("non-live promotion made %d PATCH requests, want 0", patchHits.Load())
	}
}
