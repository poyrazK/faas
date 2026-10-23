// adr: 20261023111825
package rehearsal

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestQueueDeliveryAdmissionIsGatedAndPlain(t *testing.T) {
	contained := api.RegisterScenarioTestRequest{NetworkMode: "contained", QueueDelivery: api.ScenarioQueueDeliverySameRun}
	for _, tc := range []struct {
		name    string
		req     api.RegisterScenarioTestRequest
		enabled bool
		status  int
	}{
		{"not requested", api.RegisterScenarioTestRequest{}, false, 0},
		{"same run", contained, true, 0},
		{"operator gate off", contained, false, http.StatusServiceUnavailable},
		{"unknown mode", api.RegisterScenarioTestRequest{NetworkMode: "contained", QueueDelivery: "everywhere"}, true, http.StatusBadRequest},
		{"capture only", api.RegisterScenarioTestRequest{QueueDelivery: api.ScenarioQueueDeliverySameRun}, true, http.StatusBadRequest},
		{"managed work", api.RegisterScenarioTestRequest{NetworkMode: "contained", QueueDelivery: api.ScenarioQueueDeliverySameRun, ManagedWork: &rehearsalwork.Plan{}}, true, http.StatusBadRequest},
		{"suspend resume", api.RegisterScenarioTestRequest{NetworkMode: "contained", QueueDelivery: api.ScenarioQueueDeliverySameRun, SuspendResume: &rehearsalresume.Spec{}}, true, http.StatusBadRequest},
	} {
		problem := ValidateQueueDelivery(tc.req, tc.enabled)
		if status := 0; problem != nil {
			status = problem.Status
			if status != tc.status {
				t.Errorf("%s: status %d, want %d", tc.name, status, tc.status)
			}
		} else if tc.status != 0 {
			t.Errorf("%s: admitted, want %d", tc.name, tc.status)
		}
	}
}
