package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestRecoveryCLIGatesBeforeSocketAndCredential(t *testing.T) {
	for _, gate := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		code := run(context.Background(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.RecoveryEnableEnv:
				return gate
			default:
				t.Fatal("disabled recovery read private input", key)
				return ""
			}
		}, []string{"recovery", "status"}, io.Discard, &diagnostics)
		if code == 0 {
			t.Fatal("disabled recovery command succeeded")
		}
	}
}

func TestRecoveryCLIRequiresEvidenceReviewAndBounds(t *testing.T) {
	audit := []string{"--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "oncall", "--reason", "restore_review", "--evidence", "ticket:restore-1"}
	base := append([]string{"--recovery-id", "22222222-2222-4222-8222-222222222222", "--expected-revision", "1"}, audit...)
	review := append([]string{"recovery", "review", "--id", "33333333-3333-4333-8333-333333333333", "--expected-status", "queued", "--outcome", "submitted", "--confirmed-relay-accepted"}, base...)
	release := append([]string{"recovery", "release", "--confirmed-policy", "--confirmed-access", "--confirmed-consumers", "--confirmed-relay", "--confirmed-quarantine"}, base...)
	for _, args := range [][]string{{"recovery", "status"}, {"recovery", "preview", "--limit", "1"}, review, release} {
		cmd, err := parseCommand(args, io.Discard)
		if err != nil || !recoveryCLIRoute(cmd.route) {
			t.Fatal("valid recovery command failed", err)
		}
	}
	for _, args := range [][]string{
		{"recovery", "preview", "--limit", "101"},
		{"recovery", "preview", "--after", "invalid"},
		append([]string{"recovery", "release"}, base...),
		{"recovery", "status", "--token", "private"},
		{"recovery", "review", "--outcome", "retry"},
		{"recovery", "status", "extra"},
	} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("unsafe recovery command accepted")
		}
	}
}
