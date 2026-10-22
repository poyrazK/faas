package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021152201 — `--at` takes the times people type, and the CLI
// schedules, shows and cancels releases.

func TestParseReleaseAtFlag(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	now := time.Date(2026, 10, 21, 22, 30, 0, 0, zone)
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"now", now},
		{"+2h30m", now.Add(150 * time.Minute)},
		{"2026-10-22T02:00:00Z", time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)},
		{"2026-10-22 09:00", time.Date(2026, 10, 22, 9, 0, 0, 0, zone)},
		{"23:00", time.Date(2026, 10, 21, 23, 0, 0, 0, zone)},
		// A clock time that has passed today means tomorrow.
		{"02:00", time.Date(2026, 10, 22, 2, 0, 0, 0, zone)},
	} {
		got, err := main.ParseReleaseAtFlag(tc.in, now)
		if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
			t.Fatalf("ParseReleaseAtFlag(%q) = %v, %v; want %v in UTC", tc.in, got, err, tc.want.UTC())
		}
	}
	for _, bad := range []string{"", "tomorrow", "+-1h", "+soon", "25:00"} {
		if _, err := main.ParseReleaseAtFlag(bad, now); err == nil {
			t.Fatalf("ParseReleaseAtFlag(%q) accepted", bad)
		}
	}
}

func TestResolveDeployReleaseAt(t *testing.T) {
	if at, notify, err := main.ResolveDeployReleaseAt("", "", map[string]bool{}, false, false); at != "" || notify != nil || err != nil {
		t.Fatalf("no --at = %q %v %v", at, notify, err)
	}
	if _, _, err := main.ResolveDeployReleaseAt("", "releases", map[string]bool{}, false, false); err == nil {
		t.Fatal("--release-notify without --at accepted")
	}
	if _, _, err := main.ResolveDeployReleaseAt("+1h", "", map[string]bool{}, true, false); err == nil {
		t.Fatal("--at with --no-traffic accepted")
	}
	if _, _, err := main.ResolveDeployReleaseAt("+1h", "", map[string]bool{"traffic-percent": true}, false, false); err == nil {
		t.Fatal("--at with --traffic-percent accepted")
	}
	if _, _, err := main.ResolveDeployReleaseAt("+1h", "", map[string]bool{}, false, true); err == nil {
		t.Fatal("--at with a project deploy accepted")
	}
	at, notify, err := main.ResolveDeployReleaseAt("+1h", "releases, oncall", map[string]bool{}, false, false)
	if err != nil || len(notify) != 2 || notify[1] != "oncall" {
		t.Fatalf("--at +1h = %q %v %v", at, notify, err)
	}
	if parsed, err := time.Parse(time.RFC3339, at); err != nil || parsed.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("release time %q", at)
	}
}

func TestScheduledReleaseLines(t *testing.T) {
	now := time.Date(2026, 10, 21, 22, 0, 0, 0, time.UTC)
	sr := api.ScheduledRelease{App: "shop", DeploymentID: "d42", Revision: 42, ReleaseAt: "2026-10-22T02:00:00Z",
		State: api.ScheduledReleaseStatePending, Canary: &api.CanaryPresetSpec{Preset: api.DefaultReleaseCanaryPreset},
		NotifyChannels: []string{"releases"}, OverrideFreeze: "launch night", OverriddenFreezes: []string{"f1"}}
	out := strings.Join(main.ScheduledReleaseLines(sr, now), "\n")
	for _, want := range []string{"v42 of shop: starts", "(in 4h)", "Rollout: safe canary.", "Notifies: releases",
		"Overrides 1 deploy freeze(s): launch night", "gregale scheduled-releases set v42 --app shop --at <when>",
		"gregale scheduled-releases cancel v42 --app shop"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	sr.State, sr.OutcomeDetail = api.ScheduledReleaseStateBlocked, "Production releases of shop are frozen"
	if out := strings.Join(main.ScheduledReleaseLines(sr, now), "\n"); !strings.Contains(out, "State: blocked (Production releases of shop are frozen)") ||
		strings.Contains(out, "Cancel:") {
		t.Fatalf("blocked output:\n%s", out)
	}
}

func TestScheduledReleasesSetSendsTheTimeAndRollout(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	var got api.ScheduleReleaseRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/deployments":
			main.WriteJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: id, Revision: 42, Status: main.StatusLive}}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/deployments/"+id+"/schedule":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode schedule request: %v", err)
			}
			main.WriteJSONTest(w, api.ScheduledRelease{App: "shop", DeploymentID: id, Revision: 42, ReleaseAt: got.ReleaseAt,
				State: api.ScheduledReleaseStatePending, Canary: got.Canary, NotifyChannels: got.NotifyChannels})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := main.CaptureStdout(t)
	defer restore()
	code := main.CmdScheduledReleases([]string{"set", "v42", "--app", "shop", "--at", "2026-10-22T02:00:00Z", "--rollout", "fast", "--notify", "releases"})
	if code != 0 {
		t.Fatalf("set exit = %d", code)
	}
	if got.ReleaseAt != "2026-10-22T02:00:00Z" || got.Canary == nil || got.Canary.Preset != api.FastReleaseCanaryPreset ||
		len(got.NotifyChannels) != 1 || got.NotifyChannels[0] != "releases" {
		t.Fatalf("schedule request = %+v", got)
	}
	if !strings.Contains(stdout.String(), "Scheduled v42 of shop: starts") {
		t.Fatalf("output:\n%s", stdout.String())
	}
}
