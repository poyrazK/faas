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

func TestEncryptionCLIGatesBeforeCredentials(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		result := run(context.Background(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.EncryptionRotationEnableEnv:
				return flag
			default:
				t.Fatal("disabled encryption CLI read private configuration", key)
				return ""
			}
		}, []string{"encryption", "status"}, io.Discard, &diagnostics)
		if result == 0 {
			t.Fatal("disabled encryption inspection succeeded")
		}
	}
}

func TestEncryptionCLIUsesKeyIdentifiersAndBoundedAuditedBatches(t *testing.T) {
	id := strings.Repeat("a", 64)
	audit := []string{"--key-id", id, "--expected-revision", "0", "--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "operator", "--reason", "key_roll", "--evidence", "ticket:mail-123"}
	for _, action := range []string{"status", "preview", "activate", "rewrap", "retire"} {
		args := []string{"encryption", action}
		if action != "status" && action != "preview" {
			args = append(args, audit...)
		}
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || !encryptionCLIRoute(cmd.route) {
			t.Fatal("valid encryption command rejected", action, err)
		}
		if action == "status" {
			if cmd.method != http.MethodGet || cmd.input != nil {
				t.Fatal("inspection unexpectedly writes")
			}
		} else if cmd.method != http.MethodPost {
			t.Fatal("invalid operator method")
		}
		if action == "rewrap" && cmd.input.(internalmail.EncryptionRequest).BatchSize != 10 {
			t.Fatal("missing bounded default")
		}
	}
	for _, args := range [][]string{
		{"encryption", "activate", "--key-id", id},
		{"encryption", "status", "--token", "secret"},
		{"encryption", "preview", "--batch-size", "0"},
		{"encryption", "preview", "--batch-size", "26"},
		append(append([]string{"encryption", "rewrap"}, audit...), "--batch-size", "26"),
		append(append([]string{"encryption", "retire"}, audit...), "--private-key", "secret"),
	} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("unsafe encryption command accepted")
		}
	}
}
