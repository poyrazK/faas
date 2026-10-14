//go:build metal

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// adr: 1163
// TestJobTaskLiveOutputMetal follows a running image-run task through the
// compute gateway: its first line must arrive while the task is still
// running, every line must arrive once, and the stream must end with
// task_finished after the VM is gone.
func TestJobTaskLiveOutputMetal(t *testing.T) {
	h := newMetalHarnessWith(t, e2etest.Gatewayd, "FAAS_IMAGE_RUNS=1")
	defer h.Close()
	h.MustSeedFakeImage(t, "busybox:live-output")
	args := []string{"/job-fixture", "ticks"}
	run := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: h.imageRef(t, "busybox:live-output"), Args: &args}, 5*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.GatewayURL+"/v1/jobs/"+run.Job+"/runs/"+run.RunID+"/tasks/0/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.defaultKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status %d: %s", resp.StatusCode, body)
	}

	dec := api.NewDecoder(resp.Body)
	var lines []string
	var firstLineAt time.Time
	reason := ""
	for reason == "" {
		select {
		case ev, ok := <-dec.Events():
			if !ok {
				t.Fatalf("stream closed without an end frame after %q", lines)
			}
			switch ev.Event {
			case "log":
				var line api.LogEvent
				if err := json.Unmarshal([]byte(ev.Data), &line); err != nil {
					t.Fatalf("decode log frame %q: %v", ev.Data, err)
				}
				if firstLineAt.IsZero() {
					firstLineAt = time.Now()
				}
				lines = append(lines, line.Line)
			case "end":
				var end struct{ Reason string }
				_ = json.Unmarshal([]byte(ev.Data), &end)
				reason = end.Reason
			}
		case <-ctx.Done():
			t.Fatalf("no end frame; lines so far %q", lines)
		}
	}
	if reason != api.JobTaskLogsEndFinished {
		t.Fatalf("end reason = %q, want %q", reason, api.JobTaskLogsEndFinished)
	}
	var ticks []string
	for _, line := range lines {
		if strings.HasPrefix(line, "tick ") {
			ticks = append(ticks, line)
		}
	}
	if strings.Join(ticks, ",") != "tick 1,tick 2,tick 3" {
		t.Fatalf("relayed lines = %q, want each tick once in order", lines)
	}

	h.mu.Lock()
	h.runNames[run.RunID] = run.Job
	h.mu.Unlock()
	tasks := h.listTasks(t, &api.JobRunResponse{ID: run.RunID})
	if len(tasks) != 1 || tasks[0].FinishedAt == "" {
		t.Fatalf("task = %+v, want a finished task", tasks)
	}
	finished, err := time.Parse(time.RFC3339, tasks[0].FinishedAt)
	if err != nil {
		t.Fatalf("finished_at %q: %v", tasks[0].FinishedAt, err)
	}
	// The fixture sleeps 3 s after each tick, so a live first line lands
	// at least ~6 s before the task finishes.
	if !firstLineAt.Before(finished.Add(-3 * time.Second)) {
		t.Fatalf("first line at %s, task finished at %s: output was not live", firstLineAt, finished)
	}
}
