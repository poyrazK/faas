//go:build metal && queue_binding_native

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func queueNativeWait(t *testing.T, label string, ready func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", label)
		case <-ticker.C:
		}
	}
}

func (j *queueNativeJourney) appPath() string { return "/v1/apps/" + j.slug }

func (j *queueNativeJourney) request(t *testing.T, method, path string, value any, want int) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, j.h.APIDURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+j.key)
	req.Header.Set("Content-Type", "application/json")
	response, err := j.h.HTTPClient().Do(req)
	if err != nil {
		t.Fatal("queue acceptance API request failed")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != want {
		t.Fatalf("queue API %s %s status=%d want=%d", method, path, response.StatusCode, want)
	}
	return body
}

func (j *queueNativeJourney) count(t *testing.T) int {
	t.Helper()
	var count int
	if err := j.pool.QueryRow(t.Context(), `SELECT count(*) FROM invocations WHERE app_id=$1`, j.appID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (j *queueNativeJourney) deploy(t *testing.T, artifact string) {
	t.Helper()
	annotations := api.DeployAnnotations{OverrideEnv: map[string]string{api.QueueCanaryCapabilityEnv: `["default"]`}}
	if j.deployment != "" {
		annotations.TrafficPercent = new(int)
	}
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	created, err := j.client.DeployMultipart(ctx, j.slug, bytes.NewReader(j.source), "source.tar.gz", "node22", "handler.handler", false, annotations)
	if err != nil || created.ID == "" {
		t.Fatal("queue source deployment failed")
	}
	id := created.ID
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, j.pool, id, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal(err)
	}
	j.deployment = uuid.MustParse(id).String()
	app, err := j.store.AppByID(ctx, j.appID)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := j.store.DeploymentByID(ctx, j.deployment)
	if err != nil {
		t.Fatal(err)
	}
	build, err := j.store.BuildByDeployment(ctx, j.deployment)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := j.store.BuildProvenanceByBuildID(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	release, err := j.store.RuntimeReleaseForArtifact(ctx, app.AccountID, dep.RootfsKey)
	if err != nil {
		t.Fatal("queue function runtime was not published")
	}
	if provenance.RuntimeBaseRef != release.SourceRef {
		t.Fatal("function build and deployed runtime base disagree")
	}
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	paths, err := e2etest.QueueGuestAppArtifacts(root, j.sourcePath, app, dep, build, provenance, release)
	if err != nil {
		t.Fatal(err)
	}
	paths[artifact] = paths["app_layer"]
	delete(paths, "app_layer")
	for name, path := range paths {
		digest, err := e2etest.PostgresGuestArtifactDigest(path)
		if err != nil {
			t.Fatal(err)
		}
		if previous := j.manifest.Artifacts[name]; previous != "" && previous != digest {
			t.Fatal("queue source or shared runtime changed between deployments")
		}
		j.artifacts[name], j.manifest.Artifacts[name] = path, digest
	}
	if j.manifest.Artifacts["node22_base"] != release.BaseSHA256 || j.manifest.Artifacts["guest_init"] != release.GuestInitSHA256 {
		t.Fatal("published function runtime bytes do not match executed artifacts")
	}
}

func (j *queueNativeJourney) waitHealthy(t *testing.T) {
	t.Helper()
	queueNativeWait(t, "healthy queue consumer", func(ctx context.Context) bool {
		inventory, err := j.client.GetAppBindingInventoryForDeployment(ctx, j.slug, "default", j.deployment)
		if err != nil || !inventory.Complete {
			return false
		}
		for _, binding := range inventory.Bindings {
			if binding.Type == api.BindingTypeQueue && binding.Binding == "default" {
				return binding.QueueCanarySupported && binding.ConsumerState == "active" && binding.ConsumerLiveness == "healthy"
			}
		}
		return false
	})
}

func (j *queueNativeJourney) park(t *testing.T) {
	t.Helper()
	// The first prime already parks. A later call explicitly parks the current
	// resident; historical parked rows never count as a fully parked app.
	instances, err := j.store.ListInstancesForApp(t.Context(), j.appID)
	if err != nil {
		t.Fatal(err)
	}
	resident := false
	for _, instance := range instances {
		resident = resident || state.State(instance.State).CountsForRAM()
	}
	if resident {
		j.request(t, http.MethodPost, j.appPath()+"/park", nil, http.StatusNoContent)
	}
	if _, err := e2etest.WaitForAppParked(t.Context(), t, j.pool, j.appID, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	_ = waitAfterRestoreSnapshot(t, j.store, j.deployment, "")
}

func (j *queueNativeJourney) createCanary(t *testing.T, ttl int) api.QueueBindingCanary {
	t.Helper()
	canary, err := j.client.CreateQueueBindingCanary(t.Context(), j.slug, api.CreateQueueBindingCanaryRequest{DeploymentID: j.deployment, Binding: "default", TTLSeconds: ttl, PublishConsent: true})
	if err != nil || canary.ID == "" {
		t.Fatal("queue canary publication failed")
	}
	j.owned[canary.ID] = true
	return canary
}

func (j *queueNativeJourney) waitCanary(t *testing.T, id, want string) api.QueueBindingCanary {
	t.Helper()
	return j.waitCanaryFor(t, id, want, j.deployment)
}

func (j *queueNativeJourney) waitCanaryFor(t *testing.T, id, want, deployment string) api.QueueBindingCanary {
	t.Helper()
	var result api.QueueBindingCanary
	queueNativeWait(t, "queue canary "+want, func(ctx context.Context) bool {
		canary, err := j.client.GetQueueBindingCanary(ctx, j.slug, id)
		if err != nil {
			return false
		}
		result = canary
		if want == "succeeded" && (canary.Status == "failed" || canary.Status == "blocked" || canary.Status == "expired" || canary.Status == "cancelled") {
			t.Fatalf("queue canary failed: %s", canary.Status)
		}
		return canary.Status == want
	})
	if want == "succeeded" {
		invocation, err := j.store.InvocationByID(t.Context(), id)
		if err != nil || invocation.State != state.InvocationCompleted || invocation.Attempts != 1 || invocation.ReplayGeneration != 0 || result.DeploymentID != deployment || result.CompletedAt == nil || result.DeliveredAt == nil {
			t.Fatal("queue completion lacks original attempt and exact-deployment acknowledgement")
		}
	}
	return result
}

func (j *queueNativeJourney) requireRestore(t *testing.T) {
	t.Helper()
	instances, err := j.store.ListInstancesForApp(t.Context(), j.appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.DeploymentID == j.deployment && state.State(instance.State) == state.StateRunning {
			if _, err := e2etest.WaitForWakeMethod(t.Context(), t, j.pool, instance.WakeID, "restore", 10*time.Second); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("completed queue delivery has no restored serving instance")
}

func (j *queueNativeJourney) cancel(t *testing.T, id string) {
	t.Helper()
	result, err := j.client.CancelQueueBindingCanary(t.Context(), j.slug, id)
	if err != nil || result.Status != "cancelled" || result.CompletedAt != nil {
		t.Fatal("cancellation retained queue completion authority")
	}
}

func (j *queueNativeJourney) promote(t *testing.T, want bool) {
	t.Helper()
	before, err := j.store.DeploymentByID(t.Context(), j.deployment)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	if !want {
		status = http.StatusConflict
	}
	body := j.request(t, http.MethodPost, "/v1/deployments/"+j.deployment+"/promote", api.BindingPromotionRequest{}, status)
	if want {
		var out api.BindingPromotionResponse
		if json.Unmarshal(body, &out) != nil || out.BindingsCheck == nil || !out.BindingsCheck.Passed || out.ToPercent != 100 {
			t.Fatal("release did not require fresh queue completion evidence")
		}
	} else {
		var problem api.Problem
		if json.Unmarshal(body, &problem) != nil || problem.BindingsCheck == nil || problem.BindingsCheck.Passed {
			t.Fatal("release rejection lacked failed binding evidence")
		}
		found := false
		for _, blocker := range problem.BindingsCheck.Blockers {
			found = found || blocker.Code == "queue_completion_unverified" || blocker.Code == "queue_completion_expired"
		}
		if !found {
			t.Fatal("release was rejected for an unrelated reason")
		}
		after, err := j.store.DeploymentByID(t.Context(), j.deployment)
		if err != nil || after.TrafficPercent != before.TrafficPercent {
			t.Fatal("failed queue evidence changed routing")
		}
	}
}
