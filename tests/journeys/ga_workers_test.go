package journeys

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// GA inventory: queue workers (docs/workers.md). A worker is deployed from
// gregale.yaml and driven like a customer drives it: messages go in through
// the public API and the journey watches the pool and the queue through the
// CLI.

// TestWorkers deploys a queue worker with a `worker:` block and a pull
// binding: its idle pool scales to zero, a batch send starts it, it drains
// the queue, a message it fails lands in the dead letters, and the pool stops
// again (ADR-20261018101500 amendments 2026-10-27 and 2026-10-31).
func TestWorkers(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "worker-pools")
	slug := tg.app(t, "worker")
	s := newStory(t)
	binding := func(t *testing.T) map[string]any {
		t.Helper()
		doc := object(t, tg.ok(t, time.Minute, "queue", "status", slug).stdout)
		bindings, _ := field(doc, "bindings").([]any)
		for _, b := range bindings {
			if str(b, "name") == "jobs" {
				row, _ := b.(map[string]any)
				return row
			}
		}
		t.Fatalf("queue status lists no jobs binding: %s", compact(doc))
		return nil
	}
	replicas := func(t *testing.T) float64 {
		t.Helper()
		return num(object(t, tg.ok(t, time.Minute, "worker", "status", slug).stdout), "replicas")
	}
	var key string

	if !s.step("deploy", func(t *testing.T) {
		tg.deployWith(t, slug, false, "--path", workerSource(t))
		// A worker learns its own slug from the environment; it is set here
		// so the fixture's source, and its build, stay the same every run.
		env := filepath.Join(t.TempDir(), "worker.env")
		if err := os.WriteFile(env, []byte("WORKER_APP="+slug+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tg.ok(t, 5*time.Minute, "env", "push", "--app", slug, "-f", env)
		if b := binding(t); str(b, "mode") != "pull" || !truthy(field(b, "enabled")) {
			t.Fatalf("gregale.yaml binding = %s, want an enabled pull binding", compact(b))
		}
		key = tg.scopedKey(t, "worker-send", "queues:send")
	}) {
		return
	}
	s.check("idle_pool_scales_to_zero", func(t *testing.T) {
		eventually(t, "the idle pool scales to zero", 5*time.Minute, 10*time.Second, func() (bool, string) {
			n := replicas(t)
			return n == 0, fmt.Sprintf("replicas %v", n)
		})
	})
	s.step("batch_send", func(t *testing.T) {
		messages := make([]map[string]any, 10)
		for i := range messages {
			messages[i] = map[string]any{"payload": map[string]any{"n": i}}
		}
		r := tg.apiCall(http.MethodPost, "/v1/apps/"+slug+"/queues/send-batch",
			map[string]any{"queue_name": "jobs", "messages": messages}, key)
		if r.status != http.StatusOK {
			t.Fatalf("batch send: %s", r)
		}
		results, _ := field(jsonBody(t, r), "results").([]any)
		if len(results) != len(messages) {
			t.Fatalf("batch send answered %d results for %d messages", len(results), len(messages))
		}
		for _, result := range results {
			if str(result, "status") != "accepted" {
				t.Fatalf("message not accepted: %s", compact(result))
			}
		}
	})
	s.check("pool_drains_the_queue", func(t *testing.T) {
		start := time.Now()
		eventually(t, "the worker drains the queue", 8*time.Minute, 5*time.Second, func() (bool, string) {
			b := binding(t)
			return num(b, "depth") == 0, fmt.Sprintf("depth %v, in flight %v", field(b, "depth"), field(b, "in_flight"))
		})
		t.Logf("metric worker_drain_seconds=%.0f", time.Since(start).Seconds())
	})
	s.check("failed_message_is_dead_lettered", func(t *testing.T) {
		r := tg.apiCall(http.MethodPost, "/v1/apps/"+slug+"/queues/send",
			map[string]any{"queue_name": "jobs", "payload": map[string]any{"fail": true}}, key)
		if r.status != http.StatusCreated {
			t.Fatalf("send: %s", r)
		}
		eventually(t, "the failed message reaches the dead letters", 8*time.Minute, 5*time.Second, func() (bool, string) {
			b := binding(t)
			return num(b, "dead_letter") >= 1, fmt.Sprintf("dead letters %v, depth %v", field(b, "dead_letter"), field(b, "depth"))
		})
	})
	s.check("pool_scales_back_to_zero", func(t *testing.T) {
		eventually(t, "the drained pool scales to zero", 6*time.Minute, 10*time.Second, func() (bool, string) {
			n := replicas(t)
			return n == 0, fmt.Sprintf("replicas %v", n)
		})
	})
}

// workerSource copies the worker fixture into a fresh directory.
func workerSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("testdata", "worker-node")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read worker source: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read worker source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write worker source: %v", err)
		}
	}
	return dir
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
