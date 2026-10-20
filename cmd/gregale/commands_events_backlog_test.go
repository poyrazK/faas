package main_test

// adr: 617

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCmdEventsBacklogDiscoveryAndFilters(t *testing.T) {
	main.ResetJSONOut(t)
	fake := main.AuthedFakeAPI(t, `{"coverage":"captured_and_backfill_recipients","unattributed_receipts":2,"consumers":[{"app_slug":"orders","subscription_id":"sub","waiting_recipients":9,"capacity_waiting_recipients":7}],"recipients":[{"app_slug":"orders","event_id":"evt","waiting_reason":"capacity_consumer","capacity_deferrals":20}],"next_after":"recipient","next_consumers_after":"consumer"}`, http.StatusOK)
	stdout, restore := main.SwapStdout(t)
	defer restore()
	if code := main.CmdEvents([]string{"backlog", "--app", "orders", "--subscription-id", "a&b", "--capacity-scope", "consumer", "--min-age", "10m", "--limit", "17", "--consumer-limit", "3", "--consumers-after", "before"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	q, _ := url.ParseQuery((*fake.SawQuery()))
	if (*fake.SawPath()) != "/v1/events/backlog" || q.Get("min_age_seconds") != "600" || q.Get("subscription_id") != "a&b" || q.Get("consumer_limit") != "3" || q.Get("consumers_after") != "before" {
		t.Fatalf("request=%s?%s", (*fake.SawPath()), (*fake.SawQuery()))
	}
	for _, want := range []string{"capacity_consumer", "evt", "Legacy receipts without captured recipients: 2", "Next recipient page: --after recipient", "Next consumer page: --consumers-after consumer"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q: %s", want, stdout)
		}
	}
}
func TestCmdEventsBacklogValidation(t *testing.T) {
	main.ResetJSONOut(t)
	for _, args := range [][]string{{"--limit", "0"}, {"--consumer-limit", "201"}, {"--state", "dead_letter"}, {"--capacity-scope", "all"}, {"--min-age", "-1s"}, {"--min-age", "500ms"}, {"--min-age", "9000h"}, {"unexpected"}} {
		code, _ := main.RunWithStderr(t, func() int { return main.CmdEventsBacklog(args) })
		if code != 1 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
}

func TestCmdEventsBacklogJSON(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, `{"coverage":"captured_and_backfill_recipients","recipients":[],"consumers":[],"unattributed_receipts":3}`, http.StatusOK)
	(*main.JsonOutput) = true
	stdout, restore := main.SwapStdout(t)
	defer restore()
	if code := main.CmdEventsBacklog(nil); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout.String(), `"unattributed_receipts": 3`) || strings.Contains(stdout.String(), "APP\t") {
		t.Fatalf("json=%s", stdout)
	}
}
