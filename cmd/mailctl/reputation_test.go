package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestReputationCommandsDefaultOffBeforeCredentialsAndReviewRequired(t *testing.T) {
	for _, args := range [][]string{{"reputation", "status", "--producer", "auth"}, {"reputation", "status", "--legacy"}, {"reputation", "holds", "--limit", "2"}, {"reputation", "events"}} {
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || cmd.method != http.MethodGet || !reputationCLIRoute(cmd.route) {
			t.Fatal(cmd, err)
		}
		var out, diagnostic bytes.Buffer
		code := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.ReputationEnableEnv:
				return "0"
			default:
				t.Fatalf("disabled reputation CLI read %s", key)
				return ""
			}
		}, args, &out, &diagnostic)
		if code != 1 || !strings.Contains(diagnostic.String(), "reputation protection disabled") {
			t.Fatal(code, diagnostic.String())
		}
	}
	base := []string{"reputation", "release", "--producer", "auth", "--expected-revision", "1", "--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "operator.test", "--reason", "sender_reviewed", "--evidence", "ticket:123"}
	if _, err := parseCommand(base, io.Discard); err == nil {
		t.Fatal("release accepted without review confirmation")
	}
	cmd, err := parseCommand(append(base, "--confirmed-reviewed"), io.Discard)
	if err != nil || cmd.method != http.MethodPost {
		t.Fatal(cmd, err)
	}
	for _, args := range [][]string{{"reputation", "status"}, {"reputation", "status", "--producer", "auth", "--legacy"}, {"reputation", "holds", "--after", "nonsense"}, {"reputation", "events", "--after", "domain:abc"}} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("ambiguous command accepted", args)
		}
	}
}
