package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-20: an HTTP app can never consume a queue, but each binding
// attempt was refused for a different reason. One message now says why.
func TestQueueBindingOnHTTPAppSaysItCannotConsume(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-http-app", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP,
	}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []api.CreateQueueBindingRequest{
		{Name: "jobs", QueueName: "jobs", MaxConcurrency: 1},
		{Name: "jobs", QueueName: "jobs", MaxConcurrency: 1, Mode: "push", WorkloadClass: "http"},
	} {
		rec := e.Do(t, http.MethodPost, "/v1/apps/queue-http-app/queue-bindings", req, nil)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "an HTTP app cannot consume a queue") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	}
}
