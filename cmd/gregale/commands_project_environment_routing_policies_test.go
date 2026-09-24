package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestProjectsEnvironmentRoutingPoliciesSetUsesScopedEndpoint(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"ownership":"environment","rules":[]}`, http.StatusOK)
	oldIn, oldOut := osStdin, osStdout
	osStdin = strings.NewReader(`{"rules":[]}`)
	var output bytes.Buffer
	osStdout = &output
	t.Cleanup(func() { osStdin, osStdout = oldIn, oldOut })
	if code := cmdProjectsEnvironmentPolicies([]string{"routing", "set", "shop", "staging", "shop-api", "--stdin", "--yes"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/projects/shop/environments/staging/workloads/shop-api/routing-policies" {
		t.Fatalf("request=%s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(output.String(), "0 redirect/rewrite rules") {
		t.Fatalf("output=%q", output.String())
	}
}
