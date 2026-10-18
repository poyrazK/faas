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
// public edge into the compute gateway: its first line must arrive while the
// task is still running, every line must arrive once, and the stream must end
// with task_finished after the VM is gone. A stream opened after the task
// finished must end promptly instead of waiting for output.
func TestJobTaskLiveOutputMetal(t *testing.T) {
	h := newMetalHarnessWith(t, e2etest.Gatewayd|e2etest.GatewaydPublic, "FAAS_IMAGE_RUNS=1")
	defer h.Close()
	h.MustSeedFakeImage(t, "busybox:live-output")
	args := []string{"/job-fixture", "ticks"}
	run := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: h.imageRef(t, "busybox:live-output"), Args: &args}, 5*time.Minute)
	streamURL := h.EdgeURL() + "/v1/jobs/" + run.Job + "/runs/" + run.RunID + "/tasks/0/logs/stream"

	live := h.readJobTaskLogStream(t, streamURL, 5*time.Minute)
	if live.reason != api.JobTaskLogsEndFinished {
		t.Fatalf("end reason = %q, want %q", live.reason, api.JobTaskLogsEndFinished)
	}
	if got := live.ticks(); got != "tick 1,tick 2,tick 3" {
		t.Fatalf("relayed lines = %q, want each tick once in order", live.lines)
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
	if !live.firstLineAt.Before(finished.Add(-3 * time.Second)) {
		t.Fatalf("first line at %s, task finished at %s: output was not live", live.firstLineAt, finished)
	}

	// Reopened after the task finished, the stream either replays a ring
	// vmmd still holds or hands the client over to the stored output.
	late := h.readJobTaskLogStream(t, streamURL, 30*time.Second)
	switch late.reason {
	case api.JobTaskLogsEndStoredOutput:
	case api.JobTaskLogsEndFinished:
		if got := late.ticks(); got != "tick 1,tick 2,tick 3" {
			t.Fatalf("replayed lines = %q, want each tick once in order", late.lines)
		}
	default:
		t.Fatalf("late stream end reason = %q, want %q or %q", late.reason, api.JobTaskLogsEndStoredOutput, api.JobTaskLogsEndFinished)
	}
}

type jobTaskLogStream struct {
	lines       []string
	firstLineAt time.Time
	reason      string
}

func (s jobTaskLogStream) ticks() string {
	var ticks []string
	for _, line := range s.lines {
		if strings.HasPrefix(line, "tick ") {
			ticks = append(ticks, line)
		}
	}
	return strings.Join(ticks, ",")
}

// readJobTaskLogStream reads one job task log stream until its end frame.
func (h *MetalJobHarness) readJobTaskLogStream(t *testing.T, url string, timeout time.Duration) jobTaskLogStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	var out jobTaskLogStream
	for out.reason == "" {
		select {
		case ev, ok := <-dec.Events():
			if !ok {
				t.Fatalf("stream closed without an end frame after %q", out.lines)
			}
			switch ev.Event {
			case "log":
				var line api.LogEvent
				if err := json.Unmarshal([]byte(ev.Data), &line); err != nil {
					t.Fatalf("decode log frame %q: %v", ev.Data, err)
				}
				if out.firstLineAt.IsZero() {
					out.firstLineAt = time.Now()
				}
				out.lines = append(out.lines, line.Line)
			case "end":
				var end struct{ Reason string }
				_ = json.Unmarshal([]byte(ev.Data), &end)
				out.reason = end.Reason
			}
		case <-ctx.Done():
			t.Fatalf("no end frame within %s; lines so far %q", timeout, out.lines)
		}
	}
	return out
}
