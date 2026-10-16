package main

// adr: 1330

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeJobAttachTargets struct {
	target                state.JobTaskAttachTarget
	err                   error
	account, job, run     string
	index                 int
	calls                 int
	gotAccount, gotJobRun string
}

func (f *fakeJobAttachTargets) JobTaskAttachTarget(_ context.Context, accountID, jobName, runID string, taskIndex int) (state.JobTaskAttachTarget, error) {
	f.calls++
	f.gotAccount, f.gotJobRun = accountID, jobName+"/"+runID
	if f.err != nil {
		return state.JobTaskAttachTarget{}, f.err
	}
	if accountID != f.account || jobName != f.job || runID != f.run || taskIndex != f.index {
		return state.JobTaskAttachTarget{}, state.ErrNotFound
	}
	return f.target, nil
}

type jobAttachFixture struct {
	server  *httptest.Server
	key     string
	freeKey string
	targets *fakeJobAttachTargets
	nodes   *fakeAttachNodes
}

func newJobAttachFixture(t *testing.T) *jobAttachFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	newKey := func(email string, plan api.Plan) (string, state.Account) {
		acct, err := store.CreateAccount(ctx, email, plan)
		if err != nil {
			t.Fatal(err)
		}
		key, hash, _ := api.GenerateAPIKey()
		if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
			t.Fatal(err)
		}
		return key, acct
	}
	key, acct := newKey("job-attach@example.com", api.PlanPro)
	freeKey, _ := newKey("job-attach-free@example.com", api.PlanFree)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := mwauth.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	f := &jobAttachFixture{
		key: key, freeKey: freeKey,
		targets: &fakeJobAttachTargets{account: acct.ID, job: "ir-abc", run: "run-1", target: state.JobTaskAttachTarget{
			Status: "claimed", TTY: true, InstanceID: "inst-1", InstanceState: string(state.StateRunning), NodeID: "node-1",
		}},
		nodes: &fakeAttachNodes{vmmd: &fakeAttachVmmd{token: "tok", starts: make(chan *vmmdpb.AttachAppTaskStart, 4)}},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/attach", &JobTaskAttachHandler{Auth: auth, Targets: f.targets, Nodes: f.nodes, Log: log})
	f.server = httptest.NewServer(appTaskAttachCarveOut{attach: mux, next: http.NotFoundHandler()})
	t.Cleanup(f.server.Close)
	return f
}

func (f *jobAttachFixture) dial(key, token, path string) (*websocket.Conn, *http.Response, error) {
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + path
	header := http.Header{"Authorization": {"Bearer " + key}}
	if token != "" {
		header.Set(api.AppTaskAttachTokenHeader, token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	return dialer.Dial(url, header)
}

func TestJobTaskAttachRelaysSessionToTheJobInstance(t *testing.T) {
	f := newJobAttachFixture(t)
	conn, handshake, err := f.dial(f.key, "tok", api.JobTaskAttachPath("ir-abc", "run-1", 0)+"?rows=30&cols=100")
	if handshake != nil {
		_ = handshake.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	start := <-f.nodes.vmmd.starts
	if start.GetInstance() != "inst-1" || start.GetAttachToken() != "tok" || start.GetRows() != 30 || start.GetCols() != 100 ||
		len(f.nodes.nodes) != 1 || f.nodes.nodes[0] != "node-1" {
		t.Fatalf("start = %+v nodes=%v; want the job instance on its node", start, f.nodes.nodes)
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
	_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStdin}, []byte("ls")...))
	if msg := read(); msg[0] != api.AppTaskAttachStdout || string(msg[1:]) != "LS" {
		t.Fatalf("output message = %q", msg)
	}
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte{api.AppTaskAttachStdinClose})
	msg := read()
	terminal, err := api.DecodeAppTaskAttachTerminal(msg[1:])
	if msg[0] != api.AppTaskAttachTerminal || err != nil || terminal.ExitCode == nil || *terminal.ExitCode != 0 {
		t.Fatalf("terminal = %q (%+v, %v)", msg, terminal, err)
	}
}

