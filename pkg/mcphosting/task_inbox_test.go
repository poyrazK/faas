package mcphosting

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeTaskInbox(t *testing.T) {
	const item = `{"resultType":"complete","taskId":"786512e2-9e0d-44bd-8f29-789f320fe840",
		"createdAt":"2026-11-23T12:00:00Z","lastUpdatedAt":"2026-11-23T12:00:00Z","ttlMs":60000,"status":"completed"}`
	const empty = `{"resultType":"complete","tasks":[]}`
	cursor := "page." + strings.Repeat("a", 43)
	page := func(items, fields string) json.RawMessage {
		return json.RawMessage(`{"resultType":"complete","tasks":[` + items + `]` + fields + `}`)
	}
	withCursor := page(item, `,"nextCursor":"`+cursor+`"`)
	query := TaskInboxQuery{Status: "completed", Limit: 1}
	got, err := decodeTaskInbox(withCursor, query)
	if err != nil || len(got.Tasks) != 1 || got.NextCursor != cursor || got.Tasks[0].Status != "completed" {
		t.Fatalf("valid page was rejected: %+v, %v", got, err)
	}
	got, err = decodeTaskInbox(json.RawMessage(empty), query)
	if err != nil || got.Tasks == nil || len(got.Tasks) != 0 {
		t.Fatalf("empty page was rejected: %+v, %v", got, err)
	}

	for _, tc := range []struct {
		name  string
		body  json.RawMessage
		query TaskInboxQuery
	}{
		{"over limit", page(item+","+item, ""), query},
		{"duplicate Task", page(item+","+item, ""), TaskInboxQuery{Limit: 2}},
		{"wrong status", page(item, ""), TaskInboxQuery{Status: "working", Limit: 1}},
		{"private result", page(strings.TrimSuffix(item, "}")+`,"result":{"private":"output"}}`, ""), query},
		{"repeated cursor", withCursor, TaskInboxQuery{Limit: 1, Cursor: cursor}},
		{"short page with cursor", withCursor, TaskInboxQuery{Limit: 2}},
		{"missing array", json.RawMessage(`{"resultType":"complete"}`), query},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := decodeTaskInbox(tc.body, tc.query); err == nil || got.Tasks != nil {
				t.Fatalf("invalid page exposed Task metadata: %+v, %v", got, err)
			}
		})
	}
}
