package e2e_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestE2E_NormalPath_PublicAuthIPAllowlistCLI is the released-binary
// contract for issue #2656. It configures a deny CIDR through the public CLI,
// verifies a real public request is rejected before the parked app wakes, and
// restores open access through the same binary before confirming the app serves.
func TestE2E_NormalPath_PublicAuthIPAllowlistCLI(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "public-auth-cli", api.PlanPro)
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	f.vmmd.SetDefaultVersion("public-auth-cli")

	bin := buildGregale(t)
	// Establish a known-open starting posture even if the account's create
	// defaults change in the future.
	runPublicAuthCLI(t, bin, f.h.APIDURL, f.key, f.app.Slug,
		"--public-auth", api.AppPublicAuthModeOpen)
	runPublicAuthCLI(t, bin, f.h.APIDURL, f.key, f.app.Slug,
		"--public-auth", api.AppPublicAuthModeIPAllowlist,
		"--ip-allowlist", "203.0.113.0/24")

	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	if statusCode != 403 {
		t.Fatalf("denied public request status=%d body=%q, want 403", statusCode, body)
	}
	if got := f.vmmd.ForwardCount(); got != 0 {
		t.Fatalf("denied public request reached the app: forward count=%d, want 0", got)
	}
	if got := len(f.vmmd.RestoreCalls()) + len(f.vmmd.ColdBootCalls()); got != 0 {
		t.Fatalf("denied public request woke the parked app: restore/cold-boot calls=%d, want 0", got)
	}

	runPublicAuthCLI(t, bin, f.h.APIDURL, f.key, f.app.Slug,
		"--public-auth", api.AppPublicAuthModeOpen)
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:public-auth-cli\n", 30*time.Second)
	if got := f.vmmd.ForwardCount(); got == 0 {
		t.Fatal("open public request did not reach the app after restoring open access")
	}
}

func runPublicAuthCLI(t *testing.T, bin, apiURL, token, slug string, flags ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"app", slug}, flags...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(),
		"FAAS_API="+apiURL,
		"FAAS_TOKEN="+token,
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gregale %v: %v: %s", args, err, output)
	}
}
