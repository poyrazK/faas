// adr: 696
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
)

func TestDeploymentActivityStartsAtDispatchAndWaitsForForwardReturn(t *testing.T) {
	tracker, err := activity.New(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := httptest.NewRecorder()
	factory := WithDeploymentActivity(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if w != writer {
				t.Error("original writer replaced")
			}
			w.(http.Flusher).Flush()
			close(entered)
			<-release
		})
	}, tracker)
	forward := factory(target)
	if got := tracker.Observe(target.AppID, target.DeploymentID); got.ActiveForwards != 0 || got.ActivityVersion != 1 {
		t.Fatalf("target selection counted as dispatch: %+v", got)
	}
	go func() {
		defer close(finished)
		forward.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(ctx))
	}()
	<-entered
	cancel()
	if got := tracker.Observe(target.AppID, target.DeploymentID); got.ActiveForwards != 1 || !got.CoverageKnown {
		t.Errorf("cancellation before pump return dropped activity: %+v", got)
	}
	close(release)
	<-finished
	if got := tracker.Observe(target.AppID, target.DeploymentID); got.ActiveForwards != 0 || !got.CoverageKnown || got.ActivityVersion != 3 {
		t.Fatalf("completed forward = %+v", got)
	}
}

func TestDeploymentActivityPanicUnwindsAndUnknownIdentityPreservesResponse(t *testing.T) {
	tracker, err := activity.New(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
	wrapped := WithDeploymentActivity(func(Target) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("forward failure") })
	}, tracker)(target)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("forward panic was swallowed")
			}
		}()
		wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if got := tracker.Observe(target.AppID, target.DeploymentID); got.ActiveForwards != 0 || !got.CoverageKnown {
		t.Fatalf("panic retained activity: %+v", got)
	}
	writer := httptest.NewRecorder()
	WithDeploymentActivity(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	}, tracker)(Target{}).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if writer.Code != http.StatusAccepted || tracker.Observe(target.AppID, target.DeploymentID).CoverageKnown {
		t.Fatal("missing identity changed traffic behavior or claimed complete coverage")
	}
}

func TestDeploymentActivityDisabledPreservesFactory(t *testing.T) {
	if WithDeploymentActivity(nil, nil) != nil {
		t.Fatal("disabled wrapper changed nil factory")
	}
	writer := httptest.NewRecorder()
	WithDeploymentActivity(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}, nil)(Target{}).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if writer.Code != http.StatusNoContent {
		t.Fatal("disabled tracking changed response")
	}
}
