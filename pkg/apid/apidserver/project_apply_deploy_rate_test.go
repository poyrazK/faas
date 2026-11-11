// adr: 20261105095300
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A project deploy whose services do not all fit the hourly deploy budget
// deploys none of them and spends none of the budget: deploying until the
// budget ran out left the project on mixed releases.
func TestProjectApplyOverDeployRateDeploysNoService(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	limit := api.PlanPro.DeploysPerHour()
	for range limit - 1 {
		if _, err := store.ConsumeAccountDeployRate(t.Context(), acct.ID, limit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if err := mw.WriteField("project_slug", "rate-shop"); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n  api:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n"
	if _, err := part.Write(environmentProjectArchive(t, compose)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/projects", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+*e.Key())
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Builds) != 2 {
		t.Fatalf("builds = %+v, want one refusal per service", out.Builds)
	}
	for _, build := range out.Builds {
		if build.DeploymentID != "" || !strings.Contains(build.Error, "needs 2 deploys") {
			t.Fatalf("build %+v, want refused for the deploy budget", build)
		}
	}
	rate, err := store.ReadAccountDeployRate(t.Context(), acct.ResourceOwnerID(), limit, time.Now().UTC())
	if err != nil || rate.Remaining != 1 {
		t.Fatalf("remaining = %d, %v; want the last deploy unspent", rate.Remaining, err)
	}
}
