package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCmdEvents_NoArgs(t *testing.T) {
	resetJSONOut(t)
	code, captured := runWithStderr(t, func() int { return cmdEvents(nil) })
	if code != 1 || !strings.Contains(captured, "gregale events") {
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
	f := authedFakeAPI(t, `{"app_slug":"invoice-worker","deliveries":[{"invocation_id":"inv-1","event_id":"evt-1","event_source":"billing","event_type":"invoice.paid","subscription_id":"sub-1","state":"failed","attempts":3,"last_error":"worker unavailable","created_at":"2026-09-19T12:00:00Z"}],"next_before":"inv-1"}`, http.StatusOK)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsDeliveries([]string{"invoice-worker", "--state", "failed", "--limit", "1"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/invoice-worker/event-deliveries" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	if got := stdout.String(); !strings.Contains(got, "INVOCATION\tEVENT\tSOURCE\tTYPE\tSTATE\tATTEMPTS\tCREATED\tERROR") || !strings.Contains(got, "inv-1\tevt-1\tbilling\tinvoice.paid\tfailed\t3") || !strings.Contains(got, "worker unavailable") {
		t.Fatalf("stdout=%q", got)
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
