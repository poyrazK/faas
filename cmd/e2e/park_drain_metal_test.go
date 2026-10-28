//go:build metal

package e2e_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest"
)

// parkDrainPending is apid's retryable answer when a park's drain outlasts
// its five-second wait (pkg/apid/apidserver/park_drain.go).
const parkDrainPending = "app instances did not drain before the park deadline"

// parkApp parks slug the way api.Client.park does. The first POST commits
// evicted_cold; a snapshot that takes longer than apid's drain wait answers
// the retryable 503 above, which does not mean the park failed, so it is
// repeated for up to a minute. TestAfterRestoreMetal failed on every
// nested-KVM run because a 512 MB capture took 7 s and the test treated
// that 503 as final (container qualification 2026-10-27).
func parkApp(t *testing.T, h *e2etest.Harness, key, slug string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil)
		if status == http.StatusNoContent {
			return
		}
		if status != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(parkDrainPending)) || time.Now().After(deadline) {
			t.Fatalf("park %s: status=%d body=%s", slug, status, body)
		}
		time.Sleep(time.Second)
	}
}
