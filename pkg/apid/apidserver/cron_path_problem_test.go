package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A cron path must be a request path on the cron's own app. Create and
// update both refuse a host-relative or malformed path, and a refused
// update leaves the stored path alone.
func TestCronPathIsValidated(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "cron-path")
	wantProblem := func(t *testing.T, code int, body []byte) {
		t.Helper()
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", code, body)
		}
		var problem api.Problem
		if err := json.Unmarshal(body, &problem); err != nil || problem.Code != api.CodeCronInvalid {
			t.Fatalf("problem = %+v (%v), want cron_invalid", problem, err)
		}
	}
	for _, path := range []string{"//evil.example/x", "jobs", "/a b", "/a#b"} {
		rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 3 * * *", Path: path}, nil)
		wantProblem(t, rec.Code, rec.Body.Bytes())
	}

	rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 3 * * *", Path: "/jobs/sync?full=1"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with a query string: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	bad := "//evil.example/x"
	rec = e.Do(t, http.MethodPatch, "/v1/crons/"+created.ID, api.UpdateCronRequest{Path: &bad}, nil)
	wantProblem(t, rec.Code, rec.Body.Bytes())
	rec = e.Do(t, http.MethodGet, "/v1/crons/"+created.ID, nil, nil)
	var got api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Path != "/jobs/sync?full=1" {
		t.Fatalf("after a refused update: path = %q (%v), want it unchanged", got.Path, err)
	}
}
