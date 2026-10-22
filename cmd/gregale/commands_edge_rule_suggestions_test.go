package main_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261020105000 — `edge-rules suggest` reads the suggestions route and prints
// each with a recipe command that adds it, quoting glob paths and leaving a
// placeholder for required parameters the suggestion cannot fill.
func TestEdgeRulesSuggestCommand(t *testing.T) {
	main.ResetJSONOut(t)
	var buf bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	f := main.AuthedFakeAPI(t, `{"window":"7d","since":"2026-10-07T00:00:00Z","suggestions":[
		{"recipe":"rate-limit-path","title":"Rate-limit a path","reason":"POST /login received 900 requests in the last 7 days.","params":{"path":"/login","methods":"POST"},
		 "alternative":{"recipe":"challenge-path","title":"Challenge visitors on a path","params":{"path":"/login"}}},
		{"recipe":"allow-ips-only","title":"Allow only my IPs","reason":"/admin received 30 requests.","params":{"path":"/admin/**"}}
	]}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"suggest", "--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodGet || (*f.SawPath()) != "/v1/apps/demo/edge-rules/suggestions" {
		t.Fatalf("request = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	out := buf.String()
	for _, want := range []string{
		"POST /login received 900 requests",
		"gregale edge-rules recipe rate-limit-path --app demo --set methods=POST --set path=/login",
		"Or challenge visitors on a path: gregale edge-rules recipe challenge-path --app demo --set path=/login",
		"gregale edge-rules recipe allow-ips-only --app demo --set 'path=/admin/**' --set cidrs=<cidrs>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	main.AuthedFakeAPI(t, `{"window":"7d","since":"2026-10-07T00:00:00Z","suggestions":[]}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"suggest", "--app", "demo"}); code != 0 || !strings.Contains(buf.String(), "No suggested protections for demo") {
		t.Fatalf("empty: exit %d, output %q", code, buf.String())
	}

	f = main.AuthedFakeAPI(t, `{}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"suggest", "--app", "demo", "extra"}); code == 0 || (*f.SawMethod()) != "" {
		t.Fatalf("extra positional: exit %d, request %q; want failure before any request", code, (*f.SawMethod()))
	}
}
