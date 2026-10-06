package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdInvocationsGetKeyedReplay(t *testing.T) {
	for _, outputJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[outputJSON], func(t *testing.T) {
			inv := invocationFixture()
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/invocations/"+inv.ID:
					_ = json.NewEncoder(w).Encode(inv)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/invocations/"+inv.ID+"/replay-keyed":
					posts++
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "keyed-child", StatusURL: "/v1/invocations/keyed-child"})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
			}))
			defer server.Close()
			stdout, _, restore := swapIO(t)
			defer restore()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			previous := jsonOutput
			jsonOutput = outputJSON
			defer func() { jsonOutput = previous }()
			if code := cmdInvocationsGet([]string{"--replay-keyed", inv.ID}); code != 0 || posts != 1 {
				t.Fatalf("exit=%d posts=%d", code, posts)
			}
			if outputJSON {
				var result struct {
					Original api.Invocation          `json:"original"`
					Replay   api.AsyncInvokeResponse `json:"replay"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Original.ID != inv.ID || result.Replay.ID != "keyed-child" {
					t.Fatalf("JSON result: %s %v", stdout, err)
				}
			} else if !strings.Contains(stdout.String(), "keyed-child") || !strings.Contains(stdout.String(), "Replay status:") {
				t.Fatalf("text result: %s", stdout)
			}
		})
	}
}

func TestCmdInvocationsGetRejectsAmbiguousReplay(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	if code := cmdInvocationsGet([]string{"--replay", "--replay-keyed", "parent"}); code != 1 {
		t.Fatalf("ambiguous replay exit=%d", code)
	}
}
