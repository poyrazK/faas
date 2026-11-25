// adr: 20261027090013

package sandboxdata_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxdata"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

var testNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// fakeVmmd relays SandboxCall to an in-memory guest, as vmmd does, and keeps
// the requests it saw.
type fakeVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	mu    sync.Mutex
	seen  []sandboxproto.Request
	files map[string][]byte
	// persists are the persist requests seen; persistErr fails them.
	persists   []*vmmdpb.PersistSandboxWorkspaceRequest
	persistErr error
}

func (f *fakeVmmd) SandboxCall(stream vmmdpb.Vmmd_SandboxCallServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start.GetInstance() != "vm-1" || start.GetSandboxId() != "sbx-1" {
		return status.Error(codes.NotFound, "no such sandbox session")
	}
	var req sandboxproto.Request
	if err := json.Unmarshal(start.GetRequest(), &req); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	f.mu.Lock()
	f.seen = append(f.seen, req)
	f.mu.Unlock()
	host, guest := net.Pipe()
	go func() { _ = sandboxproto.Serve(context.Background(), guest, f.guest) }()
	conn := sandboxproto.NewConn(host)
	defer func() { _ = conn.Close() }()
	ctx := stream.Context()
	if err := conn.WriteJSON(ctx, sandboxproto.FrameRequest, req); err != nil {
		return err
	}
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			_ = conn.Write(ctx, msg.GetFrame().GetType(), msg.GetFrame().GetBody())
		}
	}()
	for {
		frameType, body, err := conn.Read(ctx)
		if err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
		if err := stream.Send(&vmmdpb.SandboxCallEvent{Frame: &vmmdpb.SandboxFrame{Type: frameType, Body: body}}); err != nil {
			return err
		}
		if frameType == sandboxproto.FrameResult || frameType == sandboxproto.FrameError {
			return nil
		}
	}
}

func (f *fakeVmmd) guest(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	switch req.Op {
	case sandboxproto.OpExec:
		if req.Background {
			return sandboxproto.ExecResult{Handle: "cmd-1"}, nil
		}
		_, _ = call.Stdout().Write([]byte(strings.Join(req.Argv, " ")))
		_, _ = call.Stderr().Write([]byte("warn\xff"))
		return sandboxproto.ExecResult{ExitCode: 3, WallTimeMillis: 1500}, nil
	case sandboxproto.OpFileWrite:
		var buf bytes.Buffer
		n, err := call.ReadUpload(&buf, 1<<20)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.files[req.Path] = buf.Bytes()
		f.mu.Unlock()
		return sandboxproto.TransferResult{Bytes: n}, nil
	case sandboxproto.OpFileRead:
		if req.Path == "broken" {
			_, _ = call.Data().Write([]byte("par"))
			return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeInternal}
		}
		f.mu.Lock()
		data, ok := f.files[req.Path]
		f.mu.Unlock()
		if ok {
			_, _ = call.Data().Write(data)
			return sandboxproto.TransferResult{Bytes: int64(len(data))}, nil
		}
	case sandboxproto.OpFileRename:
		return struct{}{}, nil
	case sandboxproto.OpExecInput:
		return sandboxproto.Command{Handle: req.Handle, Running: true, TTY: true}, nil
	case sandboxproto.OpSessionEnv:
		return sandboxproto.SessionEnvResult{Names: []string{"TOKEN"}}, nil
	case sandboxproto.OpCodeRun:
		_, _ = call.Stdout().Write([]byte("hi\n"))
		count := 3
		return sandboxproto.CodeRunResult{Results: []sandboxproto.CodeOutput{{Data: map[string]json.RawMessage{"text/plain": json.RawMessage(`"42"`)},
			IsMainResult: true}}, ExecutionCount: &count}, nil
	case sandboxproto.OpCodeContext:
		switch req.Action {
		case sandboxproto.CodeActionCreate:
			return sandboxproto.CodeContextResult{Context: &sandboxproto.CodeContext{ID: "abc123", Language: "python", Cwd: "/workspace"}}, nil
		case sandboxproto.CodeActionList:
			return sandboxproto.CodeContextResult{Contexts: []sandboxproto.CodeContext{{ID: "default", Language: "python", Cwd: "/workspace"}}}, nil
		}
		return sandboxproto.CodeContextResult{}, nil
	case sandboxproto.OpArchiveExtract:
		n, err := call.ReadUpload(io.Discard, 1<<20)
		if err != nil {
			return nil, err
		}
		if n > req.MaxBytes {
			return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeTooLarge, Message: "too large"}
		}
		return sandboxproto.ArchiveResult{Bytes: n, Entries: 2}, nil
	}
	return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeNotFound, Message: req.Path + " does not exist"}
}

