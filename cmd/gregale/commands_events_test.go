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
