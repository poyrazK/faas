package apidserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// TestEgressDeniedNamesTheURLNotAWebhookField: a log drain to a private
// address was refused as "webhook_url resolves to 10.0.0.1" on
// production-us rc.294 (Hunt 14 H14-66); log drains have no webhook_url.
func TestEgressDeniedNamesTheURLNotAWebhookField(t *testing.T) {
	problem := apidserver.ResolveAndCheckEgress(context.Background(), "https://10.0.0.1/logs")
	if problem == nil || problem.Code != api.CodeImageEgressDenied {
		t.Fatalf("problem = %+v, want %s", problem, api.CodeImageEgressDenied)
	}
	if strings.Contains(problem.Detail, "webhook_url") || !strings.Contains(problem.Detail, "the URL's host 10.0.0.1 resolves to 10.0.0.1") {
		t.Fatalf("detail = %q", problem.Detail)
	}
}

// TestEgressRefusesLegacyNumericHosts: an alert webhook to
// http://2130706433/ (127.0.0.1) was saved on production (2026-11-09)
// because Go's resolver does not read the decimal form as an address.
func TestEgressRefusesLegacyNumericHosts(t *testing.T) {
	problem := apidserver.ResolveAndCheckEgress(context.Background(), "http://2130706433/")
	if problem == nil || !strings.Contains(problem.Detail, "legacy form") {
		t.Fatalf("problem = %+v, want the legacy numeric host refused", problem)
	}
	for host, want := range map[string]bool{
		"2130706433": true, "127.1": true, "0x7f000001": true, "0x7f.0.0.1": true, "10.0.0.1.": true,
		"hooks.example.com": false, "203.0.113.10": false, "2001:db8::1": false,
		"api.1password.com": false, "cafe": false, "0xcafe.dev": false,
	} {
		if got := apidserver.LegacyNumericHost(host); got != want {
			t.Errorf("LegacyNumericHost(%q) = %v, want %v", host, got, want)
		}
	}
}
