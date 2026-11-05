package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestWakeTimelineTimesKeepTheirColumn: RFC3339Nano drops trailing zeros, so
// "…24.8436Z" pushed its row out of the column on production-us rc.294
// (Hunt 14 H14-64).
func TestWakeTimelineTimesKeepTheirColumn(t *testing.T) {
	var buf bytes.Buffer
	main.RenderWakeTimelinePageWithOptions(&buf, "h14-py", api.WakeTimelineResponse{
		WakeID: "w", AppID: "a", Limit: 50,
		Events: []api.WakeTimelineEvent{
			{At: "2026-10-26T02:18:24.843597Z", Actor: "schedd", Kind: "wake.queue_accepted"},
			{At: "2026-10-26T02:18:24.8436Z", Actor: "schedd", Kind: "wake.admitted"},
		},
	}, false)
	var columns []int
	for _, line := range strings.Split(buf.String(), "\n") {
		if i := strings.Index(line, "schedd"); i >= 0 {
			columns = append(columns, i)
		}
	}
	if len(columns) != 2 || columns[0] != columns[1] || !strings.Contains(buf.String(), "02:18:24.843600Z") {
		t.Fatalf("actor columns %v:\n%s", columns, buf.String())
	}
}
