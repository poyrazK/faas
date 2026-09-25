package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestProjectsEnvironmentIPPoliciesSetUsesScopedEndpoint(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"ownership":"environment","rules":[]}`, http.StatusOK)
	oldIn, oldOut := osStdin, osStdout
	osStdin = strings.NewReader(`{"rules":[]}`)
	var output bytes.Buffer
	osStdout = &output
	t.Cleanup(func() { osStdin, osStdout = oldIn, oldOut })
	if code := cmdProjectsEnvironmentPolicies([]string{"ip", "set", "shop", "staging", "shop-api", "--stdin", "--yes"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/projects/shop/environments/staging/workloads/shop-api/ip-policies" {
		t.Fatalf("request=%s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(output.String(), "0 rules") {
		t.Fatalf("output=%q", output.String())
	}
}
