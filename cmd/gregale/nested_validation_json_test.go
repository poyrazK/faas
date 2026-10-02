package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNestedCommandValidationJSON(t *testing.T) {
	for _, useEnv := range []bool{false, true} {
		for _, args := range [][]string{
			{"debug", "requests", "audit-not-a-verb"},
			{"debug", "requsets"}, // A suggestion must not corrupt the JSON error.
			{"webhooks", "account"},
			{"webhooks", "account", "add"},
			{"webhooks", "account", "unknown", "00000000-0000-4000-8000-000000000001"},
			{"webhooks", "lsit"},
			{"alerts", "preset", "audit-not-a-verb"},
			{"registry", "lsit"},
			{"cors", "audit-not-a-verb"},
			{"crons", "cancel"},
			{"crons", "run"},
			{"crons", "occurrences"},
			{"domains", "verify"},
			{"invocations", "audit-not-a-verb"},
			{"operations", "policy", "audit-not-a-verb"},
			{"jobs", "registry", "audit-not-a-verb"},
			{"realtime", "auth", "audit-not-a-verb"},
			{"secrets", "refs", "audit-not-a-verb"},
			{"secrets", "refs", "list", "--app", "shop-api"},
			{"secrets", "refs", "list", "--app", "shop-api", "--environment", "production", "unexpected"},
			{"secrets", "refs", "set", "--app", "shop-api", "--environment", "production", "URL=plaintext"},
			{"secrets", "refs", "set", "--app", "shop-api", "--environment", "production", "URL=secret:DATABASE", "unexpected"},
			{"secrets", "refs", "unset", "--app", "shop-api", "--environment", "production", "URL", "unexpected"},
		} {
			label := strings.Join(args, " ")
			if useEnv {
				label += "/env"
			}
			t.Run(label, func(t *testing.T) {
				resetJSONOut(t)
				t.Setenv("FAAS_JSON", "0")
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(500)
				}))
				defer srv.Close()
				t.Setenv("FAAS_API", srv.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				argv := append([]string(nil), args...)
				if useEnv {
					t.Setenv("FAAS_JSON", "1")
				} else {
					argv = append(argv, "--json")
				}
				out, restoreOut := captureStdout(t)
				defer restoreOut()
				errOut, restoreErr := captureStderr(t)
				code := run(argv)
				restoreErr()
				var problem api.Problem
				if err := json.Unmarshal([]byte(errOut.String()), &problem); err != nil {
					t.Fatalf("invalid invocation must emit one Problem: %v: %s", err, errOut.String())
				}
				if code != 1 || problem.Status != 400 || problem.Code != api.CodeValidation || problem.Detail == "" || out.String() != "" || requests.Load() != 0 {
					t.Fatalf("exit=%d stdout=%s requests=%d error=%s", code, out.String(), requests.Load(), errOut.String())
				}
			})
		}
	}
}

func TestNestedCommandValidationHumanMode(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("FAAS_JSON", "0")
	errOut, restore := captureStderr(t)
	code := run([]string{"debug", "requests", "audit-not-a-verb"})
	restore()
	if code != 1 || errOut.String() != "unknown debug requests subcommand \"audit-not-a-verb\"\n" {
		t.Fatalf("human diagnostic changed: exit=%d: %s", code, errOut.String())
	}
}
