// cron_e2e_test.go — KVM-free HTTP cron acceptance (ADR-20261026201726).
//
// Boots the real apid, schedd and gatewayd processes of the normal-path
// fixture against Postgres. A cron created through the API must reach the
// app through schedd's minute tick, the invocation drain and the gateway
// bridge, carry the unforgeable cron source header, and land in the run
// history; a manual run must do the same and report the run's outcome.

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// cronRequests returns the bridge requests the cron at path received.
func cronRequests(vmmd *e2etest.FakeVMMD, path string) []*vmmdpb.ForwardHTTPRequestInit {
	var out []*vmmdpb.ForwardHTTPRequestInit
	for _, request := range vmmd.Requests() {
		if request.Init.GetMethod() == http.MethodPost && request.Init.GetRequestUri() == path &&
			hasNormalPathHeader(request.Init, api.InvocationSourceHeader, "cron") {
			out = append(out, request.Init)
		}
	}
	return out
}

func normalPathHeaderValue(request *vmmdpb.ForwardHTTPRequestInit, name string) string {
	for _, header := range request.GetHeaders() {
		if strings.EqualFold(header.GetName(), name) {
			return header.GetValue()
		}
	}
	return ""
}

func TestE2E_Cron_FiresOnScheduleAndOnDemand(t *testing.T) {
	f := newNormalPathFixture(t, "cron-e2e")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)

	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: f.app.ID, Schedule: "* * * * *", Path: "/tick"})
	if status != http.StatusCreated {
		t.Fatalf("create cron: status=%d body=%s", status, raw)
	}
	var cron api.CronResponse
	if err := json.Unmarshal(raw, &cron); err != nil {
		t.Fatalf("decode cron: %v body=%s", err, raw)
	}

	// A visitor's own X-Faas-Invocation-Source never reaches the guest.
	_, _, spoofStatus := doReqHeaders(t, f.h, f.host, http.MethodPost, "/tick", struct{}{},
		map[string]string{api.InvocationSourceHeader: "cron"})
	if spoofStatus != http.StatusOK {
		t.Fatalf("visitor POST /tick: status=%d", spoofStatus)
	}
	if spoofed := cronRequests(f.vmmd, "/tick"); len(spoofed) != 0 {
		t.Fatalf("visitor request reached the guest as a cron call: %+v", spoofed)
	}

	// The minute tick fires the cron; the drain delivers it.
	var scheduled *vmmdpb.ForwardHTTPRequestInit
	deadline := time.Now().Add(150 * time.Second)
	for scheduled == nil && time.Now().Before(deadline) {
		if requests := cronRequests(f.vmmd, "/tick"); len(requests) > 0 {
			scheduled = requests[0]
			break
		}
		time.Sleep(time.Second)
	}
	if scheduled == nil {
		f.h.DumpLogs(t)
		t.Fatal("cron did not reach the app within two minute boundaries")
	}
	scheduledID := normalPathHeaderValue(scheduled, api.InvocationIDHeader)
	if scheduledID == "" {
		t.Fatalf("cron request carried no %s header", api.InvocationIDHeader)
	}
	waitForCronRun(t, f, cron.ID, scheduledID, api.CronRunSuccess)

	// A manual run is queued, delivered by the drain, and reports the run.
	raw, status = doReq(t, f.h, f.key, http.MethodPost, "/v1/crons/"+cron.ID+"/run", nil)
	if status != http.StatusAccepted {
		t.Fatalf("fire-now: status=%d body=%s", status, raw)
	}
	var fire api.FireCronResponse
	if err := json.Unmarshal(raw, &fire); err != nil {
		t.Fatalf("decode fire-now: %v body=%s", err, raw)
	}
	var request api.FireCronRequestResponse
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		raw, status = doReq(t, f.h, f.key, http.MethodGet, "/v1/cron-fire-now-requests/"+fire.RequestID, nil)
		if status != http.StatusOK {
			t.Fatalf("fire-now status: status=%d body=%s", status, raw)
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatalf("decode fire-now status: %v body=%s", err, raw)
		}
		if request.Status == "succeeded" || request.Status == "failed" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if request.Status != "succeeded" || request.InvocationID == nil || request.FinishedAt == nil {
		f.h.DumpLogs(t)
		t.Fatalf("fire-now request = %+v, want succeeded with its invocation", request)
	}
	delivered := false
	for _, r := range cronRequests(f.vmmd, "/tick") {
		if normalPathHeaderValue(r, api.InvocationIDHeader) == *request.InvocationID {
			delivered = true
		}
	}
	if !delivered {
		t.Fatalf("manual run invocation %s never reached the app", *request.InvocationID)
	}
	waitForCronRun(t, f, cron.ID, *request.InvocationID, api.CronRunSuccess)
}

// waitForCronRun waits for the cron's run history to show invocationID with
// the wanted outcome.
func waitForCronRun(t *testing.T, f *normalPathFixture, cronID, invocationID string, want api.CronRunOutcome) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		raw, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/crons/"+cronID+"/runs", nil)
		if status != http.StatusOK {
			t.Fatalf("cron runs: status=%d body=%s", status, raw)
		}
		var runs api.ListCronRunsResponse
		if err := json.Unmarshal(raw, &runs); err != nil {
			t.Fatalf("decode cron runs: %v body=%s", err, raw)
		}
		for _, run := range runs.Runs {
			if run.ID == invocationID {
				last = fmt.Sprintf("%+v", run)
				if run.Outcome == want {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("cron run %s did not reach %s (last %s)", invocationID, want, last)
}
