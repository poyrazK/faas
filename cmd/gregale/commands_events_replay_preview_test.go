package main

// adr: 624

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const previewCLISubscription = "ab44d60a-0b6c-42a2-87b0-f5f73a640e5b"

func replayPreviewCLIArgs() []string {
	return []string{"worker", "--subscription-id", previewCLISubscription, "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z"}
}

func TestCmdEventsReplayPreviewMetadataAndContinuation(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"app_slug":"worker","subscription":{"id":"target"},"scanned_count":1,"matched_count":0,"pattern_mismatch_count":1,"retention":{"settled_retention_seconds":2592000,"history_complete":false},"matches":[],"next_after":"erp1.next"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	args := append([]string{"replay-preview"}, replayPreviewCLIArgs()...)
	args = append(args, "--limit", "17", "--after", "erp1.a+/b?")
	if code := cmdEvents(args); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	q, _ := url.ParseQuery(fake.sawQuery)
	if fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/apps/worker/event-subscriptions/"+previewCLISubscription+"/replay-preview" || q.Get("limit") != "17" || q.Get("after") != "erp1.a+/b?" || q.Get("from") != "2026-10-01T00:00:00Z" || q.Get("until") != "2026-10-02T00:00:00Z" {
		t.Fatalf("request=%s %s?%s", fake.sawMethod, fake.sawPath, fake.sawQuery)
	}
	for _, want := range []string{"read-only", "This page: examined 1 | matched 0", "Complete history is not guaranteed", "Next page: --after erp1.next"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q: %s", want, stdout)
		}
	}
}

func TestCmdEventsReplayPreviewRejectsInvalidRangeAndTarget(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{{}, {"worker", "--subscription-id", "not-uuid", "--from", "bad", "--until", "bad"}, append(replayPreviewCLIArgs(), "--limit", "0"), append(replayPreviewCLIArgs(), "--limit", "101"), {"worker", "--subscription-id", previewCLISubscription, "--from", "2026-10-03T00:00:00Z", "--until", "2026-10-02T00:00:00Z"}} {
		code, _ := runWithStderr(t, func() int { return cmdEventsReplayPreview(args) })
		if code != 1 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
}

func TestCmdEventsReplayPreviewJSON(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"scanned_count":1,"matches":[{"event_id":"old","original_recipient":"not_captured"}],"retention":{"history_complete":false},"next_after":"next"}`, http.StatusOK)
	jsonOutput = true
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsReplayPreview(replayPreviewCLIArgs()); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout.String(), `"original_recipient": "not_captured"`) || !strings.Contains(stdout.String(), `"next_after": "next"`) || strings.Contains(stdout.String(), "This page:") {
		t.Fatalf("json=%s", stdout)
	}
}
