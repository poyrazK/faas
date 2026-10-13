package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeLogStream yields scripted lines, then EOF.
type fakeLogStream struct {
	grpc.ClientStream
	lines []*vmmdpb.LogsResponse
}

func (s *fakeLogStream) Recv() (*vmmdpb.LogsResponse, error) {
	if len(s.lines) == 0 {
		return nil, io.EOF
	}
	line := s.lines[0]
	s.lines = s.lines[1:]
	return line, nil
}

// fakeLogsVmmd serves each instance's scripted ring once.
type fakeLogsVmmd struct {
	vmmdpb.VmmdClient
	mu       sync.Mutex
	rings    map[string][]string
	requests []*vmmdpb.LogsRequest
}

func (v *fakeLogsVmmd) Logs(_ context.Context, in *vmmdpb.LogsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[vmmdpb.LogsResponse], error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.requests = append(v.requests, in)
	lines, ok := v.rings[in.GetInstance()]
	if !ok {
		return nil, status.Error(codes.NotFound, "instance not live on this vmmd")
	}
	stream := &fakeLogStream{}
	for i, line := range lines {
		stream.lines = append(stream.lines, &vmmdpb.LogsResponse{
			Seq: int64(i + 1), Stream: "stdout", Line: line, WrittenAt: timestamppb.New(time.Unix(1700000000, 0)),
		})
	}
	return stream, nil
}

type fakeLogsNodes struct {
	vmmd    *fakeLogsVmmd
	offline bool
}

func (n *fakeLogsNodes) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if n.offline {
		return nil, nil, false
	}
	return n.vmmd, io.NopCloser(nil), true
}

// scriptedTargets returns each target in turn, then repeats the last one.
type scriptedTargets struct {
	mu      sync.Mutex
	targets []state.JobTaskLogTarget
	err     error
}

func (s *scriptedTargets) JobTaskLogTarget(_ context.Context, _, job, run string, idx int) (state.JobTaskLogTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil || job != "ir-0123" || run != "run-1" || idx != 0 {
		return state.JobTaskLogTarget{}, state.ErrNotFound
	}
	target := s.targets[0]
	if len(s.targets) > 1 {
		s.targets = s.targets[1:]
	}
	return target, nil
}

type jobLogsFixture struct {
	server *httptest.Server
	key    string
	vmmd   *fakeLogsVmmd
}

func newJobLogsFixture(t *testing.T, plan api.Plan, targets *scriptedTargets, nodes *fakeLogsNodes) *jobLogsFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "joblogs@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := mwauth.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream", &JobTaskLogsHandler{
		Auth: auth, Targets: targets, Nodes: nodes, Log: log,
		Poll: 5 * time.Millisecond, Heartbeat: time.Hour, Backstop: 10 * time.Second,
	})
	srv := httptest.NewServer(jobTaskLogsCarveOut{logs: mux, next: http.NotFoundHandler()})
	t.Cleanup(srv.Close)
	return &jobLogsFixture{server: srv, key: key, vmmd: nodes.vmmd}
}

func (f *jobLogsFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

const jobLogsPath = "/v1/jobs/ir-0123/runs/run-1/tasks/0/logs/stream"

// adr: 1163
func TestJobTaskLogsStreamsEveryAttemptUntilTerminal(t *testing.T) {
	targets := &scriptedTargets{targets: []state.JobTaskLogTarget{
		{Status: "queued", Attempt: 1},
		{Status: "claimed", Attempt: 1, InstanceID: "inst-a", NodeID: "node-1"},
		{Status: "queued", Attempt: 2, InstanceID: "inst-a", NodeID: "node-1"},
		{Status: "claimed", Attempt: 2, InstanceID: "inst-b", NodeID: "node-2"},
		{Status: "failed", Attempt: 2, InstanceID: "inst-b"},
	}}
	nodes := &fakeLogsNodes{vmmd: &fakeLogsVmmd{rings: map[string][]string{
		"inst-a": {"first attempt"},
		"inst-b": {"second attempt", "bye"},
	}}}
	f := newJobLogsFixture(t, api.PlanPro, targets, nodes)
	code, body := f.get(t, jobLogsPath+"?since_seq=7")
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
	order := []string{`"line":"first attempt"`, `"line":"second attempt"`, `"line":"bye"`, `event: end`, `"reason":"task_finished"`}
	at := 0
	for _, want := range order {
		i := strings.Index(body[at:], want)
		if i < 0 {
			t.Fatalf("missing %s in order; body=%s", want, body)
		}
		at += i
	}
	f.vmmd.mu.Lock()
	defer f.vmmd.mu.Unlock()
	if len(f.vmmd.requests) != 2 || f.vmmd.requests[0].GetSinceSeq() != 7 || f.vmmd.requests[1].GetSinceSeq() != 1 ||
		!f.vmmd.requests[0].GetFollow() || f.vmmd.requests[1].GetInstance() != "inst-b" {
		t.Fatalf("vmmd requests = %+v; want the cursor on the first attempt only, following each VM once", f.vmmd.requests)
	}
}

// adr: 1163
func TestJobTaskLogsEndsAndRejects(t *testing.T) {
	running := []state.JobTaskLogTarget{{Status: "claimed", Attempt: 1, InstanceID: "inst-a", NodeID: "node-1"}}
	cases := map[string]struct {
		plan    api.Plan
		path    string
		targets *scriptedTargets
		offline bool
		status  int
		want    string
	}{
		"finished before the stream opened": {api.PlanPro, jobLogsPath, &scriptedTargets{targets: []state.JobTaskLogTarget{{Status: "succeeded"}}}, false, http.StatusOK, `"reason":"task_finished"`},
		"node unreachable":                  {api.PlanPro, jobLogsPath, &scriptedTargets{targets: running}, true, http.StatusOK, `"reason":"node_unreachable"`},
		"unknown task":                      {api.PlanPro, "/v1/jobs/ir-0123/runs/run-1/tasks/3/logs/stream", &scriptedTargets{targets: running}, false, http.StatusNotFound, api.CodeJobTaskNotFound},
		"bad index":                         {api.PlanPro, "/v1/jobs/ir-0123/runs/run-1/tasks/x/logs/stream", &scriptedTargets{targets: running}, false, http.StatusBadRequest, api.CodeValidation},
		"bad cursor":                        {api.PlanPro, jobLogsPath + "?since_seq=-1", &scriptedTargets{targets: running}, false, http.StatusBadRequest, api.CodeValidation},
		"free plan":                         {api.PlanFree, jobLogsPath, &scriptedTargets{targets: running}, false, http.StatusPaymentRequired, api.CodeJobsNotAllowed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			nodes := &fakeLogsNodes{vmmd: &fakeLogsVmmd{rings: map[string][]string{"inst-a": {"x"}}}, offline: tc.offline}
			f := newJobLogsFixture(t, tc.plan, tc.targets, nodes)
			code, body := f.get(t, tc.path)
			if code != tc.status || !strings.Contains(body, tc.want) {
				t.Fatalf("status=%d body=%s; want %d with %s", code, body, tc.status, tc.want)
			}
		})
	}
}
