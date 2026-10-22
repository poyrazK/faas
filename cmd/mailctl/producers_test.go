package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestProducerLifecycleCLIIsDefaultOffBeforeSecrets(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		result := run(context.Background(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.ProducerLifecycleEnableEnv:
				return flag
			default:
				t.Fatal("disabled lifecycle CLI read private configuration", key)
				return ""
			}
		}, []string{"producers", "status", "--producer", "auth"}, io.Discard, &diagnostics)
		if result == 0 {
			t.Fatal("disabled producer command succeeded")
		}
	}
}

func TestProducerLifecycleCLISelectsIdentifiersAndRequiresAudit(t *testing.T) {
	id := strings.Repeat("a", 64)
	audit := []string{"--producer", "auth", "--expected-revision", "0", "--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "operator", "--reason", "credential_roll", "--evidence", "ticket:mail-123"}
	cmd, err := parseCommand([]string{"producers", "status", "--producer", "auth"}, io.Discard)
	if err != nil || cmd.method != http.MethodGet || cmd.route != internalmail.ProducerLifecyclePath+"/auth" || cmd.input != nil {
		t.Fatal("invalid inspection route", err)
	}
	for _, action := range []string{"suspend", "resume", "rotate", "revoke"} {
		args := append([]string{"producers", action}, audit...)
		switch action {
		case "rotate":
			args = append(args, "--replacement-id", id)
		case "revoke":
			args = append(args, "--credential-id", id)
		case "resume":
			args = append(args, "--confirmed-reviewed")
		}
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || cmd.method != http.MethodPost || cmd.route != internalmail.ProducerLifecyclePath+"/"+action {
			t.Fatal("invalid mutation", action, err)
		}
		input := cmd.input.(internalmail.ProducerLifecycleRequest)
		if input.Validate(action) != nil || action == "rotate" && (input.OverlapSeconds == nil || *input.OverlapSeconds != 3600) {
			t.Fatal("invalid request or missing bounded default", action)
		}
	}
	invalid := [][]string{
		{"producers", "rotate", "--producer", "auth", "--replacement-id", id},
		append(append([]string{"producers", "resume"}, audit...), "--confirmed-reviewed=false"),
		{"producers", "status", "--producer", "../auth"},
		{"producers", "status", "--producer", "auth", "--token", "secret"},
		{"producers", "rotate", "--producer", "auth", "--replacement-id", "raw-secret"},
	}
	for _, overlap := range []string{"24h1s", "1.5s", "-1s"} {
		invalid = append(invalid, append(append([]string{"producers", "rotate"}, audit...), "--replacement-id", id, "--overlap", overlap))
	}
	for _, args := range invalid {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("unsafe operator arguments accepted")
		}
	}
}
