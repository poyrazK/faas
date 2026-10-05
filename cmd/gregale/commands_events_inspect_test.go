package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ADR-596: text inspection reports routing and execution independently.
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

func TestCmdEventsInspectReplayRecovery(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"event_id":"evt","event_source":"orders","snapshot_captured":true,"recipient_count":1,"routing_summary":{"enqueued":1},"recipients":[{"subscription_id":"sub","routing":{"state":"enqueued"},"execution":{"state":"failed","last_error":"original failure"},"recovery":{"retained_replay_count":2,"latest_replay":{"invocation_id":"replay-two","state":"completed","attempts":1},"history_url":"/history"},"recovery_actions":[]}]}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsInspect([]string{"--source", "orders", "--id", "evt"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	for _, want := range []string{"failed", "original failure", "Recovery: recovered", "retained replays: 2", "--subscription sub"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q: %s", want, stdout)
		}
	}
}

func TestCmdEventsInspectReplayHistory(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"event_id":"evt","event_source":"orders","subscription_id":"sub","original_invocation_id":"original","replays":[{"invocation_id":"replay-two","replayed_from_invocation_id":"replay-one","state":"completed","attempts":1,"created_at":"2026-10-05T00:00:00Z"}],"next_after":"err1.next"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsInspect([]string{"--source", "orders/?+", "--id", "evt&1", "--subscription", "sub/?+", "--after", "err1.previous", "--limit", "1"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	query, err := url.ParseQuery(fake.sawQuery)
	if err != nil || fake.sawPath != "/v1/events/receipt/replays" || query.Get("subscription_id") != "sub/?+" || query.Get("source") != "orders/?+" || query.Get("id") != "evt&1" || query.Get("after") != "err1.previous" || query.Get("limit") != "1" {
		t.Fatalf("history request: %s %s %v", fake.sawPath, fake.sawQuery, err)
	}
	for _, want := range []string{"original invocation: original", "replay-two", "replay-one", "completed", "Next page: --subscription sub/?+ --after err1.next"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q: %s", want, stdout)
		}
	}
	jsonOutput = true
	stdout.Reset()
	if code := cmdEventsInspect([]string{"--source", "orders", "--id", "evt", "--subscription", "sub"}); code != 0 {
		t.Fatalf("json exit=%d", code)
	}
	if !strings.Contains(stdout.String(), `"replayed_from_invocation_id"`) {
		t.Fatalf("missing JSON lineage: %s", stdout)
	}
}
