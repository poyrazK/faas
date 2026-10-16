//go:build metal

package acceptance

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func (f *nativeFixture) prepareWake(t *testing.T) func(ci.Execution, *Scenario) {
	t.Helper()
	// A different tenant receives the wake; CI must not consume its account
	// concurrency. This exercises CPU contention, not a full-host RAM SLO.
	key := f.h.SeedAccount(t.Context(), api.PlanHobby, "ci-wake")
	client := api.NewClient(f.h.APIDURL, key)
	falsy := false
	app, err := client.CreateApp(t.Context(), api.CreateAppRequest{Slug: "ci-wake-probe", Type: "app", RequireAuthn: &falsy})
	mustNative(t, err)
	image, _ := e2etest.HelloImage("ci-native/wake", "ci-native-wake")
	ref := f.registry.AddImage("ci-native/wake", image)
	deployment, err := client.Deploy(t.Context(), app.Slug, api.CreateDeploymentRequest{Image: ref})
	mustNative(t, err)
	_, err = e2etest.WaitForDeploymentLive(t.Context(), t, f.pool, deployment.ID, 2*time.Minute)
	mustNative(t, err)
	mustNative(t, client.Park(t.Context(), app.Slug))
	_, err = e2etest.WaitForAppParked(t.Context(), t, f.pool, app.ID, time.Minute)
	mustNative(t, err)
	// Preserve a normal API deletion closure instead of putting this tenant's
	// bearer key in the recovery journal or native evidence.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.Park(ctx, app.Slug); err != nil {
			t.Error("native wake fixture park is pending")
		}
		if err := client.DeleteApp(ctx, app.Slug); err != nil {
			t.Error("native wake fixture deletion is pending")
		}
	})
	return func(row ci.Execution, entry *Scenario) {
		f.wakeDuringCI(t, app.Slug, row, entry)
	}
}

func (f *nativeFixture) wakeDuringCI(t *testing.T, slug string, row ci.Execution, entry *Scenario) {
	t.Helper()
	var running int
	nativeEventually(t, time.Minute, "concurrent native CI pressure", func() bool {
		err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM ci_job_attempts a JOIN job_tasks t ON t.run_id=a.run_id
 JOIN instances i ON i.id=t.instance_id WHERE a.execution_id=$1 AND i.state='running' AND t.status='claimed'`, row.ID).Scan(&running)
		return err == nil && running == len(row.Jobs)
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.h.EdgeURL()+"/", nil)
	mustNative(t, err)
	request.Host = slug + ".apps.test.example"
	response, err := (&http.Client{Timeout: time.Minute}).Do(request)
	mustNative(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	mustNative(t, err)
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ci-native-wake" {
		t.Fatal("tenant could not wake and serve while CI guests consumed CPU")
	}
	var stillRunning int
	mustNative(t, f.pool.QueryRow(t.Context(), `SELECT count(*) FROM ci_job_attempts a JOIN job_tasks t ON t.run_id=a.run_id
 JOIN instances i ON i.id=t.instance_id WHERE a.execution_id=$1 AND i.state='running' AND t.status='claimed'`, row.ID).Scan(&stillRunning))
	if stillRunning != len(row.Jobs) {
		t.Fatal("CI pressure disappeared before the tenant response")
	}
	entry.Checks["concurrent_ci"], entry.Checks["tenant_wake"] = true, true
}
