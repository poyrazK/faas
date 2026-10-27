package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022105846 — --penalty-for/--penalty-after/--penalty-within set
// a rule's penalty box on create and update, --clear-penalty removes it, and
// inconsistent flags fail before any request.
func TestEdgeRulesPenaltyFlags(t *testing.T) {
	main.ResetJSONOut(t)
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = io.Discard
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	f := main.AuthedFakeAPI(t, `{"id":"r1","kind":"ip"}`, http.StatusCreated)
	if code := main.CmdEdgeRules([]string{"create", "--app", "demo", "--kind", "ip", "--match-host", "demo.example.com",
		"--ip-deny", "203.0.113.0/24", "--penalty-for", "10m", "--penalty-after", "3", "--penalty-within", "30s"}); code != 0 {
		t.Fatalf("create: exit = %d", code)
	}
	var create api.CreateEdgeRuleRequest
	if err := json.Unmarshal((*f.SawBody()), &create); err != nil {
		t.Fatal(err)
	}
	if create.Penalty == nil || *create.Penalty != (api.EdgeRulePenalty{Seconds: 600, After: 3, WithinSeconds: 30}) {
		t.Fatalf("create penalty = %+v (body %s)", create.Penalty, (*f.SawBody()))
	}

	f = main.AuthedFakeAPI(t, `{"id":"r1","kind":"ip"}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"update", "r1", "--clear-penalty"}); code != 0 {
		t.Fatalf("update: exit = %d", code)
	}
	var update api.UpdateEdgeRuleRequest
	if err := json.Unmarshal((*f.SawBody()), &update); err != nil || !update.ClearPenalty || update.Penalty != nil {
		t.Fatalf("update body = %s", (*f.SawBody()))
	}

	for name, args := range map[string][]string{
		"after without for": {"create", "--app", "demo", "--kind", "ip", "--match-host", "demo.example.com", "--ip-deny", "203.0.113.0/24", "--penalty-after", "3"},
		"too short":         {"create", "--app", "demo", "--kind", "ip", "--match-host", "demo.example.com", "--ip-deny", "203.0.113.0/24", "--penalty-for", "10s"},
		"set and clear":     {"update", "r1", "--penalty-for", "10m", "--clear-penalty"},
	} {
		f = main.AuthedFakeAPI(t, `{}`, http.StatusOK)
		if code := main.CmdEdgeRules(args); code == 0 || (*f.SawMethod()) != "" {
			t.Errorf("%s: exit %d, request %q; want failure before any request", name, code, (*f.SawMethod()))
		}
	}
}
