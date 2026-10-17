package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestProducerOutboxCLIIsGatedBeforeSecretsAndReadOnly(t *testing.T) {
	for _, gate := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		code := run(context.Background(), func(k string) string {
			switch k {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.ProducerOutboxEnableEnv:
				return gate
			default:
				t.Fatal("disabled outbox read private config", k)
				return ""
			}
		}, []string{"outbox", "status"}, io.Discard, &diagnostics)
		if code == 0 {
			t.Fatal("disabled outbox command succeeded")
		}
	}
	for _, args := range [][]string{{"outbox", "status"}, {"outbox", "list", "--status", "failed"}, {"outbox", "show", "--id", "11111111-1111-4111-8111-111111111111"}} {
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || cmd.method != "GET" || !producerOutboxCLIRoute(cmd.route) {
			t.Fatal("valid read rejected", err)
		}
	}
	for _, args := range [][]string{{"outbox", "list", "--limit", "101"}, {"outbox", "list", "--after", "invalid"}, {"outbox", "show", "--id", "invalid"}, {"outbox", "retry"}, {"outbox", "status", "--token", "private"}} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("invalid or mutating command accepted")
		}
	}
}

func TestOutboxAdmissionCLIRequiresOwnExactGateBeforeSecrets(t *testing.T) {
	for _, gate := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			switch k {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv, internalmail.ProducerOutboxEnableEnv:
				return "1"
			case internalmail.OutboxReconciliationEnableEnv:
				return gate
			default:
				t.Fatal("disabled admission command read private config", k)
				return ""
			}
		}
		if run(t.Context(), getenv, []string{"outbox", "admission", "--id", "11111111-1111-4111-8111-111111111111"}, io.Discard, io.Discard) == 0 {
			t.Fatal("nonexact gate enabled lookup")
		}
	}
	cmd, err := parseCommand([]string{"outbox", "admission", "--id", "11111111-1111-4111-8111-111111111111"}, io.Discard)
	if err != nil || cmd.method != "GET" || !outboxAdmissionCLIRoute(cmd.route) || cmd.input != nil {
		t.Fatal("admission lookup is not a metadata-only GET", err)
	}
	for _, args := range [][]string{{"outbox", "admission", "--id", "invalid"}, {"outbox", "reconcile"}, {"outbox", "admission", "--id", "11111111-1111-4111-8111-111111111111", "--producer", "other"}} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("invalid or foreign-owned operation accepted")
		}
	}
}
