// adr: 20261123180000

package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Questions in the words agents used in the 2026-11-23 eval transcripts:
// each must list a command containing want among its three commands.
func TestGuideSearchAnswersAgentQuestions(t *testing.T) {
	t.Setenv("GREGALE_DOCS_OFFLINE", "1")
	for _, tc := range []struct{ query, want string }{
		{"make my app public without an api key", "--no-require-authn"},
		{"open an outbound tcp port", "egress-ports"},
		{"set an environment variable", "env set"},
		{"add a secret", "env set"},
		{"roll back to the previous release", "rollback"},
		{"redis cache for my app", "redis "},
		{"follow logs", "--follow"},
		{"run a database migration before release", "migrate"},
	} {
		got := main.GuideSearchCommands(tc.query)
		found := false
		for _, command := range got {
			if strings.Contains(command, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: commands %q lack %q", tc.query, got, tc.want)
		}
	}
}

// Every command line a search can show is a real command.
func TestGuideSearchListsRealCommands(t *testing.T) {
	t.Setenv("GREGALE_DOCS_OFFLINE", "1")
	for _, query := range []string{"deploy", "public", "logs", "postgres", "redis", "rollback", "secrets", "domain"} {
		for _, command := range main.GuideSearchCommands(query) {
			words := strings.Fields(command)
			if len(words) < 2 || words[0] != "gregale" {
				t.Errorf("%q: %q is not a gregale command line", query, command)
			}
		}
	}
}