func TestJobTaskAttachHTTPFailures(t *testing.T) {
	ready := state.JobTaskAttachTarget{Status: "claimed", InstanceID: "inst-1", InstanceState: string(state.StateRunning), NodeID: "node-1"}
	cases := map[string]struct {
		mutate      func(*jobAttachFixture)
		free        bool
		token       string
		path        string
		status      int
		noTargetHit bool
	}{
		"missing token":   {token: "", status: http.StatusUnauthorized, noTargetHit: true},
		"free plan":       {token: "tok", free: true, status: http.StatusPaymentRequired, noTargetHit: true},
		"index not int":   {token: "tok", path: "/v1/jobs/ir-abc/runs/run-1/tasks/x/attach", status: http.StatusNotFound, noTargetHit: true},
		"negative index":  {token: "tok", path: "/v1/jobs/ir-abc/runs/run-1/tasks/-1/attach", status: http.StatusNotFound, noTargetHit: true},
		"other task":      {token: "tok", path: api.JobTaskAttachPath("ir-abc", "run-1", 1), status: http.StatusNotFound},
		"other job":       {token: "tok", path: api.JobTaskAttachPath("ir-other", "run-1", 0), status: http.StatusNotFound},
		"not interactive": {token: "tok", status: http.StatusNotFound, mutate: func(f *jobAttachFixture) { f.targets.err = state.ErrNotFound }},
		"store error":     {token: "tok", status: http.StatusInternalServerError, mutate: func(f *jobAttachFixture) { f.targets.err = errors.New("db down") }},
		"queued": {token: "tok", status: http.StatusConflict, mutate: func(f *jobAttachFixture) {
			f.targets.target = state.JobTaskAttachTarget{Status: "queued"}
		}},
		"booting": {token: "tok", status: http.StatusConflict, mutate: func(f *jobAttachFixture) {
			f.targets.target.InstanceState = string(state.StateColdBooting)
		}},
		"no node yet": {token: "tok", status: http.StatusConflict, mutate: func(f *jobAttachFixture) { f.targets.target.NodeID = "" }},
		"no instance": {token: "tok", status: http.StatusConflict, mutate: func(f *jobAttachFixture) { f.targets.target.InstanceID = "" }},
		"succeeded":   {token: "tok", status: http.StatusGone, mutate: func(f *jobAttachFixture) { f.targets.target.Status = "succeeded" }},
		"cancelled":   {token: "tok", status: http.StatusGone, mutate: func(f *jobAttachFixture) { f.targets.target.Status = "cancelled" }},
		"wrong token": {token: "nope", status: http.StatusForbidden},
		"node offline": {token: "tok", status: http.StatusServiceUnavailable, mutate: func(f *jobAttachFixture) {
			f.nodes.offline = true
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newJobAttachFixture(t)
			f.targets.target = ready
			if tc.mutate != nil {
				tc.mutate(f)
			}
			path := tc.path
			if path == "" {
				path = api.JobTaskAttachPath("ir-abc", "run-1", 0)
			}
			key := f.key
			if tc.free {
				key = f.freeKey
			}
			conn, resp, err := f.dial(key, tc.token, path)
			if resp != nil {
				_ = resp.Body.Close()
			}
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
			if tc.noTargetHit && f.targets.calls != 0 {
				t.Fatalf("target resolved %d times for a request rejected before lookup", f.targets.calls)
			}
			if tc.status == http.StatusConflict && len(f.nodes.nodes) != 0 {
				t.Fatalf("dialed vmmd for a task that is not ready: %v", f.nodes.nodes)
			}
		})
	}
}

func TestJobTaskAttachCarveOutRoutesOnlyTheAttachPath(t *testing.T) {
	var reached string
	handler := appTaskAttachCarveOut{
		attach: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = "attach" }),
		next:   http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = "next" }),
	}
	for path, want := range map[string]string{
		"/v1/jobs/ir-abc/runs/r1/tasks/0/attach":      "attach",
		"/v1/jobs/ir-abc/runs/r1/tasks/0/logs/stream": "next",
		"/v1/jobs/ir-abc/runs/r1/tasks/0/logs":        "next",
		"/v1/jobs/ir-abc/runs/r1/tasks":               "next",
		"/v1/jobs/ir-abc/runs/r1/tasks/0/attach/x":    "next",
	} {
		reached = ""
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if reached != want {
			t.Fatalf("%s reached %q, want %q", path, reached, want)
		}
	}
	reached = ""
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/jobs/ir-abc/runs/r1/tasks/0/attach", nil))
	if reached != "next" {
		t.Fatalf("POST attach reached %q", reached)
	}
}
