// Hunt 15: a job whose memory cannot boot a guest fails every attempt after a
// cold boot, so the API refuses it.
package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestJobRAMBelowTheGuestMinimumIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	create := func(name string, ramMB int) *http.Response {
		rec := e.Do(t, "POST", "/v1/jobs", api.CreateJobRequest{Name: name, Kind: "batch", ImageRef: "alpine:latest",
			Command: []string{"/bin/true"}, RAMMB: ramMB}, nil)
		return rec.Result()
	}
	for _, ramMB := range []int{1, api.JobMinRAMMB - 1} {
		rec := e.Do(t, "POST", "/v1/jobs", api.CreateJobRequest{Name: "tiny-job", Kind: "batch", ImageRef: "alpine:latest",
			Command: []string{"/bin/true"}, RAMMB: ramMB}, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ram_mb must be at least") {
			t.Fatalf("ram_mb=%d: %d %s; want 400 naming the minimum", ramMB, rec.Code, rec.Body)
		}
	}
	if res := create("smallest-job", api.JobMinRAMMB); res.StatusCode != http.StatusCreated {
		t.Fatalf("ram_mb=%d: status %d; want 201", api.JobMinRAMMB, res.StatusCode)
	}
	if res := create("default-job", 0); res.StatusCode != http.StatusCreated {
		t.Fatalf("omitted ram_mb: status %d; want 201 with the default", res.StatusCode)
	}
	rec := e.Do(t, "PATCH", "/v1/jobs/smallest-job", map[string]any{"ram_mb": 8}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ram_mb must be at least") {
		t.Fatalf("PATCH ram_mb=8: %d %s; want 400 naming the minimum", rec.Code, rec.Body)
	}
}
