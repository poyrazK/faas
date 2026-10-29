package main_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeProjectLogs struct {
	mu      sync.Mutex
	streams map[string][]string // app slug -> successive SSE bodies
	calls   map[string]int
}

func (f *fakeProjectLogs) GetProject(context.Context, string) (api.ProjectResponse, error) {
	return api.ProjectResponse{Workloads: []api.ProjectWorkloadResponse{
		{Slug: "shop-api", WorkloadName: "api", Status: "active"},
		{Slug: "shop-billing", WorkloadName: "billing_api", Status: "active"},
		{Slug: "shop-old", WorkloadName: "old", Status: "deleted"},
	}}, nil
}

func (f *fakeProjectLogs) StreamAppLogs(_ context.Context, slug, _ string, _ bool, _ api.LogFilter) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bodies := f.streams[slug]
	call := f.calls[slug]
	f.calls[slug]++
	body := "event: end\ndata: {}\n\n"
	if call < len(bodies) {
		body = bodies[call]
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// ADR-20261026190244: `gregale logs --project` reads every live service
// together, each line prefixed with its service, and keeps asking a parked
// service while following so its lines appear once it wakes.
func TestProjectLogsInterleaveServices(t *testing.T) {
	client := &fakeProjectLogs{calls: map[string]int{}, streams: map[string][]string{
		"shop-api":     {"data: ready on :8080\n\ndata: GET /orders 200\n\nevent: end\ndata: {}\n\n"},
		"shop-billing": {"event: degraded\ndata: {\"code\":\"not_found\"}\n\n", "data: billing woke\n\nevent: end\ndata: {}\n\n"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out, errOut lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- main.RunProjectLogs(ctx, client, "shop", nil, true, api.LogFilter{}, &out, &errOut, 10*time.Millisecond)
	}()
	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "[billing_api] billing woke") {
		select {
		case <-deadline:
			t.Fatalf("billing never streamed after waking:\n%s", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	got := out.String()
	for _, want := range []string{"[api] ready on :8080", "[api] GET /orders 200", "[billing_api] billing woke"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[old]") {
		t.Fatalf("a deleted service was read:\n%s", got)
	}
}

func TestProjectLogsSelectServicesAndReportParked(t *testing.T) {
	client := &fakeProjectLogs{calls: map[string]int{}, streams: map[string][]string{
		"shop-billing": {"event: degraded\ndata: {\"code\":\"not_found\"}\n\n"},
	}}
	var out, errOut bytes.Buffer
	if code := main.RunProjectLogs(context.Background(), client, "shop", []string{"billing-api"}, false, api.LogFilter{}, &out, &errOut, time.Millisecond); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if client.calls["shop-api"] != 0 || client.calls["shop-billing"] != 1 {
		t.Fatalf("--service billing-api read %v", client.calls)
	}
	if !strings.Contains(errOut.String(), "[billing_api] parked: no running instance") {
		t.Fatalf("parked service not reported:\n%s", errOut.String())
	}
	if code := main.RunProjectLogs(context.Background(), client, "shop", []string{"nope"}, false, api.LogFilter{}, &out, &errOut, time.Millisecond); code == 0 {
		t.Fatal("an unknown service read nothing and succeeded")
	}
}

func TestProjectLogJSON(t *testing.T) {
	if got := main.ProjectLogJSON("api", `{"msg":"ready","level":"info"}`); got != `{"level":"info","msg":"ready","service":"api"}` {
		t.Fatalf("object line = %s", got)
	}
	if got := main.ProjectLogJSON("api", "plain text"); got != `{"line":"plain text","service":"api"}` {
		t.Fatalf("text line = %s", got)
	}
}

// lockedBuffer is a buffer the test reads while stream goroutines write it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
