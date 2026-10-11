// adr: 697
package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
)

func TestDeploymentFenceRejectsTargetSelectedBeforeWeightsChanged(t *testing.T) {
	tracker, err := activity.NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
	calls := 0
	factory := WithDeploymentActivity(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) })
	}, tracker)
	selected := factory(target)
	if _, err := tracker.InstallFence(target.AppID, target.DeploymentID, strings.Repeat("a", 64), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	selected.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if calls != 0 || writer.Code != http.StatusServiceUnavailable {
		t.Fatal("late target bypassed fence", calls, writer.Code)
	}
	target.DeploymentID = uuid.NewString()
	writer = httptest.NewRecorder()
	factory(target).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if calls != 1 || writer.Code != http.StatusNoContent {
		t.Fatal("candidate forwarding changed", calls, writer.Code)
	}
}
