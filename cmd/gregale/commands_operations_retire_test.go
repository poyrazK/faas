// adr: 425
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func TestCmdOperationPolicyRetireJSON(t *testing.T) {
	for _, inUse := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired", true: "in use"}[inUse], func(t *testing.T) {
			resetJSONOut(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/account/operation-policies/crm-sync" || r.Header.Get("Authorization") != "Bearer fp_test_x" {
					t.Errorf("request: %s %s", r.Method, r.URL.Path)
				}
				if inUse {
					api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_policy_in_use", "Operation policy in use", "finish active work"))
					return
				}
				writeJSONTest(w, api.ExclusiveWorkPolicyRecord{ID: "policy-id", Revision: 2, Retired: true, Policy: exclusivework.Policy{Name: "crm-sync"}})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			stderr, restoreErr := captureStderr(t)
			code := run([]string{"operations", "policy", "retire", "crm-sync", "--json"})
			restoreErr()
			if inUse {
				var problem api.Problem
				if code == 0 || json.Unmarshal([]byte(stderr.String()), &problem) != nil || problem.Code != "operation_policy_in_use" || stdout.Len() != 0 {
					t.Fatalf("in-use result: exit=%d stdout=%s stderr=%s", code, &stdout, stderr.String())
				}
				return
			}
			var receipt api.ExclusiveWorkPolicyRecord
			if code != 0 || json.Unmarshal(stdout.Bytes(), &receipt) != nil || !receipt.Retired || receipt.ID != "policy-id" || receipt.Revision != 2 {
				t.Fatalf("retirement result: exit=%d stdout=%s stderr=%s", code, &stdout, stderr.String())
			}
		})
	}
}
