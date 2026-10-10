package main

// adr: 958

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeAttachStream is vmmd's side of AttachAppTask: it checks the start
// frame, echoes stdin upper-cased, and ends with exit 0 on stdin close.
type fakeAttachStream struct {
	grpc.ClientStream
	ctx    context.Context
	events chan *vmmdpb.AttachAppTaskEvent
	start  chan *vmmdpb.AttachAppTaskStart
	token  string
	once   sync.Once
}

func (s *fakeAttachStream) Context() context.Context { return s.ctx }
func (s *fakeAttachStream) CloseSend() error         { return nil }

func (s *fakeAttachStream) Send(req *vmmdpb.AttachAppTaskRequest) error {
	switch frame := req.GetFrame().(type) {
	case *vmmdpb.AttachAppTaskRequest_Start:
		s.start <- frame.Start
		if frame.Start.GetAttachToken() != s.token {
			close(s.events)
			return nil
		}
		s.events <- &vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Attached{Attached: &vmmdpb.AttachAppTaskAttached{}}}
	case *vmmdpb.AttachAppTaskRequest_Stdin:
		s.events <- &vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Output{
			Output: &vmmdpb.ExecuteAppTaskOutputChunk{Stream: "stdout", Chunk: bytes.ToUpper(frame.Stdin)},
		}}
	case *vmmdpb.AttachAppTaskRequest_StdinClose:
		s.once.Do(func() {
			s.events <- &vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Terminal{
				Terminal: &vmmdpb.ExecuteAppTaskResponse{Status: "succeeded", ExitCode: wrapperspb.Int32(0)},
			}}
		})
	}
	return nil
}

func (s *fakeAttachStream) Recv() (*vmmdpb.AttachAppTaskEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "attach token rejected")
		}
		return event, nil
	case <-s.ctx.Done():
		return nil, status.Error(codes.Canceled, "client gone")
	}
}

type fakeAttachVmmd struct {
	vmmdpb.VmmdClient
	token  string
	starts chan *vmmdpb.AttachAppTaskStart
}

func (v *fakeAttachVmmd) AttachAppTask(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.AttachAppTaskRequest, vmmdpb.AttachAppTaskEvent], error) {
	return &fakeAttachStream{ctx: ctx, events: make(chan *vmmdpb.AttachAppTaskEvent, 8), start: v.starts, token: v.token}, nil
}

type fakeAttachNodes struct {
	vmmd    *fakeAttachVmmd
	offline bool
	nodes   []string
}

func (n *fakeAttachNodes) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	n.nodes = append(n.nodes, nodeID)
	if n.offline {
		return nil, nil, false
	}
	return n.vmmd, io.NopCloser(nil), true
}

type fakeAttachTargets struct {
	target state.AppTaskAttachTarget
	err    error
	appID  string
}

func (f *fakeAttachTargets) AppTaskAttachTarget(_ context.Context, _, appID, taskID string) (state.AppTaskAttachTarget, error) {
	if f.err != nil {
		return state.AppTaskAttachTarget{}, f.err
	}
	if appID != f.appID || taskID != f.target.TaskID {
		return state.AppTaskAttachTarget{}, state.ErrNotFound
	}
	return f.target, nil
}

type attachFixture struct {
	server  *httptest.Server
	key     string
	app     state.App
	targets *fakeAttachTargets
	nodes   *fakeAttachNodes
}

func newAttachFixture(t *testing.T) *attachFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "attach@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := mwauth.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	f := &attachFixture{
		key: key, app: app,
		targets: &fakeAttachTargets{appID: app.ID, target: state.AppTaskAttachTarget{
			TaskID: "task-1", TaskStatus: state.AppTaskRunning, TTY: true, NodeID: "node-1",
		}},
		nodes: &fakeAttachNodes{vmmd: &fakeAttachVmmd{token: "tok", starts: make(chan *vmmdpb.AttachAppTaskStart, 4)}},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/apps/{slug}/tasks/{id}/attach", &AppTaskAttachHandler{Auth: auth, Targets: f.targets, Nodes: f.nodes, Log: log})
	f.server = httptest.NewServer(appTaskAttachCarveOut{attach: mux, next: http.NotFoundHandler()})
	t.Cleanup(f.server.Close)
	return f
}

