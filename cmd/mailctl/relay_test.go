package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestRelayCommandsAreReadOnlyAndDefaultOffBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{{"relay", "status"}, {"relay", "events", "--limit", "2"}} {
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || cmd.method != http.MethodGet || !strings.HasPrefix(cmd.route, internalmail.RelayOperationsPath) || cmd.input != nil {
			t.Fatal("relay inspection could mutate state", cmd, err)
		}
		var out, diagnostic bytes.Buffer
		code := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.BackpressureEnableEnv:
				return "0"
			default:
				t.Fatalf("disabled relay CLI read %s", key)
				return ""
			}
		}, args, &out, &diagnostic)
		if code != 1 || !strings.Contains(diagnostic.String(), "backpressure disabled") {
			t.Fatal("disabled relay CLI reached private configuration", code, diagnostic.String())
		}
	}
}
