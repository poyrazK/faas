package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261022140000 — the break-glass release pause refuses every new
// release, whatever the request carries, and leaves rollbacks alone.

func TestPlatformReleasePauseRefusesReleasesButNotRollbacks(t *testing.T) {
	f := newPostReleaseFixture(t, "pause-app", true, time.Hour)
	e := f.e
	pause := func(on bool) {
		t.Helper()
		if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, json.RawMessage(strconv.FormatBool(on))); err != nil {
			t.Fatal(err)
		}
	}

	pause(true)
	for name, req := range map[string]api.CreateDeploymentRequest{
		"plain deploy":         {Image: imageRef('a')},
		"freeze override":      {Image: imageRef('b'), OverrideFreeze: "hotfix for incident 4711"},
		"scheduled for later":  {Image: imageRef('c'), ReleaseAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		"non-production scope": {Image: imageRef('d'), Scope: "staging"},
	} {
		rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/deployments", req, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s while paused = %d %s, want 503", name, rec.Code, rec.Body)
		}
		if p := decodeProblem(t, rec.Body.Bytes()); p.Code != api.CodePlatformReleasePaused {
			t.Fatalf("%s problem = %+v", name, p)
		}
	}

	target := f.previous.ID
	rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/rollback", api.RollbackRequest{TargetDeploymentID: &target, Reason: "incident containment"}, nil)
	if rec.Code == http.StatusServiceUnavailable || decodeProblemCode(rec.Body.Bytes()) == api.CodePlatformReleasePaused {
		t.Fatalf("rollback was refused by the release pause: %d %s", rec.Code, rec.Body)
	}

	pause(false)
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/pause-app/deployments", api.CreateDeploymentRequest{Image: imageRef('e')}, nil); rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("deploy after the pause was lifted = %d %s", rec.Code, rec.Body)
	}
}

func decodeProblemCode(body []byte) string {
	var p api.Problem
	_ = json.Unmarshal(body, &p)
	return p.Code
}
