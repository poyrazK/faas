package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdEvents_NoArgs(t *testing.T) {
	resetJSONOut(t)
	code, captured := runWithStderr(t, func() int { return cmdEvents(nil) })
	if code != 1 || !strings.Contains(captured, "gregale events") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsPreviewUsesReadOnlyPreviewAPI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"event_id":"preview-1","source":"billing.stripe","type":"invoice.paid","candidate_count":2,"matched_count":1,"filter_mismatch_count":1,"other_mismatch_count":0,"matches":[{"app_slug":"invoice-worker","subscription_id":"sub-1","source":"billing.*","type":"invoice.paid","filter":{},"reason":"would_deliver"}],"non_matches":[{"app_slug":"audit-worker","subscription_id":"sub-2","source":"billing.*","type":"invoice.paid","filter":{"data":{"amount":{"$gt":200}}},"reason":"content_filter_mismatch"}],"truncated":false}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsPreview([]string{
		"billing.stripe", "invoice.paid", "--id", "preview-1", "--data", `{"amount":150}`,
	}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/events:preview" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	var got struct {
		ID     string          `json:"id"`
		Source string          `json:"source"`
		Type   string          `json:"type"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if got.ID != "preview-1" || got.Source != "billing.stripe" || got.Type != "invoice.paid" || string(got.Data) != `{"amount":150}` {
		t.Fatalf("request body=%s", f.sawBody)
	}
	for _, want := range []string{"Would deliver: 1", "Filtered: 1", "invoice-worker", "audit-worker", "content_filter_mismatch"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q: %s", want, stdout.String())
		}
	}
}

func TestCmdEventsPreviewRejectsInvalidDataBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{}`, http.StatusOK)
	code, captured := runWithStderr(t, func() int {
		return cmdEventsPreview([]string{"billing", "invoice.paid", "--data", "{bad"})
	})
	if code != 1 || !strings.Contains(captured, "Invalid --data") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsPublish_ValidatesDataBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"id":"evt-1"}`, http.StatusAccepted)
	code, captured := runWithStderr(t, func() int {
		return cmdEventsPublish([]string{"--id", "evt-1", "--source", "billing", "--type", "invoice.paid", "--data", "{bad"})
	})
	if code != 1 || !strings.Contains(captured, "Invalid --data") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsPublish_SendsEnvelope(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"evt-1","accepted_at":"2026-09-19T12:00:00Z","account_id":"acct-1"}`, http.StatusAccepted)
	if code := cmdEventsPublish([]string{
		"--id", "evt-1",
		"--source", "billing.stripe",
		"--type", "invoice.paid",
		"--data", `{"amount":150}`,
		"--time", "2026-09-19T11:59:00Z",
	}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/events:publish" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	var got struct {
		ID     string          `json:"id"`
		Source string          `json:"source"`
		Type   string          `json:"type"`
		Time   string          `json:"time"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "evt-1" || got.Source != "billing.stripe" || got.Type != "invoice.paid" || got.Time != "2026-09-19T11:59:00Z" || string(got.Data) != `{"amount":150}` {
		t.Fatalf("body=%s", f.sawBody)
	}
}

func TestCmdEventsPublish_PositionalSourceTypeGeneratesID(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"generated-id","accepted_at":"2026-09-19T12:00:00Z","account_id":"acct-1"}`, http.StatusAccepted)
	if code := cmdEventsPublish([]string{
		"billing.stripe", "invoice.paid", "--data", `{"amount":150}`,
	}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var got struct {
		ID     string          `json:"id"`
		Source string          `json:"source"`
		Type   string          `json:"type"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("generated event id is empty")
	}
	if got.Source != "billing.stripe" || got.Type != "invoice.paid" || string(got.Data) != `{"amount":150}` {
		t.Fatalf("body=%s", f.sawBody)
	}
}

func TestCmdEventsPublish_RejectsPositionalFlagConflict(t *testing.T) {
	resetJSONOut(t)
	if code, captured := runWithStderr(t, func() int {
		return cmdEventsPublish([]string{
			"billing.stripe", "invoice.paid", "--source", "other", "--data", `{}`,
		})
	}); code != 1 || !strings.Contains(captured, "source provided both positionally") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsPublish_JSONOutput(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"evt-1","accepted_at":"2026-09-19T12:00:00Z","account_id":"acct-1"}`, http.StatusAccepted)
	jsonOutput = true
	if code := cmdEventsPublish([]string{"--id", "evt-1", "--source", "source", "--type", "type", "--data", `{"ok":true}`}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(string(f.sawBody), `"id":"evt-1"`) {
		t.Fatalf("body=%s", f.sawBody)
	}
}

func TestCmdEventsSubscriptions_RendersReconciledManifest(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"app_slug":"invoice-worker","subscriptions":[{"id":"sub-1","app_id":"app-1","source":"billing.*","type":"invoice.paid","filter":{"data":{"amount":{"$gt":100}}},"enabled":true,"created_at":"2026-09-19T12:00:00Z","updated_at":"2026-09-19T12:01:00Z"}]}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsSubscriptions([]string{"invoice-worker"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/invoice-worker/event-subscriptions" {
		t.Fatalf("route=%s %s, want GET /v1/apps/invoice-worker/event-subscriptions", f.sawMethod, f.sawPath)
	}
	out := stdout.String()
	for _, want := range []string{"ID\tSOURCE\tTYPE\tFILTER\tENABLED\tUPDATED", "sub-1\tbilling.*\tinvoice.paid", `"$gt":100`, "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q: %s", want, out)
		}
	}
}

