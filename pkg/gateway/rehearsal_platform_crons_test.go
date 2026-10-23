// adr: 20261022194510
package gateway_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRehearsalPlatformAPICapturesCronFireNow(t *testing.T) {
	const cron = "6b1e2c3d-4f5a-4b6c-8d7e-9f0a1b2c3d4e"
	for _, tc := range []struct {
		name, path string
		plan       api.Plan
		status     int
	}{
		{name: "fire", path: "/v1/crons/" + cron + "/run", plan: api.PlanPro, status: http.StatusAccepted},
		{name: "free-plan", path: "/v1/crons/" + cron + "/run", plan: api.PlanFree, status: http.StatusPaymentRequired},
		{name: "bad-id", path: "/v1/crons/not-a-uuid/run", plan: api.PlanPro, status: http.StatusBadRequest},
		{name: "unknown-cron", path: "/v1/crons/" + fakeMissingEndpoint + "/run", plan: api.PlanPro, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: tc.plan}
			response := serveRehearsalExecution(t, store, http.MethodPost, tc.path, "", "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.status != http.StatusAccepted {
				if len(store.calls) != 0 {
					t.Fatalf("refused fire-now was captured: %+v", store.calls)
				}
				return
			}
			got := store.calls[0]
			if len(store.calls) != 1 || got.Kind != api.ScenarioPlatformKindCronRun || got.Address != cron || got.Name != "run" || string(got.Message) != `{}` {
				t.Fatalf("captured %+v (%s)", got, got.Message)
			}
			var fired api.FireCronResponse
			if err := json.NewDecoder(response.Body).Decode(&fired); err != nil || fired.RequestID != fakeCallID(0) || fired.CronID != cron || fired.Status != "pending" {
				t.Fatalf("response = %+v %v", fired, err)
			}
			read := serveRehearsalExecution(t, store, http.MethodGet, "/v1/cron-fire-now-requests/"+fired.RequestID, "", "")
			var request api.FireCronRequestResponse
			if err := json.NewDecoder(read.Body).Decode(&request); err != nil || read.Code != http.StatusOK || request.CronID != cron || request.Status != "pending" ||
				request.RequestedAt == "" || request.FinishedAt != nil || request.TaskID != nil {
				t.Fatalf("read = %d %+v %v", read.Code, request, err)
			}
		})
	}
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
	first := serveRehearsalExecution(t, store, http.MethodPost, "/v1/crons/"+cron+"/run", "", "k1")
	again := serveRehearsalExecution(t, store, http.MethodPost, "/v1/crons/"+cron+"/run", "", "k1")
	if first.Code != http.StatusAccepted || again.Header().Get("Idempotent-Replayed") != "true" || len(store.calls) != 1 {
		t.Fatalf("keyed fire-now replay: %d %v, %d captures", again.Code, again.Header(), len(store.calls))
	}
	for _, path := range []string{"/v1/cron-fire-now-requests/" + fakeCallID(9), "/v1/cron-fire-now-requests/not-a-capture"} {
		if response := serveRehearsalExecution(t, store, http.MethodGet, path, "", ""); response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", path, response.Code)
		}
	}
}

func TestRehearsalPlatformAPIRefusesJobRuns(t *testing.T) {
	for _, path := range []string{"/v1/jobs/nightly/runs", "/v1/jobs/nightly/operations"} {
		store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
		response := serveRehearsalExecution(t, store, http.MethodPost, path, `{"tasks":1}`, "")
		if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "Jobs unavailable in a rehearsal") || len(store.calls) != 0 {
			t.Fatalf("POST %s = %d %s", path, response.Code, response.Body)
		}
	}
	// Job reads are not served; they reach next.
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
	if response := serveRehearsalExecution(t, store, http.MethodGet, "/v1/jobs/nightly/runs", "", ""); response.Code != http.StatusTeapot {
		t.Fatalf("GET job runs = %d", response.Code)
	}
}
