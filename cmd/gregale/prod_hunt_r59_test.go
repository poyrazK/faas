package main_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r59 (rc.381): `app deploy-token delete <slug> <id>` printed
// usage although `list <slug>` and `create <slug>` take the app first.
func TestDeployTokenDeleteTakesTheAppFirst(t *testing.T) {
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/web/deploy-tokens/3eb5a046-4542-45df-bd4c-7ebcb6f73240" {
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "app", "deploy-token", "delete", "web", "3eb5a046-4542-45df-bd4c-7ebcb6f73240")
	if code != 0 || !strings.Contains(stdout, "Revoked deploy token 3eb5a046") {
		t.Fatalf("delete <slug> <id> = %d (%v):\n%s%s", code, *paths, stdout, stderr)
	}
}

// Prod hunt r59: in a linked checkout, `app wake-timeline web` asked the
// server for wake "web" ("no events row with that wake_id"). A slug is
// never a wake ID, so the app's latest wake is shown.
func TestWakeTimelineSlugInLinkedCheckoutShowsLatestWake(t *testing.T) {
	const wake = "01a2192d-7fb3-79fa-b697-0a36f5e80f60"
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/v1/apps/web/instances":
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{WakeID: wake, StartedAt: "2026-11-26T18:45:42Z"}})
			return true
		case "/v1/apps/web/wakes/" + wake + "/timeline":
			_ = json.NewEncoder(w).Encode(api.WakeTimelineResponse{})
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "app", "wake-timeline", "web")
	if code != 0 || !slices.Contains(*paths, "GET /v1/apps/web/wakes/"+wake+"/timeline") {
		t.Fatalf("wake-timeline web = %d (%v):\n%s%s", code, *paths, stdout, stderr)
	}
}

// Prod hunt r59: an unknown `app scale` flag pointed at 'gregale app
// --help', which does not list scale's flags.
func TestAppScaleUnknownFlagPointsAtScaleHelp(t *testing.T) {
	linkedAppServer(t, nil)
	for _, args := range [][]string{
		{"app", "scale", "web", "--max", "3"},
		{"app", "web", "scale", "--max", "3"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code == 0 || !strings.Contains(stderr, "gregale app scale") || strings.Contains(stderr, "gregale app --help") {
			t.Fatalf("%v = %d:\n%s%s", args, code, stdout, stderr)
		}
	}
}

// Prod hunt r59: `env requirement` errors named the old verbs
// (set/add/remove, show) and `env rotate KEY` said "secret set must look
// like KEY=VALUE".
func TestEnvRequirementAndRotateMessagesUseCurrentVerbs(t *testing.T) {
	linkedAppServer(t, nil)
	code, stdout, stderr := runCLI(t, "env", "requirement", "bogus")
	if code == 0 || !strings.Contains(stderr, "gregale env requirement set|create|delete") || strings.Contains(stderr, "secrets requirements") {
		t.Fatalf("env requirement bogus = %d:\n%s%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "env", "rotate", "APP_A")
	if code == 0 || !strings.Contains(stderr, "env rotate: expected KEY=VALUE") || strings.Contains(stderr, "secret set") {
		t.Fatalf("env rotate KEY = %d:\n%s%s", code, stdout, stderr)
	}
}
