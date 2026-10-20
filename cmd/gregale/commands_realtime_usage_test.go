package main_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrintRealtimeUsage(t *testing.T) {
	var out bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = prev })
	hour := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	main.PrintRealtimeUsage(api.RealtimeUsageResponse{Plan: api.PlanPro, ConnectionLimit: 1000, OpenConnections: 180,
		Hours: []api.RealtimeUsageHour{{Hour: hour, PeakConnections: 142, ConnectionSeconds: 346680, DeliveredMessages: 12401, ReceivedMessages: 311}}}, 24)
	got := out.String()
	for _, want := range []string{"180 open of 1,000 allowed on the pro plan", "Realtime is free", "2026-10-14 21:00", "142", "96.3", "12,401", "311"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	out.Reset()
	main.PrintRealtimeUsage(api.RealtimeUsageResponse{Plan: api.PlanPro, ConnectionLimit: 1000}, 6)
	if !strings.Contains(out.String(), "No recorded realtime usage in the last 6 hours.") {
		t.Fatalf("empty output = %q", out.String())
	}
	out.Reset()
	main.PrintRealtimeUsage(api.RealtimeUsageResponse{Plan: api.PlanFree}, 24)
	if !strings.Contains(out.String(), "does not include realtime") {
		t.Fatalf("free plan output = %q", out.String())
	}
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := main.GroupDigits(in); got != want {
			t.Fatalf("groupDigits(%d) = %q, want %q", in, got, want)
		}
	}
}
