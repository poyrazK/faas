package apidserver_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A generic delivery signed 10 minutes earlier was told only that its
// signature did not verify (prod hunt r78).
func TestGenericInboundWebhookTimestampProblem(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name, value, want string
	}{
		{"fresh", strconv.FormatInt(now.Add(-time.Minute).Unix(), 10), ""},
		{"missing", "", "Unix time in seconds"},
		{"malformed", "2026-11-28T10:00:00Z", "Unix time in seconds"},
		{"stale", strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10), "10m0s from Gregale's clock"},
		{"future", strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10), "6m0s from Gregale's clock"},
	} {
		p := apidserver.GenericInboundWebhookTimestampProblem(tc.value, now)
		if tc.want == "" {
			if p != nil {
				t.Errorf("%s: got %v, want nil", tc.name, p)
			}
			continue
		}
		if p == nil || !strings.Contains(p.Detail, tc.want) {
			t.Errorf("%s: got %+v, want detail containing %q", tc.name, p, tc.want)
		}
	}
}

// Delivery paths accepted //host/x and /../ while cron paths refused them
// (prod hunt r78).
func TestInboundWebhookDeliveryPathIsClean(t *testing.T) {
	t.Parallel()
	for p, ok := range map[string]bool{
		"": true, "/": true, "/hooks/stripe": true, "/hooks/stripe/": true,
		"//": false, "//evil.example/x": false, "/a/../b": false, "/a/./b": false, "/a//b": false, "/a b": false, `/a\b`: false, "hooks": false,
	} {
		if got := apidserver.ValidateInboundWebhookDeliveryPath(p) == nil; got != ok {
			t.Errorf("ValidateInboundWebhookDeliveryPath(%q) ok = %v, want %v", p, got, ok)
		}
	}
}
