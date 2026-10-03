package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHelpDocumentsAuditCommandContracts(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "")
	cases := []struct {
		path string
		want []string
	}{
		{"registry list", []string{"gregale registry list --app <slug>", "(required)"}},
		{"registry rm", []string{"gregale registry rm --app <slug> --registry <host>", "(required)"}},
		{"webhooks account", []string{"<list|add|info|update|rm|deliveries|retry|rotate-secret>"}},
		{"webhooks account add", []string{"--target-url <URL>", "--event <EVENT>", "--from-stdin"}},
		{"webhooks account info", []string{"gregale webhooks account info <id>"}},
		{"webhooks account update", []string{"<id>", "--enable", "--disable", "--delivery-format"}},
		{"webhooks account rm", []string{"gregale webhooks account rm <id>"}},
		{"webhooks account deliveries", []string{"<id>", "--page-size <N>", "--page-token <CURSOR>"}},
		{"webhooks account retry", []string{"gregale webhooks account retry <id> <delivery-id>"}},
		{"webhooks account rotate-secret", []string{"<id>", "--from-stdin", "--secret <VALUE>"}},
		{"alerts preset", []string{"<list|enable>"}},
		{"alerts preset enable", []string{"--app <slug>", "--webhook-url <URL>", "<preset-name>", "--webhook-secret-stdin"}},
		{"debug requests", []string{"<list|export|watch|get|show|evidence|explain|trace|inspect|replay>"}},
		{"debug requests list", []string{"<slug>", "--cursor <CURSOR>", "--status <N>"}},
		{"debug requests export", []string{"<slug>", "--format <FORMAT>", "--output <PATH>"}},
		{"debug requests watch", []string{"<slug>", "--interval <DURATION>", "--once"}},
		{"debug requests get", []string{"<slug> <request-id-or-row-id>"}},
		{"debug requests show", []string{"<slug> <request-id-or-row-id>"}},
		{"debug requests evidence", []string{"<slug> <request-id-or-row-id>"}},
		{"debug requests explain", []string{"<slug> <request-id-or-row-id>"}},
		{"debug requests trace", []string{"<slug> <request-id-or-row-id>"}},
		{"debug requests inspect", []string{"<slug> [<request-id-or-row-id>]", "--latest"}},
		{"debug requests replay", []string{"<slug> <request-id-or-row-id>", "--wait", "--deployment-id <UUID>"}},
		{"debug dependencies", []string{"<slug>", "--since <DURATION>"}},
		{"app example network", []string{"gregale app <slug> network <show|doctor|attach|detach>"}},
		{"app example network attach", []string{"gregale app <slug> network attach --region <REGION> --cidrs <CIDRS> <network-id>"}},
		{"app example egress-allowlist add", []string{"gregale app <slug> egress-allowlist add <cidr>"}},
		{"app example egress-ports remove", []string{"gregale app <slug> egress-ports remove <port>"}},
		{"app example static-egress-ip", []string{"gregale app <slug> static-egress-ip <show|set|clear>"}},
		{"app example static-egress-ip set", []string{"gregale app <slug> static-egress-ip set <ip>"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resetJSONOut(t)
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := osStdout, osStderr
			osStdout, osStderr = &stdout, &stderr
			t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
			args := append(strings.Fields(tc.path), "--help")
			if code := run(args); code != 0 {
				t.Fatalf("help exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help is missing %q: %s", want, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("help wrote stderr: %q", stderr.String())
			}
		})
	}
	if count := requests.Load(); count != 0 {
		t.Fatalf("help made %d API requests", count)
	}
}
