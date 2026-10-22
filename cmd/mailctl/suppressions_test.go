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

func TestSuppressionCLIGatesBeforePrivateConfiguration(t *testing.T) {
	for _, gate := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		result := run(context.Background(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.SuppressionControlsEnableEnv:
				return gate
			default:
				t.Fatal("disabled controls read private configuration", key)
				return ""
			}
		}, []string{"suppressions", "show", "--address", "one@example.net"}, io.Discard, &diagnostics)
		if result == 0 {
			t.Fatal("disabled command succeeded")
		}
	}
}

func TestSuppressionCLIRequiresReviewAndCanonicalizesExactMailbox(t *testing.T) {
	audit := []string{"--address", "One@EXAMPLE.NET", "--expected-revision", "0", "--request-id", "11111111-1111-4111-8111-111111111111", "--actor", "oncall", "--reason", "mailbox_review", "--evidence", "ticket:mail-123"}
	for _, action := range []string{"list", "show", "history", "add", "release"} {
		args := []string{"suppressions", action}
		switch action {
		case "list":
		case "show", "history":
			args = append(args, "--address", "A+tag@EXAMPLE.NET")
		case "add":
			args = append(args, audit...)
		case "release":
			args = append(append(args, audit...), "--confirmed-reviewed")
		}
		cmd, err := parseCommand(args, io.Discard)
		if err != nil {
			t.Fatal("valid suppression command rejected", action, err)
		}
		if action == "list" {
			if suppressionCLIRoute(cmd.route) {
				t.Fatal("legacy inspection acquired a new gate")
			}
			continue
		}
		if !suppressionCLIRoute(cmd.route) {
			t.Fatal("wrong route")
		}
		if action == "show" || action == "history" {
			if cmd.method != http.MethodGet || !strings.Contains(cmd.route, "a%2Btag%40example.net") {
				t.Fatal("query encoding lost literal plus or case normalization")
			}
		} else if cmd.input.(internalmail.SuppressionRequest).Address != "one@example.net" {
			t.Fatal("write lost canonical address")
		}
	}
	for _, args := range [][]string{
		{"suppressions", "show", "--address", "*@example.net"},
		{"suppressions", "history", "--address", "one@example.net", "--after", "0"},
		{"suppressions", "history", "--address", "one@example.net", "--limit", "101"},
		{"suppressions", "add", "--address", "one@example.net"},
		append([]string{"suppressions", "release"}, audit...),
		{"suppressions", "show", "--address", "one@example.net", "--token", "private"},
	} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("unsafe suppression arguments accepted")
		}
	}
}
