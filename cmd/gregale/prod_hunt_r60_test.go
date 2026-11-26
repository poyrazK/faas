package main_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r60: `trigger get transforms` sent the slug `trigger list`
// shows as the ID ("Bad trigger id: invalid UUID length: 10").
func TestTriggerCommandsAcceptTheListedSlug(t *testing.T) {
	const id = "5a34687c-0c34-48c0-a912-ea71a9d151ae"
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/v1/triggers":
			_ = json.NewEncoder(w).Encode([]api.Trigger{
				{ID: id, Slug: "transforms", Kind: api.TriggerKindQueue},
				{ID: "11111111-1111-4111-8111-111111111111", Slug: "dup"},
				{ID: "22222222-2222-4222-8222-222222222222", Slug: "dup"},
			})
			return true
		case "/v1/triggers/" + id:
			_ = json.NewEncoder(w).Encode(api.Trigger{ID: id, Slug: "transforms", Kind: api.TriggerKindQueue})
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "trigger", "get", "transforms")
	if code != 0 || !slices.Contains(*paths, "GET /v1/triggers/"+id) {
		t.Fatalf("trigger get <slug> = %d (%v):\n%s%s", code, *paths, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "trigger", "get", "dup")
	if code == 0 || !strings.Contains(stdout+stderr, "names 2 triggers") {
		t.Fatalf("ambiguous slug = %d:\n%s%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "trigger", "get", "nope")
	if code == 0 || !strings.Contains(stdout+stderr, `no trigger has slug or ID "nope"`) {
		t.Fatalf("unknown slug = %d:\n%s%s", code, stdout, stderr)
	}
}

// Prod hunt r60 (rc.381): unknown-verb errors listed the dispatchers' own
// names, not the ones help lists: `org agent x` offered "ls, info, rm, keys,
// agent-policy, seat-usage"; `insight list` offered "events|...|requests";
// `pipeline bogus` offered "indexes, evolve, operations".
func TestUnknownVerbErrorsSpellHelpNames(t *testing.T) {
	linkedAppServer(t, nil)
	for _, tc := range []struct {
		args       []string
		want, deny []string
	}{
		{[]string{"org", "agent", "x"}, []string{"gregale org: unknown subcommand", "agent"}, []string{"ls,", "info,", "agent-policy", "seat-usage", "gregale orgs:"}},
		{[]string{"insight", "list", "--project", "p"}, []string{"event|funnel|retention|request|cohorts|saved"}, []string{"events|", "requests"}},
		{[]string{"pipeline", "bogus"}, []string{"index, evolution, work"}, []string{"indexes", "evolve", "operations"}},
	} {
		code, stdout, stderr := runCLI(t, tc.args...)
		out := stdout + stderr
		if code == 0 {
			t.Fatalf("%v succeeded:\n%s", tc.args, out)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("%v: missing %q in:\n%s", tc.args, want, out)
			}
		}
		for _, deny := range tc.deny {
			if strings.Contains(out, deny) {
				t.Errorf("%v: still says %q in:\n%s", tc.args, deny, out)
			}
		}
	}
}
