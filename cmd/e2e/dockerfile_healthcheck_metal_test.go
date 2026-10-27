//go:build metal

// dockerfile_healthcheck_metal_test.go — qualification for
// ADR-20261024100000 on a source-built image (ADR-20261026152731): a
// Dockerfile's command HEALTHCHECK gates readiness. Before, it was recorded
// but ignored for source builds; the native lane was still pending.
//
//   - passing: the check succeeds, the deployment goes live, and its profile
//     records image_healthcheck_required=true;
//   - failing: the check always fails, so the deployment never serves and
//     fails instead of going live on a bare TCP accept.
//
// Requires the TestBuildMetal environment: /dev/kvm + root, Firecracker,
// FAAS_TEST_KERNEL and FAAS_BUILDER_BASE_PATH.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// dockerfileHealthcheckFixture is DockerfileFixture with a HEALTHCHECK.
func dockerfileHealthcheckFixture(t *testing.T, check string) []byte {
	t.Helper()
	dockerfile := "FROM busybox:1.37\n" +
		"LABEL faas.build.token=\"" + time.Now().UTC().Format(time.RFC3339Nano) + "\"\n" +
		"RUN mkdir -p /public && printf 'hello from faas (healthcheck fixture)\\n' > /public/index.html && printf 'ready\\n' > /public/healthz\n" +
		"RUN adduser -D -u 1000 app\n" +
		"USER app\n" +
		"EXPOSE 3000\n" +
		"HEALTHCHECK --interval=2s --timeout=2s --retries=2 CMD " + check + "\n" +
		"CMD [\"/bin/busybox\", \"httpd\", \"-f\", \"-p\", \"3000\", \"-h\", \"/public\"]\n"
	return buildTarGz(t, map[string]string{"Dockerfile": dockerfile, ".faas-fixture": "dockerfile-healthcheck\n"})
}

func TestDockerfileHealthcheckMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal Dockerfile healthcheck test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping (no builder-base.ext4 path)")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	t.Run("passing-check-goes-live", func(t *testing.T) {
		result := runBuildSubtest(t, h, pool, key, "hcpass", "hc-pass",
			dockerfileHealthcheckFixture(t, "wget -q -O /dev/null http://127.0.0.1:3000/healthz || exit 1"), true)
		assertHostingRuntime(t, h, pool, result, "docker", "hello from faas (healthcheck fixture)\n")
		dep, err := state.NewPgStore(pool).DeploymentByID(context.Background(), result.deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		var profile struct {
			Required bool `json:"image_healthcheck_required"`
		}
		if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil || !profile.Required {
			t.Fatalf("profile %s (err=%v): want image_healthcheck_required=true", dep.InferredProfile, err)
		}
	})

	t.Run("failing-check-never-serves", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
		defer cancel()
		public := false
		if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hcfail", Type: "app", RequireAuthn: &public}); got != http.StatusCreated {
			t.Fatalf("create app: status=%d", got)
		}
		body, status := postMultipartDeploymentWithSourceRoot(t, h, key, "hcfail", dockerfileHealthcheckFixture(t, "false"), true, "", "")
		if status != http.StatusAccepted {
			t.Fatalf("create deployment: status=%d body=%s", status, body)
		}
		depID, _ := parseQueuedDeployment(t, body)
		dep, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling)
		if err == nil || dep.Status == state.DeployLive {
			t.Fatalf("deployment with an always-failing HEALTHCHECK = %s (err=%v), want it never to go live", dep.Status, err)
		}
		t.Logf("failing healthcheck outcome: status=%s error=%q", dep.Status, dep.Error)
	})
}
