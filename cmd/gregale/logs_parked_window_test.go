package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParkedWindowInstances(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	since := now.Add(-2 * time.Hour)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	instances := []api.InstanceResponse{
		{ID: "running", State: "running", StartedAt: at(-30 * time.Minute)},
		{ID: "parked-in-window", State: "parked", StartedAt: at(-90 * time.Minute), ParkedAt: at(-80 * time.Minute)},
		{ID: "parked-before-window", State: "parked", StartedAt: at(-5 * time.Hour), ParkedAt: at(-3 * time.Hour)},
		{ID: "straddles-window", State: "stopped", StartedAt: at(-3 * time.Hour), LastRequestAt: at(-100 * time.Minute)},
		{ID: "no-start", State: "parked"},
	}
	got := main.ParkedWindowInstances(instances, since, now)
	var ids []string
	for _, ins := range got {
		ids = append(ids, ins.ID)
	}
	if want := []string{"straddles-window", "parked-in-window"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("instances = %v, want %v (oldest first, no running or out-of-window)", ids, want)
	}

	// Activity across UTC midnight reads both archive days.
	midnight := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	cross := main.ParkedWindowInstances([]api.InstanceResponse{{ID: "x", State: "parked", StartedAt: midnight.Add(-10 * time.Minute).Format(time.RFC3339), ParkedAt: midnight.Add(10 * time.Minute).Format(time.RFC3339)}}, midnight.Add(-time.Hour), midnight.Add(time.Hour))
	if len(cross) != 1 || !reflect.DeepEqual(cross[0].Days, []string{"2026-10-15", "2026-10-16"}) {
		t.Fatalf("midnight-crossing days = %+v", cross)
	}

	var many []api.InstanceResponse
	for i := 0; i < main.ParkedWindowInstanceLimit+3; i++ {
		many = append(many, api.InstanceResponse{ID: fmt.Sprint(i), State: "parked", StartedAt: at(time.Duration(-60+i) * time.Minute)})
	}
	limited := main.ParkedWindowInstances(many, since, now)
	if len(limited) != main.ParkedWindowInstanceLimit || limited[len(limited)-1].ID != fmt.Sprint(main.ParkedWindowInstanceLimit+2) {
		t.Fatalf("limit keeps the newest %d, got %+v", main.ParkedWindowInstanceLimit, limited)
	}
}

type parkedWindowFakeClient struct {
	instances []api.InstanceResponse
	archives  map[string]string
}

func (f parkedWindowFakeClient) ListInstancesWithHistory(context.Context, string, bool) ([]api.InstanceResponse, error) {
	return f.instances, nil
}

func (f parkedWindowFakeClient) StreamAppArchivedLogs(_ context.Context, _ string, s api.ArchiveLogSelector) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.archives[s.InstanceID+"/"+s.Date] + "event: end\ndata: {\"reason\":\"archive_complete\"}\n\n")), nil
}

// H10-100: `gregale logs app --since 2h` printed nothing for a marker an
// instance wrote at 07:17 because a different instance was running; the line
// was only reachable with --archive --instance --date.
func TestPrintParkedWindowLogs(t *testing.T) {
	now := time.Now().UTC()
	since := now.Add(-2 * time.Hour)
	line := func(at time.Time, text string) string {
		raw, _ := json.Marshal(api.LogEvent{Seq: 1, InstanceID: "parked", Stream: "stdout", Line: text, WrittenAt: at.Format(time.RFC3339Nano)})
		return "data: " + string(raw) + "\n\n"
	}
	start := now.Add(-100 * time.Minute)
	day := start.UTC().Format("2006-01-02")
	client := parkedWindowFakeClient{
		instances: []api.InstanceResponse{
			{ID: "live", State: "running", StartedAt: now.Add(-10 * time.Minute).Format(time.RFC3339)},
			{ID: "parked", State: "parked", StartedAt: start.Format(time.RFC3339), ParkedAt: now.Add(-90 * time.Minute).Format(time.RFC3339)},
		},
		archives: map[string]string{"parked/" + day: line(since.Add(-time.Minute), "before window marker") + line(start.Add(time.Minute), "h10log marker") + line(start.Add(2*time.Minute), "unrelated")},
	}
	var out, errOut bytes.Buffer
	n := main.PrintParkedWindowLogs(context.Background(), client, "h9-echo", api.LogFilter{Grep: "marker", Level: "error"}, since, &out, &errOut)
	if n != 1 || !strings.Contains(out.String(), "h10log marker") || strings.Contains(out.String(), "before window") || strings.Contains(out.String(), "unrelated") {
		t.Fatalf("printed %d lines:\n%s", n, out.String())
	}
	if h := errOut.String(); !strings.Contains(h, "Retained logs of parked instance parked") || !strings.Contains(h, "--level is not applied") {
		t.Fatalf("header = %q", h)
	}
}
