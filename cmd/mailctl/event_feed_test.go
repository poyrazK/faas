package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestEventFeedCLIIsPrivateDefaultOffAndAudited(t *testing.T) {
	for _, args := range [][]string{{"events", "consumers", "--producer", "auth"}, {"events", "status", "--producer", "auth", "--consumer", "effects"}} {
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || cmd.method != http.MethodGet || !eventFeedCLIRoute(cmd.route) {
			t.Fatal(cmd, err)
		}
		var out, diag bytes.Buffer
		code := run(t.Context(), func(k string) string {
			switch k {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.EventFeedEnableEnv:
				return "0"
			default:
				t.Fatalf("disabled feed CLI read %s", k)
				return ""
			}
		}, args, &out, &diag)
		if code != 1 || !strings.Contains(diag.String(), "event feeds disabled") {
			t.Fatal(code, diag.String())
		}
	}
	valid := []string{"events", "replay", "--producer", "auth", "--consumer", "effects", "--from-start", "--expected-generation", "1", "--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "operator.test", "--reason", "consumer_reviewed", "--evidence", "ticket:123"}
	cmd, err := parseCommand(valid, io.Discard)
	if err != nil || cmd.method != http.MethodPost || cmd.route != internalmail.EventFeedOperationsPath+"/replay" {
		t.Fatal(cmd, err)
	}
	for _, args := range [][]string{
		{"events", "replay", "--producer", "auth", "--consumer", "effects", "--from-start"},
		append(valid, "--after", "22222222-2222-4222-8222-222222222222"),
		{"events", "status", "--producer", "auth", "--consumer", "../private"},
		{"events", "consumers", "--producer", "auth", "--after", "invalid?name"},
	} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("ambiguous or unaudited command accepted", args)
		}
	}
}
