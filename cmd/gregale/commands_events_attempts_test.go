package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// adr: 611
func TestCmdEventsAttemptsHistory(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"event_id":"evt","event_source":"orders","subscription_id":"sub","original_invocation_id":"original","coverage":"recorded_attempts_only","attempts":[{"id":1,"invocation_id":"original","attempt":2,"replay_generation":1,"outcome":"unknown","error_detail":"lease expired","started_at":"2026-10-05T00:00:00Z"}],"next_after":"era1.next"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	args := []string{"attempts", "--source", "orders/?+", "--id", "evt&1", "--subscription", "sub/?+", "--after", "era1.previous", "--limit", "1"}
	if code := cmdEvents(args); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	query, err := url.ParseQuery(fake.sawQuery)
	if err != nil || fake.sawMethod != "GET" || fake.sawPath != "/v1/events/receipt/attempts" || query.Get("source") != "orders/?+" || query.Get("id") != "evt&1" || query.Get("subscription_id") != "sub/?+" || query.Get("after") != "era1.previous" || query.Get("limit") != "1" {
		t.Fatalf("request: %s %s %v", fake.sawPath, fake.sawQuery, err)
	}
	for _, want := range []string{"original invocation: original", "unknown", "lease expired", "Next page: --after era1.next"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q: %s", want, stdout)
		}
	}
	jsonOutput = true
	stdout.Reset()
	if code := cmdEvents(args); code != 0 || !strings.Contains(stdout.String(), `"replay_generation"`) {
		t.Fatalf("json: %d %s", code, stdout)
	}
}

func TestCmdEventsAttemptsRejectsMissingIdentity(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{{"--source", "orders", "--id", "evt"}, {"--source", "orders", "--id", "evt", "--subscription", "sub", "--limit", "0"}, {"unexpected", "--source", "orders", "--id", "evt", "--subscription", "sub"}} {
		code, _ := runWithStderr(t, func() int { return cmdEventsAttempts(args) })
		if code != 1 {
			t.Fatalf("arguments %v: %d", args, code)
		}
	}
}