func (f *attachFixture) dial(token string, query string) (*websocket.Conn, *http.Response, error) {
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + api.AppTaskAttachPath(f.app.Slug, "task-1") + query
	header := http.Header{"Authorization": {"Bearer " + f.key}}
	if token != "" {
		header.Set(api.AppTaskAttachTokenHeader, token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	return dialer.Dial(url, header)
}

func TestAppTaskAttachRelaysSession(t *testing.T) {
	f := newAttachFixture(t)
	conn, _, err := f.dial("tok", "?rows=40&cols=120")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	start := <-f.nodes.vmmd.starts
	if start.GetInstance() != "task-1" || start.GetRows() != 40 || start.GetCols() != 120 || f.nodes.nodes[0] != "node-1" {
		t.Fatalf("start = %+v nodes=%v", start, f.nodes.nodes)
	}
	read := func() []byte {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return msg
	}
	if msg := read(); len(msg) != 1 || msg[0] != api.AppTaskAttachAttached {
		t.Fatalf("first message = %v", msg)
	}
	_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStdin}, []byte("hi")...))
	if msg := read(); msg[0] != api.AppTaskAttachStdout || string(msg[1:]) != "HI" {
		t.Fatalf("output message = %q", msg)
	}
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte{api.AppTaskAttachStdinClose})
	msg := read()
	if msg[0] != api.AppTaskAttachTerminal {
		t.Fatalf("terminal message = %q", msg)
	}
	terminal, err := api.DecodeAppTaskAttachTerminal(msg[1:])
	if err != nil || terminal.Status != api.AppTaskStatusSucceeded || terminal.ExitCode == nil || *terminal.ExitCode != 0 {
		t.Fatalf("terminal = %+v, %v", terminal, err)
	}
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("close = %v", err)
	}
}

func TestAppTaskAttachHTTPFailures(t *testing.T) {
	cases := map[string]struct {
		mutate func(*attachFixture)
		token  string
		status int
	}{
		"missing token":  {token: "", status: http.StatusUnauthorized},
		"wrong token":    {token: "nope", status: http.StatusForbidden},
		"not found":      {token: "tok", status: http.StatusNotFound, mutate: func(f *attachFixture) { f.targets.err = state.ErrNotFound }},
		"store error":    {token: "tok", status: http.StatusInternalServerError, mutate: func(f *attachFixture) { f.targets.err = errors.New("db down") }},
		"still starting": {token: "tok", status: http.StatusConflict, mutate: func(f *attachFixture) { f.targets.target.TaskStatus = state.AppTaskRestoring }},
		"no node yet":    {token: "tok", status: http.StatusConflict, mutate: func(f *attachFixture) { f.targets.target.NodeID = "" }},
		"finished":       {token: "tok", status: http.StatusGone, mutate: func(f *attachFixture) { f.targets.target.TaskStatus = state.AppTaskSucceeded }},
		"node offline":   {token: "tok", status: http.StatusServiceUnavailable, mutate: func(f *attachFixture) { f.nodes.offline = true }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAttachFixture(t)
			if tc.mutate != nil {
				tc.mutate(f)
			}
			conn, resp, err := f.dial(tc.token, "")
			if err == nil {
				_ = conn.Close()
				t.Fatal("dial succeeded")
			}
			if resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("status = %v, want %d (err %v)", resp, tc.status, err)
			}
			if tc.status == http.StatusConflict && resp.Header.Get("Retry-After") == "" {
				t.Fatal("409 without Retry-After")
			}
		})
	}
}

func TestAppTaskAttachCarveOutPassesOtherPaths(t *testing.T) {
	var reached string
	handler := appTaskAttachCarveOut{
		attach: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = "attach" }),
		next:   http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = "next" }),
	}
	for path, want := range map[string]string{
		"/v1/apps/api/tasks/t1/attach": "attach",
		"/v1/apps/api/tasks/t1":        "next",
		"/v1/apps/api/logs":            "next",
	} {
		reached = ""
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if reached != want {
			t.Fatalf("%s reached %q, want %q", path, reached, want)
		}
	}
	reached = ""
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/apps/api/tasks/t1/attach", nil))
	if reached != "next" {
		t.Fatalf("POST attach reached %q", reached)
	}
}
