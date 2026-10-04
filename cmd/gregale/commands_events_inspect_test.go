package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ADR-582: text inspection reports routing and execution independently.
func TestCmdEventsInspectMixedOutcomes(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"event_id":"evt-1","event_source":"orders","event_type":"created","accepted_at":"2026-10-04T12:00:00Z","snapshot_captured":true,"recipient_count":3,"routing_summary":{"enqueued":2,"failed":1},"recipients":[{"app_slug":"complete","subscription_id":"sub-1","routing":{"state":"enqueued","attempts":1},"execution":{"state":"completed","attempts":2},"recovery_actions":[]},{"app_slug":"outage","subscription_id":"sub-2","routing":{"state":"failed","attempts":12,"last_error":"route outage"},"recovery_actions":[{"kind":"routing_replay"}]},{"app_slug":"cancel","subscription_id":"sub-3","routing":{"state":"enqueued","attempts":1},"cancellation":{"cancelled_count":2},"execution_unavailable":"cancel_pending","recovery_actions":[]}],"next_after":"erc1.next"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEvents([]string{"inspect", "--source", "orders/?+", "--id", "evt&1", "--limit", "17", "--after", "erc1.previous"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	path, err := url.Parse(fake.sawPath)
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.ParseQuery(fake.sawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if fake.sawMethod != "GET" || path.Path != "/v1/events/receipt" || query.Get("source") != "orders/?+" || query.Get("id") != "evt&1" || query.Get("limit") != "17" || query.Get("after") != "erc1.previous" {
		t.Fatalf("request: %s %v", fake.sawPath, fake.sawQuery)
	}
	for _, want := range []string{"Recipients: 3", "completed", "routing_replay", "cancel_pending (2 cancelled)", "Next page: --after erc1.next"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q: %s", want, stdout)
		}
	}
}

func TestCmdEventsInspectRejectsInvalidArguments(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{{"--source", "orders"}, {"--source", "orders", "--id", "evt", "--limit", "0"}, {"app", "--source", "orders", "--id", "evt"}} {
		code, _ := runWithStderr(t, func() int { return cmdEventsInspect(args) })
		if code != 1 {
			t.Fatalf("arguments %v: %d", args, code)
		}
	}
}

func TestCmdEventsInspectJSONAndLegacyEvidence(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"event_id":"evt","event_source":"orders","snapshot_captured":false,"recipients":[],"routing_summary":{},"recipient_count":0}`, http.StatusOK)
	jsonOutput = true
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsInspect([]string{"--source", "orders", "--id", "evt"}); code != 0 {
		t.Fatalf("json exit=%d", code)
	}
	if !strings.Contains(stdout.String(), `"snapshot_captured": false`) && !strings.Contains(stdout.String(), `"snapshot_captured":false`) {
		t.Fatalf("missing legacy json evidence: %s", stdout)
	}
	jsonOutput = false
	stdout.Reset()
	if code := cmdEventsInspect([]string{"--source", "orders", "--id", "evt"}); code != 0 {
		t.Fatalf("text exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "Recipient snapshot was not retained") {
		t.Fatalf("legacy text: %s", stdout)
	}
}
