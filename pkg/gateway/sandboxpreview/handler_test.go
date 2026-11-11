// adr: 20261027090013 §11 — a preview host reaches its exposed guest port
// through port_connect; a private port needs its own preview token (bearer,
// cookie, or the one-time query parameter swapped for the cookie), a public
// port needs none; unknown hosts, unexposed ports, ended sessions and other
// sessions' or ports' tokens answer 404; the platform's cookie and headers
// never reach the guest; routes are cached, misses included.
package sandboxpreview_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxpreview"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

const (
	domain    = "preview.example.test"
	sandboxID = "0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"
	otherID   = "1b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"
	account   = "acct-1"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// fakeVmmd answers port_connect as guest-init does, with ports mapped to
// local listeners.
type fakeVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	ports map[int]string
}

func (f *fakeVmmd) SandboxCall(stream vmmdpb.Vmmd_SandboxCallServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start.GetInstance() != "vm-1" || start.GetSandboxId() != sandboxID {
		return status.Error(codes.NotFound, "no such sandbox session")
	}
	var req sandboxproto.Request
	if err := json.Unmarshal(start.GetRequest(), &req); err != nil || req.Validate() != nil {
		return status.Error(codes.InvalidArgument, "bad request")
	}
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

func (f *fakeVmmd) guest(ctx context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	addr, ok := f.ports[req.Port]
	if req.Op != sandboxproto.OpPortConnect || !ok {
		return nil, &sandboxproto.GuestError{Code: sandboxproto.CodePortClosed, Message: "nothing listens"}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &sandboxproto.GuestError{Code: sandboxproto.CodePortClosed, Message: "nothing listens"}
	}
	defer func() { _ = conn.Close() }()
	go func() {
		if _, err := call.ReadUpload(conn, math.MaxInt64); err != nil {
			_ = conn.Close()
			return
		}
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	out, _ := io.Copy(call.Data(), conn)
	return sandboxproto.PortResult{BytesOut: out}, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

type nodes struct {
	client vmmdpb.VmmdClient
	fake   *fakeVmmd
}

func (n nodes) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if nodeID != "node-a" {
		return nil, nil, false
	}
	return n.client, nopCloser{}, true
}

// routes serves one route and counts reads.
type routes struct {
	mu    sync.Mutex
	route sandboxpreview.Route
	reads atomic.Int32
}

func (r *routes) PreviewRoute(_ context.Context, id string) (sandboxpreview.Route, error) {
	r.reads.Add(1)
	if id != sandboxID {
		return sandboxpreview.Route{}, sandboxpreview.ErrNoRoute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.route, nil
}

type fixture struct {
	h      *sandboxpreview.Handler
	routes *routes
	signer *sandboxtoken.Issuer
	seen   chan *http.Request
	now    *time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	seen := make(chan *http.Request, 16)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		w.Header().Set("X-App", "yes")
		_, _ = io.WriteString(w, "hello "+r.URL.RequestURI())
	}))
	t.Cleanup(app.Close)
	vmmd := &fakeVmmd{ports: map[int]string{3000: app.Listener.Addr().String(), 8080: app.Listener.Addr().String()}}
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
	signer, err := sandboxtoken.NewIssuer([]byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	rt := &routes{route: sandboxpreview.Route{AccountID: account, Plan: api.PlanPro, Live: true, NodeID: "node-a", InstanceID: "vm-1",
		Ports: map[int]string{3000: api.SandboxPortPrivate, 8080: api.SandboxPortPublic, 9000: api.SandboxPortPublic}}}
	now := testNow
	h := &sandboxpreview.Handler{Domain: domain, Routes: rt, Tokens: sandboxtoken.NewVerifier(signer.PublicKey()),
		Nodes: nodes{client: vmmdpb.NewVmmdClient(conn), fake: vmmd}, Now: func() time.Time { return now }}
	return fixture{h: h, routes: rt, signer: signer, seen: seen, now: &now}
}

func (f fixture) token(t *testing.T, id string, port int) string {
	t.Helper()
	tok, err := f.signer.SignPreview(sandboxtoken.PreviewClaims{SandboxID: id, AccountID: account, Port: port,
		IssuedAt: testNow, ExpiresAt: testNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f fixture) do(t *testing.T, host, target string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Add(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func host(port int, id string) string { return api.SandboxPreviewHost(port, id, domain) }

func TestPublicPortAnswersAnyoneWithoutPlatformHeaders(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, host(8080, sandboxID), "/hi?x=1", "Cookie", sandboxpreview.CookieName+"=secret; theirs=1",
		"X-Faas-Account", "acct-1", "Authorization", "Basic app-own")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello /hi?x=1" || rec.Header().Get("X-App") != "yes" {
		t.Fatalf("public = %d %q", rec.Code, rec.Body.String())
	}
	got := <-f.seen
	if got.Header.Get("X-Faas-Account") != "" || strings.Contains(got.Header.Get("Cookie"), "secret") || got.Header.Get("Cookie") != "theirs=1" {
		t.Fatalf("platform headers reached the guest: %v", got.Header)
	}
	if got.Header.Get("Authorization") != "Basic app-own" || got.Host != host(8080, sandboxID) || got.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("the app's own request changed: host %q %v", got.Host, got.Header)
	}
}

func TestPrivatePortNeedsItsToken(t *testing.T) {
	f := newFixture(t)
	h := host(3000, sandboxID)
	if rec := f.do(t, h, "/"); rec.Code != http.StatusNotFound {
		t.Fatalf("no token = %d", rec.Code)
	}
	for name, tok := range map[string]string{"another port": f.token(t, sandboxID, 8080), "another session": f.token(t, otherID, 3000), "garbage": "x"} {
		if rec := f.do(t, h, "/", "Authorization", "Bearer "+tok); rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", name, rec.Code)
		}
	}
	tok := f.token(t, sandboxID, 3000)
	rec := f.do(t, h, "/", "Authorization", "Bearer "+tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer = %d %s", rec.Code, rec.Body.String())
	}
	if got := <-f.seen; got.Header.Get("Authorization") != "" {
		t.Fatal("the preview token reached the guest")
	}
	// A browser opens the URL with the token once and gets a host-only
	// cookie and a redirect without it.
	rec = f.do(t, h, "/app?"+api.SandboxPreviewTokenParam+"="+url.QueryEscape(tok)+"&tab=2")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app?tab=2" {
		t.Fatalf("token param = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != sandboxpreview.CookieName || !cookie[0].Secure || !cookie[0].HttpOnly || cookie[0].Domain != "" {
		t.Fatalf("cookie = %+v", cookie)
	}
	if rec := f.do(t, h, "/app?tab=2", "Cookie", cookie[0].Name+"="+cookie[0].Value); rec.Code != http.StatusOK {
		t.Fatalf("cookie = %d", rec.Code)
	}
	if got := <-f.seen; got.Header.Get("Cookie") != "" {
		t.Fatalf("the preview cookie reached the guest: %q", got.Header.Get("Cookie"))
	}
	if rec := f.do(t, h, "/?"+api.SandboxPreviewTokenParam+"=bad"); rec.Code != http.StatusNotFound {
		t.Fatalf("bad token param = %d", rec.Code)
	}
}

func TestUnroutableHostsAnswer404(t *testing.T) {
	f := newFixture(t)
	for name, h := range map[string]string{
		"another domain":  "8080-" + sandboxID + ".elsewhere.test",
		"not exposed":     host(4000, sandboxID),
		"unknown session": host(8080, otherID),
		"leading zero":    "08080-" + sandboxID + "." + domain,
		"nested label":    "a.8080-" + sandboxID + "." + domain,
	} {
		if rec := f.do(t, h, "/"); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	// An exposed port nothing listens on is the session's to fix.
	rec := f.do(t, host(9000, sandboxID), "/")
	var p api.Problem
	if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Code != api.CodeSandboxPortClosed {
		t.Fatalf("closed port = %d %s", rec.Code, rec.Body.String())
	}
	// An ended session stops answering once its cached route expires.
	f.routes.mu.Lock()
	f.routes.route.Live = false
	f.routes.mu.Unlock()
	*f.now = f.now.Add(sandboxpreview.RouteCacheTTL)
	if rec := f.do(t, host(8080, sandboxID), "/"); rec.Code != http.StatusNotFound {
		t.Fatalf("ended session = %d", rec.Code)
	}
}

func TestRoutesAreCachedMissesIncluded(t *testing.T) {
	f := newFixture(t)
	for range 5 {
		f.do(t, host(8080, sandboxID), "/")
		<-f.seen
		f.do(t, host(8080, otherID), "/")
	}
	if n := f.routes.reads.Load(); n != 2 {
		t.Fatalf("%d route reads for 10 requests to 2 sessions", n)
	}
	*f.now = f.now.Add(sandboxpreview.RouteCacheTTL)
	f.do(t, host(8080, otherID), "/")
	if n := f.routes.reads.Load(); n != 3 {
		t.Fatalf("an expired miss was not read again: %d", n)
	}
}

func TestRequestRateIsCapped(t *testing.T) {
	f := newFixture(t)
	f.routes.mu.Lock()
	f.routes.route.Plan = api.PlanHobby
	f.routes.mu.Unlock()
	burst := 2 * api.SandboxPreviewRequestsPerSec[api.PlanHobby.PlanIndex()]
	limited := 0
	for range burst + 1 {
		rec := f.do(t, host(8080, sandboxID), "/")
		switch rec.Code {
		case http.StatusOK:
			<-f.seen
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("status %d", rec.Code)
		}
	}
	if limited != 1 {
		t.Fatalf("%d of %d requests limited; want 1", limited, burst+1)
	}
}

// An upgraded connection (a WebSocket, a dev server's hot reload) is
// spliced both ways through the same port_connect call.
func TestUpgradedConnectionsAreSpliced(t *testing.T) {
	f := newFixture(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf) // the upgrade request
		if !strings.Contains(string(buf[:n]), "Upgrade: echo") {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		_, _ = io.Copy(conn, conn)
	}()
	f.h.Nodes.(nodes).fake.ports[3001] = echo.Addr().String()
	f.routes.mu.Lock()
	f.routes.route.Ports[3001] = api.SandboxPortPublic
	f.routes.mu.Unlock()
	front := httptest.NewServer(f.h)
	t.Cleanup(front.Close)
	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: "+host(3001, sandboxID)+"\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 101") {
		t.Fatalf("upgrade = %q, %v", buf[:n], err)
	}
	_, _ = io.WriteString(conn, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}
