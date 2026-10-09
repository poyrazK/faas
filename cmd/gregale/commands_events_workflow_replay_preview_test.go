package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func workflowReplayPreviewCLIArgs() []string {
	return []string{"worker", "--workflow-name", "paid", "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z"}
}

func TestCmdEventsWorkflowReplayPreviewMetadataAndContinuation(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"app_slug":"worker","workflow_name":"paid","scanned_count":1,"captured_count":1,"matched_count":1,"already_admitted_count":1,"potential_admission_count":0,"retention":{"settled_retention_seconds":2592000,"history_complete":false},"matches":[{"event_id":"old","event_source":"billing.stripe","event_type":"invoice.paid","accepted_at":"2026-10-01T12:00:00Z","original_recipient":"captured","routing_state":"enqueued","filter_matched":true,"admission_recorded":true,"workflow_run_status":"pending","receipt_url":"/v1/events/receipt?source=billing.stripe&id=old"}],"next_after":"werp1.next"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	args := append([]string{"workflow-replay-preview"}, workflowReplayPreviewCLIArgs()...)
	args = append(args, "--limit", "17", "--after", "werp1.a+/b?")
	if code := cmdEvents(args); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	query, _ := url.ParseQuery(fake.sawQuery)
	if fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/apps/worker/workflow-event-replay-preview" || query.Get("workflow_name") != "paid" ||
		query.Get("limit") != "17" || query.Get("after") != "werp1.a+/b?" || query.Get("from") != "2026-10-01T00:00:00Z" || query.Get("until") != "2026-10-02T00:00:00Z" {
		t.Fatalf("request=%s %s?%s", fake.sawMethod, fake.sawPath, fake.sawQuery)
	}
	for _, want := range []string{"read-only", "trigger matches 1", "already admitted 1", "Next page: --after werp1.next"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q: %s", want, stdout)
		}
	}
}

func TestCmdEventsWorkflowReplayPreviewRejectsInvalidArgumentsAndJSON(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{
		{}, {"worker", "--from", "bad", "--until", "bad"},
		{"worker", "--workflow-name", "paid", "--from", "2026-10-02T00:00:00Z", "--until", "2026-10-01T00:00:00Z"},
		append(workflowReplayPreviewCLIArgs(), "--limit", "0"), append(workflowReplayPreviewCLIArgs(), "--limit", "101"),
	} {
		code, _ := runWithStderr(t, func() int { return cmdEventsWorkflowReplayPreview(args) })
		if code != 1 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
	authedFakeAPI(t, `{"workflow_name":"paid","matches":[{"event_id":"old","original_recipient":"captured","admission_recorded":false}]}`, http.StatusOK)
	jsonOutput = true
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsWorkflowReplayPreview(workflowReplayPreviewCLIArgs()); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout.String(), `"workflow_name": "paid"`) || strings.Contains(stdout.String(), "This page:") {
		t.Fatalf("json=%s", stdout)
	}
}
