package main

import (
	"bytes"
	"strings"
	"testing"
)

// adr: 20261018090210 — until API monetization is released in the shell, its
// verbs and flags are absent from help, man pages and suggestions, and
// invoking them is a usage error that does not name them.
func TestMonetizationDiscoveryRequiresOptIn(t *testing.T) {
	oldOut := osStdout
	t.Cleanup(func() { osStdout = oldOut })
	for _, value := range []string{"", "0", "1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FAAS_API_MONETIZATION_ENABLED", value)
			released := value == "1"
			for _, args := range [][]string{{"help", "consumers"}, {"man", "consumers"}, {"help", "platform-tenants"}, {"help", "account"}} {
				var out bytes.Buffer
				osStdout = &out
				_, restore := captureStderr(t)
				code := run(args)
				restore()
				text := out.String()
				if code != 0 {
					t.Fatalf("%v: exit %d", args, code)
				}
				for _, verb := range []string{"rate-card", "statement-draft", "billing-run", "spend-cap", "trial-days"} {
					if strings.Contains(text, verb) && !released {
						t.Fatalf("%v names %s while monetization is hidden:\n%s", args, verb, text)
					}
				}
				if args[1] == "consumers" && strings.Contains(text, "rate-card-create") != released {
					t.Fatalf("%v: rate-card-create listed=%t, released=%t", args, !released, released)
				}
			}
			for _, args := range [][]string{
				{"consumers", "rate-cards", "shop"},
				{"platform-tenants", "statements", "--id", "t1"},
				{"account", "billing-run", "list"},
				{"consumers", "create", "shop", "--external-ref", "c1", "--name", "C1", "--trial-days", "7"},
			} {
				if released {
					continue
				}
				stderr, restore := captureStderr(t)
				code := run(args)
				message := stderr.String()
				restore()
				if code == 0 {
					t.Fatalf("%v: hidden verb ran", args)
				}
				// The typed verb may be echoed back; nothing else may be named.
				for _, word := range []string{"rate-card-create", "statement-draft", "billing-connection", "spend-cap", "credit-grant", "invoices"} {
					if strings.Contains(message, word) {
						t.Fatalf("%v revealed %s: %s", args, word, message)
					}
				}
			}
			if suggestion, ok := suggestSubcommand("rate-card", mustCommand(t, "consumers")); !released && ok && strings.HasPrefix(suggestion, "rate-card") {
				t.Fatal("hidden verb suggested")
			}
		})
	}
}

func TestReleasedUsage(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "")
	if got := releasedUsage("account", "usage: gregale account {export|billing-run|invoices|slo}"); got != "usage: gregale account {export|slo}" {
		t.Errorf("account usage = %q", got)
	}
	if got := releasedUsage("consumers", "usage: gregale consumers <list|quote|rate-cards|usage> <slug>"); got != "usage: gregale consumers <list|usage> <slug>" {
		t.Errorf("consumers usage = %q", got)
	}
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1")
	if got := releasedUsage("account", "usage: gregale account {export|billing-run}"); got != "usage: gregale account {export|billing-run}" {
		t.Errorf("released usage = %q", got)
	}
}

func mustCommand(t *testing.T, name string) cliCommand {
	t.Helper()
	command, ok := lookupCliCommand(name)
	if !ok {
		t.Fatalf("no %s command", name)
	}
	return command
}
