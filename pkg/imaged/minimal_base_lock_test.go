package imaged

// adr: 435

import (
	"context"
	"testing"
	"time"
)

// Two plain-image deployments staging the minimal base together each
// published a producer for base/base-amd64.ext4; the first one's scan
// publication then failed as stale and failed its deploy (n2, 2026-10-28).
func TestEnsureMinimalBaseWaitsForOtherStaging(t *testing.T) {
	h, _, _, _, ref := verifiedBaseFixtureOf(t, "complete", "minimal base lock")
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	env := func(key string) string { return map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": ref}[key] }
	h.runtimeBaseMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := h.EnsureMinimalBase(t.Context(), "amd64", env)
		done <- err
	}()
	select {
	case err := <-done:
		h.runtimeBaseMu.Unlock()
		t.Fatalf("EnsureMinimalBase returned %v while another staging held the lock", err)
	case <-time.After(200 * time.Millisecond):
	}
	h.runtimeBaseMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("EnsureMinimalBase after the lock was released: %v", err)
	}
}
