// adr: 20261104104521
package journeys

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The deployed CLI preserves the request identity after later writes. Equal
// settings keep this journey's resource use unchanged while consuming revisions.
func TestRecoverableScaling(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "scale-recovery")
	var before api.AppScalingSnapshot
	if err := json.Unmarshal(tg.ok(t, time.Minute, "app", "scaling", slug).stdout, &before); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	args := []string{"app", "scaling", slug, "--request-id", requestID, "--expected-revision", strconv.FormatInt(before.Revision, 10), "--expected-app-id", before.AppID, "--ram", strconv.Itoa(before.Settings.RAMMB)}
	var first api.AppScalingReceipt
	if err := json.Unmarshal(tg.ok(t, time.Minute, args...).stdout, &first); err != nil {
		t.Fatal(err)
	}
	if first.RequestID != requestID || first.AppID != before.AppID || first.Revision <= before.Revision {
		t.Fatalf("invalid commit receipt: %+v", first)
	}
	tg.ok(t, time.Minute, "app", "scaling", slug, "--request-id", uuid.NewString(), "--expected-revision", strconv.FormatInt(first.Revision, 10), "--expected-app-id", before.AppID, "--ram", strconv.Itoa(before.Settings.RAMMB))
	for _, replay := range [][]string{args, {"app", "scaling", slug, "--receipt", requestID}} {
		var receipt api.AppScalingReceipt
		if err := json.Unmarshal(tg.ok(t, time.Minute, replay...).stdout, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt != first {
			t.Fatalf("recovery changed the original receipt: %+v", receipt)
		}
	}
	stale := append([]string(nil), args...)
	stale[4] = uuid.NewString()
	if out := tg.run(t, time.Minute, stale...); out.exit == 0 || !strings.Contains(string(out.stderr)+string(out.stdout), "scaling_revision_conflict") {
		t.Fatalf("stale write not refused: %s %s", out.stdout, out.stderr)
	}
	var current api.AppScalingSnapshot
	if err := json.Unmarshal(tg.ok(t, time.Minute, "app", "scaling", slug).stdout, &current); err != nil {
		t.Fatal(err)
	}
	if current.Revision <= first.Revision || current.Settings != before.Settings {
		t.Fatalf("recovery replaced current state: %+v", current)
	}
	public := fetch(http.MethodGet, base+"/", nil)
	if public.status != http.StatusOK || !strings.Contains(string(public.body), "gregale-e2e-probe") {
		t.Fatalf("public probe after scaling recovery: %s", public)
	}
}