func (f *fakeVmmd) requests() []sandboxproto.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sandboxproto.Request(nil), f.seen...)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// nodes serves node-a's vmmd.
type nodes struct{ client vmmdpb.VmmdClient }

func (n nodes) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if nodeID != "node-a" {
		return nil, nil, false
	}
	return n.client, nopCloser{}, true
}

type fixture struct {
	vmmd   *fakeVmmd
	signer *sandboxtoken.Issuer
	mux    *http.ServeMux
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	vmmd := &fakeVmmd{files: map[string][]byte{}}
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, vmmd)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	signer := newSigner(t, "a")
	mux := http.NewServeMux()
	(&sandboxdata.Handler{Tokens: sandboxtoken.NewVerifier(signer.PublicKey()), Nodes: nodes{client: vmmdpb.NewVmmdClient(conn)}, Now: func() time.Time { return testNow }}).Routes(mux)
	return fixture{vmmd: vmmd, signer: signer, mux: mux}
}

func newSigner(t *testing.T, fill string) *sandboxtoken.Issuer {
	t.Helper()
	s, err := sandboxtoken.NewIssuer([]byte(strings.Repeat(fill, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func placed() sandboxtoken.Claims {
	return sandboxtoken.Claims{SandboxID: "sbx-1", AccountID: "acct-1", NodeID: "node-a", InstanceID: "vm-1",
		IssuedAt: testNow.Add(-time.Minute), ExpiresAt: testNow.Add(10 * time.Minute), ExecTimeoutMaxMillis: 300_000, ExecOutputMaxBytes: 1 << 20}
}

func (f fixture) token(t *testing.T, c sandboxtoken.Claims) string {
	t.Helper()
	tok, err := f.signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f fixture) do(t *testing.T, method, target, token string, body io.Reader, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body %q: %v", rec.Body.String(), err)
	}
	return p.Code
}

func TestExecRunsACommandLine(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec", f.token(t, placed()), strings.NewReader(`{"cmd":"echo hi"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got sandboxdata.ExecResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "/bin/sh -c echo hi" || got.Stderr != "warn�" || got.ExitCode != 3 || got.WallTimeSeconds != 1.5 {
		t.Fatalf("response = %+v", got)
	}
	req := f.vmmd.requests()[0]
	if req.TimeoutMillis != 60_000 || req.MaxOutputBytes != 1<<20 {
		t.Fatalf("guest request = %+v", req)
	}
}

func TestExecStreamsEvents(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec", f.token(t, placed()), strings.NewReader(`{"cmd":["ls","-l"]}`),
		"Accept", "text/event-stream")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(body, "event: stdout\ndata: \"ls -l\"\n\n") || !strings.Contains(body, "event: result\ndata: {") {
		t.Fatalf("status = %d, body %q", rec.Code, body)
	}
}

func TestExecInBackgroundAnswersAHandle(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec", f.token(t, placed()), strings.NewReader(`{"cmd":"sleep 9","background":true}`))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"handle":"cmd-1"`) {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestExecRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"timeout over the plan", `{"cmd":"true","timeout_ms":300001}`, api.CodeAgentLimit, http.StatusForbidden},
		{"output over the plan", `{"cmd":"true","max_output_bytes":1048577}`, api.CodeAgentLimit, http.StatusForbidden},
		{"terminal in the foreground", `{"cmd":"bash","tty":true}`, api.CodeSandboxUnsupported, http.StatusUnprocessableEntity},
		{"no command", `{"cmd":[]}`, api.CodeValidation, http.StatusBadRequest},
		{"guest validation", `{"cmd":"true","run_as":"nobody"}`, api.CodeValidation, http.StatusBadRequest},
		{"a size without a terminal", `{"cmd":"bash","background":true,"rows":24,"cols":80}`, api.CodeValidation, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec", f.token(t, placed()), strings.NewReader(tc.body))
			if rec.Code != tc.status || problemCode(t, rec) != tc.code {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			if seen := f.vmmd.requests(); len(seen) != 0 {
				t.Fatalf("a refused call reached the node: %+v", seen)
			}
		})
	}
}

