package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsIncidentContextReadAndValidation(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	store := *e.Store()
	a, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "context-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Status: state.DeployFailed})
	if err != nil {
		t.Fatal(err)
	}
	trace := "11111111111111111111111111111111"
	if err := store.AppendEventWithTrace(ctx, "private-actor", "deployment.failed", &d.ID, []byte(`{"secret":"private-payload"}`), &trace); err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/obs/incidents/deployment/" + d.ID + "/context"
	rec := e.DoOperator(t, "GET", path+"?limit=1&history_limit=1", nil, nil)
	var out api.ObsIncidentContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || out.Counts == nil || out.Counts.Apps != 1 || out.Counts.FailedResources != 1 || out.NextCursor == "" || len(out.Resources) != 1 || len(out.Timeline) != 1 || out.Timeline[0].Entry.TracePath == "" || strings.Contains(rec.Body.String(), "private-") {
		t.Fatalf("context: %d %s", rec.Code, rec.Body.String())
	}
	next := e.DoOperator(t, "GET", path+"?limit=1&cursor="+out.NextCursor, nil, nil)
	if next.Code != 200 {
		t.Fatalf("next: %d %s", next.Code, next.Body.String())
	}
	for _, query := range []string{"?limit=101", "?history_limit=0", "?cursor=garbage"} {
		rec = e.DoOperator(t, "GET", path+query, nil, nil)
		if rec.Code != 400 {
			t.Fatalf("bad query=%s status=%d", query, rec.Code)
		}
	}
	for _, ref := range []struct {
		kind, id string
		status   int
	}{{"deployment", "not-uuid", 400}, {"app", a.ID, 400}, {"deployment", uuid.NewString(), 404}, {"platform_alert", strings.Repeat("a", 64), 404}} {
		rec = e.DoOperator(t, "GET", "/v1/admin/obs/incidents/"+ref.kind+"/"+ref.id+"/context", nil, nil)
		if rec.Code != ref.status {
			t.Fatalf("reference %s: %d %s", ref.kind, rec.Code, rec.Body.String())
		}
	}
}

type obsContextFailureStore struct{ Store state.Store }

func (s *obsContextFailureStore) OperatorIncidentContext(context.Context, state.OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	return api.ObsIncidentContextResponse{}, errors.New("private-database-error")
}

func TestObsIncidentContextFailClosedAndAuthorization(t *testing.T) {
	e := gapEnv(t)
	*(*e.S()).Store() = statetest.NewStore(&obsContextFailureStore{Store: *(*e.S()).Store()})
	path := "/v1/admin/obs/incidents/compute_node/" + uuid.NewString() + "/context"
	rec := e.DoOperator(t, http.MethodGet, path, nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private-") {
		t.Fatalf("database error: %d %s", rec.Code, rec.Body.String())
	}
	for _, scopes := range [][]string{api.ScopesReadSurface, api.ScopesAdminOnly} {
		customer := apidserver.NewObsEnv(t, scopes, "ops@faas.dev", "customer@faas.dev")
		rec = customer.Do(t, http.MethodGet, path, nil, nil)
		if rec.Code != 403 {
			t.Fatalf("authorization: %d %s", rec.Code, rec.Body.String())
		}
	}
}