func TestCmdEventsSubscriptions_JSONOutput(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"app_slug":"invoice-worker","subscriptions":[]}`, http.StatusOK)
	jsonOutput = true
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsSubscriptions([]string{"invoice-worker"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var got struct {
		AppSlug       string `json:"app_slug"`
		Subscriptions []any  `json:"subscriptions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, stdout.String())
	}
	if got.AppSlug != "invoice-worker" || got.Subscriptions == nil {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestCmdEventsDeliveries_RendersFilteredRows(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"app_slug":"invoice-worker","deliveries":[{"invocation_id":"inv-1","invocation_source":"replay","event_id":"evt-1","event_source":"billing","event_type":"invoice.paid","subscription_id":"sub-1","state":"failed","attempts":3,"last_error":"worker unavailable","created_at":"2026-09-19T12:00:00Z"}],"next_before":"delivery-cursor"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsDeliveries([]string{"invoice-worker", "--event-source", "billing", "--event-id", "evt-1", "--state", "failed", "--limit", "1"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/invoice-worker/event-deliveries" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	if f.sawQuery != "event_id=evt-1&event_source=billing&limit=1&state=failed" {
		t.Fatalf("query=%q", f.sawQuery)
	}
	if got := stdout.String(); !strings.Contains(got, "INVOCATION\tINVOCATION_SOURCE\tEVENT\tSOURCE\tTYPE\tSTATE\tATTEMPTS\tCREATED\tERROR") || !strings.Contains(got, "inv-1\treplay\tevt-1\tbilling\tinvoice.paid\tfailed\t3") || !strings.Contains(got, "worker unavailable") {
		t.Fatalf("stdout=%q", got)
	}
}

func TestCmdEventsDeliveries_EventSourceRequiresEventID(t *testing.T) {
	resetJSONOut(t)
	code, captured := runWithStderr(t, func() int {
		return cmdEventsDeliveries([]string{"invoice-worker", "--event-source", "billing"})
	})
	if code != 1 || !strings.Contains(captured, "--event-source SOURCE --event-id ID") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsDeliveries_RendersFanoutFailureClassification(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"app_slug":"invoice-worker","deliveries":[],"fanout_failures":[{"event_id":"evt-1","event_source":"billing","event_type":"invoice.paid","subscription_id":"sub-1","state":"failed","attempts":12,"failure_code":"invocation_enqueue_failed","retryable":true,"last_error":"temporary outage","created_at":"2026-09-19T12:00:00Z","failed_at":"2026-09-19T12:01:00Z"}]}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsDeliveries([]string{"invoice-worker", "--state", "failed"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"FAILURE_CODE\tRETRYABLE",
		"evt-1\tbilling\tinvoice.paid\tsub-1\tfailed\t12\tinvocation_enqueue_failed\ttrue",
		"temporary outage",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestCmdEventsDeliveries_JSONOutput(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"app_slug":"invoice-worker","deliveries":[]}`, http.StatusOK)
	jsonOutput = true
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsDeliveries([]string{"invoice-worker"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var got struct {
		AppSlug    string `json:"app_slug"`
		Deliveries []any  `json:"deliveries"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, stdout.String())
	}
	if got.AppSlug != "invoice-worker" || got.Deliveries == nil {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestCmdEventsReplayRetryable_UsesBoundedBatchAPI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"app_slug":"invoice-worker","replayed_count":3,"has_more":true}`, http.StatusAccepted)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsReplayRetryableFanoutFailures([]string{"invoice-worker", "--event-source", "orders.us", "--event-id", "evt-1", "--limit", "3", "--yes"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/invoice-worker/event-deliveries:replay-retryable-fanout-failures" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	var request api.ReplayRetryableEventFanoutFailuresRequest
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if request.EventSource != "orders.us" || request.EventID != "evt-1" || request.Limit != 3 {
		t.Fatalf("request = %+v", request)
	}
	for _, want := range []string{"Queued 3 retryable event recipient(s)", "More retryable failures remain"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestCmdEventsReplayRetryable_RequiresCompleteEventIdentity(t *testing.T) {
	resetJSONOut(t)
	code, captured := runWithStderr(t, func() int {
		return cmdEventsReplayRetryableFanoutFailures([]string{"invoice-worker", "--event-id", "evt-1", "--yes"})
	})
	if code != 1 || !strings.Contains(captured, "--event-source SOURCE --event-id ID") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}

func TestCmdEventsReplayRetryable_RequiresConfirmation(t *testing.T) {
	resetJSONOut(t)
	code, captured := runWithStderr(t, func() int {
		return cmdEventsReplayRetryableFanoutFailures([]string{"invoice-worker"})
	})
	if code != 1 || !strings.Contains(captured, "--yes") {
		t.Fatalf("exit=%d stderr=%q", code, captured)
	}
}
