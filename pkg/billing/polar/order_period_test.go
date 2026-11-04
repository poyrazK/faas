package polar

import (
	"encoding/json"
	"testing"
	"time"
)

// Shapes captured from the Polar sandbox on 2026-10-27: Polar sends no
// top-level period on an order, only per-line timestamps.
const (
	sandboxCreateOrder = `{"id":"8520a404","created_at":"2026-10-27T17:36:12Z","items":[
		{"id":"i1","label":"Gregale Hobby","amount":1000,"start_timestamp":"2026-10-27T17:36:11.355967Z","end_timestamp":"2026-11-27T17:36:11.355967Z"}]}`
	// The usage line covers an earlier meter period (billed in arrears), so
	// the plan line's fixed price decides the period.
	sandboxCycleOrder = `{"id":"85d46356","created_at":"2026-10-28T09:54:57Z",
		"product":{"prices":[{"id":"price_plan","amount_type":"fixed"},{"id":"price_usage","amount_type":"metered_unit"}]},
		"items":[
		{"id":"i1","label":"Gregale Hobby — From Oct 28, 2026 to Nov 28, 2026","amount":1000,"product_price_id":"price_plan","start_timestamp":"2026-10-28T09:39:57Z","end_timestamp":"2026-11-28T09:39:57Z"},
		{"id":"i2","label":"Gregale compute (GB-RAM-hours) — (10 consumed units) × $0.01","amount":10,"product_price_id":"price_usage","start_timestamp":"2026-10-27T19:00:00Z","end_timestamp":"2026-10-27T20:04:41Z"}]}`
)

func decodeOrder(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// adr: 20261028101500 — the period comes from the order lines; a recurring
// line's span wins over a zero-length usage line.
func TestPolarOrderPeriod(t *testing.T) {
	tests := []struct {
		name       string
		order      string
		start, end string
	}{
		{"subscription_create", sandboxCreateOrder, "2026-10-27T17:36:11.355967Z", "2026-11-27T17:36:11.355967Z"},
		{"subscription_cycle with arrears usage line", sandboxCycleOrder, "2026-10-28T09:39:57Z", "2026-11-28T09:39:57Z"},
		{"no price types: widest real span", `{"items":[{"id":"a","start_timestamp":"2026-10-28T09:39:57Z","end_timestamp":"2026-11-28T09:39:57Z"},{"id":"b","start_timestamp":"2026-10-27T19:00:00Z","end_timestamp":"2026-10-27T20:04:41Z"}]}`, "2026-10-27T19:00:00Z", "2026-11-28T09:39:57Z"},
		{"only a zero-length line", `{"items":[{"id":"u","start_timestamp":"2026-10-27T18:51:47Z","end_timestamp":"2026-10-27T18:51:47Z"}]}`, "2026-10-27T18:51:47Z", "2026-10-27T18:51:47Z"},
		{"top-level period wins", `{"period_start":"2026-09-01T00:00:00Z","period_end":"2026-09-30T00:00:00Z","items":[{"id":"i","start_timestamp":"2026-10-01T00:00:00Z","end_timestamp":"2026-11-01T00:00:00Z"}]}`, "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z"},
		{"line without timestamps", `{"items":[{"id":"i"}]}`, "", ""},
		{"no lines", `{}`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := polarOrderPeriod(decodeOrder(t, tt.order))
			if got := formatOptional(start); got != tt.start {
				t.Errorf("start = %q, want %q", got, tt.start)
			}
			if got := formatOptional(end); got != tt.end {
				t.Errorf("end = %q, want %q", got, tt.end)
			}
		})
	}
}

// adr: 20261028101500 — a webhook projection of a real cycle order gets the
// service period, not start == end == created_at.
func TestPolarInvoiceUsesLinePeriod(t *testing.T) {
	inv := polarInvoice("order.paid", decodeOrder(t, sandboxCycleOrder))
	if inv == nil {
		t.Fatal("order.paid produced no invoice")
	}
	if !inv.PeriodEnd.After(inv.PeriodStart) || formatOptional(inv.PeriodStart) != "2026-10-28T09:39:57Z" {
		t.Fatalf("period = %s .. %s, want the plan line's month", inv.PeriodStart, inv.PeriodEnd)
	}
	pending := polarInvoice("order.created", decodeOrder(t, `{"id":"o","status":"pending","total_amount":1089,"created_at":"2026-10-27T17:56:29Z","items":[{"id":"i","start_timestamp":"2026-10-27T17:41:29Z","end_timestamp":"2026-11-27T17:41:29Z"}]}`))
	if pending == nil || pending.Status != "open" || pending.AmountPaidCents != 0 {
		t.Fatalf("pending order = %+v, want an open unpaid invoice", pending)
	}
}

func formatOptional(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}