// adr: 20261027090013 step 12 — a terminal command runs in the background,
// and its input, end of input and resizes reach the guest as exec_input.
func TestBackgroundTerminalAndInput(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec", f.token(t, placed()),
		strings.NewReader(`{"cmd":"bash","tty":true,"background":true,"rows":24,"cols":80}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if req := f.vmmd.requests()[0]; !req.TTY || req.Rows != 24 || req.Cols != 80 || !req.Background {
		t.Fatalf("guest request = %+v", req)
	}
	const handle = "c0123456789abcdef"
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec/"+handle+"/input", f.token(t, placed()),
		strings.NewReader(`{"data":"bHMK","eof":true,"rows":30,"cols":100}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tty":true`) {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	req := f.vmmd.requests()[1]
	if req.Op != sandboxproto.OpExecInput || req.Handle != handle || string(req.Input) != "ls\n" || !req.CloseInput ||
		req.Rows != 30 || req.Cols != 100 {
		t.Fatalf("guest request = %+v", req)
	}
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/exec/"+handle+"/input", f.token(t, placed()), strings.NewReader(`{}`))
	if rec.Code != http.StatusBadRequest || problemCode(t, rec) != api.CodeValidation {
		t.Fatalf("empty input: status = %d, body %s", rec.Code, rec.Body)
	}
	if len(f.vmmd.requests()) != 2 {
		t.Fatal("a refused input reached the node")
	}
}

// adr: 20261027090013 step 13 — the session's environment goes to the
// guest as session_env, and a bad name never reaches the node.
func TestSessionEnv(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPut, "/v1/sandboxes/sbx-1/env", f.token(t, placed()), strings.NewReader(`{"env":{"TOKEN":"s3cret"}}`))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"names":["TOKEN"]}` {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if req := f.vmmd.requests()[0]; req.Op != sandboxproto.OpSessionEnv || req.Env["TOKEN"] != "s3cret" {
		t.Fatalf("guest request = %+v", req)
	}
	rec = f.do(t, http.MethodPut, "/v1/sandboxes/sbx-1/env", f.token(t, placed()), strings.NewReader(`{"env":{"1BAD":"x"}}`))
	if rec.Code != http.StatusBadRequest || len(f.vmmd.requests()) != 1 {
		t.Fatalf("a bad name: status = %d, body %s", rec.Code, rec.Body)
	}
	if !api.IsSandboxDataPath("/v1/sandboxes/sbx-1/env") {
		t.Fatal("the env route is not a data-plane path")
	}
}

// adr: 20261027090013 step 14 — a run answers its results, logs and
// execution count within the plan's caps, contexts are created, listed,
// restarted and removed, and a language other than python is refused.
func TestCodeRoutes(t *testing.T) {
	f := newFixture(t)
	tok := f.token(t, placed())
	rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/code", tok, strings.NewReader(`{"code":"x + 2","context":"abc123"}`))
	want := `{"results":[{"data":{"text/plain":"42"},"is_main_result":true}],"logs":{"stdout":["hi\n"],"stderr":[]},"execution_count":3}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if req := f.vmmd.requests()[0]; req.Op != sandboxproto.OpCodeRun || req.Context != "abc123" || req.TimeoutMillis != 60_000 || req.MaxOutputBytes != 1<<20 {
		t.Fatalf("guest request = %+v", req)
	}
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/code", tok, strings.NewReader(`{"code":"1"}`), "Accept", "text/event-stream")
	if body := rec.Body.String(); !strings.Contains(body, "event: stdout\ndata: \"hi\\n\"") || !strings.Contains(body, `"execution_count":3`) {
		t.Fatalf("stream %q", body)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/v1/sandboxes/sbx-1/code/contexts", `{"cwd":"app"}`, http.StatusCreated},
		{http.MethodGet, "/v1/sandboxes/sbx-1/code/contexts", "", http.StatusOK},
		{http.MethodPost, "/v1/sandboxes/sbx-1/code/contexts/abc123/restart", "", http.StatusOK},
		{http.MethodDelete, "/v1/sandboxes/sbx-1/code/contexts/abc123", "", http.StatusOK},
	} {
		if rec := f.do(t, tc.method, tc.path, tok, strings.NewReader(tc.body)); rec.Code != tc.status {
			t.Fatalf("%s %s: status = %d, body %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	seen := f.vmmd.requests()
	if create := seen[2]; create.Action != sandboxproto.CodeActionCreate || create.Workdir != "app" {
		t.Fatalf("create = %+v", create)
	}
	if remove := seen[5]; remove.Action != sandboxproto.CodeActionRemove || remove.Context != "abc123" {
		t.Fatalf("remove = %+v", remove)
	}
	for name, body := range map[string]string{
		"another language":        `{"code":"1","language":"r"}`,
		"a timeout over the plan": `{"code":"1","timeout_ms":300001}`,
	} {
		if rec := f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/code", tok, strings.NewReader(body)); rec.Code < 400 {
			t.Fatalf("%s: status = %d", name, rec.Code)
		}
	}
	if len(f.vmmd.requests()) != 6 {
		t.Fatal("a refused run reached the node")
	}
	if !api.IsSandboxDataPath("/v1/sandboxes/sbx-1/code/contexts") {
		t.Fatal("the code routes are not data-plane paths")
	}
}

func TestTokenChecks(t *testing.T) {
	f := newFixture(t)
	expired := placed()
	expired.ExpiresAt = testNow.Add(-time.Second)
	for _, tc := range []struct {
		name, path, token string
	}{
		{"no token", "/v1/sandboxes/sbx-1/files:stat?path=a", ""},
		{"another session", "/v1/sandboxes/sbx-2/files:stat?path=a", f.token(t, placed())},
		{"expired", "/v1/sandboxes/sbx-1/files:stat?path=a", f.token(t, expired)},
		{"another key", "/v1/sandboxes/sbx-1/files:stat?path=a", fixture{signer: newSigner(t, "b")}.token(t, placed())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := f.do(t, http.MethodGet, tc.path, tc.token, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
		})
	}
	if len(f.vmmd.requests()) != 0 {
		t.Fatalf("an unauthenticated call reached the node")
	}
}

func TestUnplacedSessionIsNotReady(t *testing.T) {
	f := newFixture(t)
	c := placed()
	c.NodeID, c.InstanceID = "", ""
	rec := f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/exec", f.token(t, c), nil)
	if rec.Code != http.StatusConflict || rec.Header().Get("Retry-After") != "1" || problemCode(t, rec) != api.CodeSandboxNotReady {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestSessionGoneFromItsNode(t *testing.T) {
	f := newFixture(t)
	c := placed()
	c.InstanceID = "vm-old"
	rec := f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/files:stat?path=a", f.token(t, c), nil)
	if rec.Code != http.StatusNotFound || problemCode(t, rec) != api.CodeSandboxNotFound {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	c = placed()
	c.NodeID = "node-gone"
	rec = f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/files:stat?path=a", f.token(t, c), nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unreachable node: status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestFilesRoundTrip(t *testing.T) {
	f := newFixture(t)
	tok := f.token(t, placed())
	rec := f.do(t, http.MethodPut, "/v1/sandboxes/sbx-1/files?path=notes/a.txt&parents=true&mode=0640", tok, strings.NewReader("hello world"))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"bytes":11}` {
		t.Fatalf("write: status = %d, body %s", rec.Code, rec.Body)
	}
	if w := f.vmmd.requests()[0]; !w.Parents || w.Mode != 0o640 {
		t.Fatalf("write request = %+v", w)
	}
	rec = f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/files?path=notes/a.txt", tok, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Body.String() != "hello world" {
		t.Fatalf("read: status = %d, body %q", rec.Code, rec.Body)
	}
	rec = f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/files?path=missing", tok, nil)
	if rec.Code != http.StatusNotFound || problemCode(t, rec) != api.CodeSandboxEntryNotFound {
		t.Fatalf("missing: status = %d, body %s", rec.Code, rec.Body)
	}
	rec = f.do(t, http.MethodPost, "/v1/sandboxes/sbx-1/files:rename", tok, strings.NewReader(`{"from":"notes/a.txt","to":"b.txt"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body %s", rec.Code, rec.Body)
	}
	seen := f.vmmd.requests()
	if r := seen[len(seen)-1]; r.Op != sandboxproto.OpFileRename || r.Path != "notes/a.txt" || r.To != "b.txt" {
		t.Fatalf("rename request = %+v", r)
	}
}

func TestFileReadFailingMidwayAborts(t *testing.T) {
	f := newFixture(t)
	defer func() {
		got := recover()
		if err, _ := got.(error); !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", got)
		}
	}()
	f.do(t, http.MethodGet, "/v1/sandboxes/sbx-1/files?path=broken", f.token(t, placed()), nil)
	t.Fatal("a partial read completed")
}

func TestIsSandboxDataPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/sandboxes/sbx-1/exec":              true,
		"/v1/sandboxes/sbx-1/exec/cmd-1":        true,
		"/v1/sandboxes/sbx-1/files":             true,
		"/v1/sandboxes/sbx-1/files:list":        true,
		"/v1/sandboxes/sbx-1/stdin":             true,
		"/v1/sandboxes/sbx-1/workspace:persist": true,
		"/v1/sandboxes":                         false,
		"/v1/sandboxes/sbx-1":                   false,
		"/v1/sandboxes/sbx-1/ports":             false,
		"/v1/sandboxes/sbx-1/ports/3000":        false,
		"/v1/sandboxes//exec":                   false,
		"/v1/sandboxesx/sbx-1/exec":             false,
	} {
		if got := api.IsSandboxDataPath(path); got != want {
			t.Errorf("IsSandboxDataPath(%q) = %v, want %v", path, got, want)
		}
	}
}
