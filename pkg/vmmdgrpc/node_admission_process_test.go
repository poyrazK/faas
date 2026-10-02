// adr: 375
package vmmdgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/gateway"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type nodeAdmissionProcessVMM struct {
	VmmdAPI
	owner *fcvm.Manager
}

func (v nodeAdmissionProcessVMM) AcquireHTTPForward(ctx context.Context, instance string) (context.Context, func(), error) {
	return v.owner.AcquireHTTPForward(ctx, instance)
}
func (v nodeAdmissionProcessVMM) NetnsFor(instance string) (string, bool) {
	return v.owner.NetnsFor(instance)
}

type nodeAdmissionClient struct{ client vmmdpb.VmmdClient }

func (c nodeAdmissionClient) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return c.client, io.NopCloser(strings.NewReader("")), true
}

func TestNodeAdmissionHTTPGatewayProcess(t *testing.T) {
	addr := os.Getenv("GREGALE_NODE_ADMISSION_CHILD_ADDR")
	if addr == "" {
		t.Skip("subprocess helper")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port, err := strconv.Atoi(os.Getenv("GREGALE_NODE_ADMISSION_CHILD_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := gateway.ForwardingReverseProxy(nodeAdmissionClient{vmmdpb.NewVmmdClient(conn)}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	forward := proxy(gateway.Target{NodeID: "node", InstanceID: "vm", Port: port})
	childHandler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stand in for the trusted target selection performed by Handler and
		// ServiceProxy; replace any client claim before the forwarding RPC.
		r.Header.Set("x-faas-instance", "vm")
		forward.ServeHTTP(w, r)
	}))
	if os.Getenv("GREGALE_NODE_ADMISSION_SURFACES") == "1" {
		childHandler = nodeAdmissionSurfaceHandler(nodeAdmissionClient{vmmdpb.NewVmmdClient(conn)}, port)
	}
	server := httptest.NewServer(childHandler)
	defer server.Close()
	if err := json.NewEncoder(os.Stdout).Encode(server.URL); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type nodeAdmissionProcess struct {
	endpoint string
	stdin    io.WriteCloser
	cmd      *exec.Cmd
	once     sync.Once
}

func (p *nodeAdmissionProcess) stop() { p.once.Do(func() { _ = p.stdin.Close(); _ = p.cmd.Wait() }) }
func startNodeAdmissionProcess(t *testing.T, addr string, port int) *nodeAdmissionProcess {
	t.Helper()
	return startNodeAdmissionProcessMode(t, addr, port, false)
}

func startNodeAdmissionProcessMode(t *testing.T, addr string, port int, surfaces bool) *nodeAdmissionProcess {
	t.Helper()
	c := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNodeAdmissionHTTPGatewayProcess$")
	mode := "0"
	if surfaces {
		mode = "1"
	}
	c.Env = append(os.Environ(), "GREGALE_NODE_ADMISSION_CHILD_ADDR="+addr, "GREGALE_NODE_ADMISSION_CHILD_PORT="+strconv.Itoa(port), "GREGALE_NODE_ADMISSION_SURFACES="+mode)
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	p := &nodeAdmissionProcess{stdin: in, cmd: c}
	t.Cleanup(p.stop)
	if err := json.NewDecoder(out).Decode(&p.endpoint); err != nil {
		t.Fatal(err)
	}
	return p
}

// The guest, namespace selection and VM lifecycle are fixtures. The permit
// owner, both OS gateway processes, gRPC forwarding and reusable bridge binary
// are production code. This does not replace native KVM/restart/leak evidence.
func TestNodeAdmissionAcrossHTTPProcessesReplacementAndUpgrade(t *testing.T) {
	f := newNodeAdmissionProcessFixture(t)
	started, peak, owner, listener, port := f.started, f.peak, f.owner, f.listener, f.port
	first := startNodeAdmissionProcess(t, listener.Addr().String(), port)
	second := startNodeAdmissionProcess(t, listener.Addr().String(), port)
	var cancels []context.CancelFunc
	results := make(chan error, 4)
	for range 4 {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, first.endpoint+"/hold", nil)
		go func() {
			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				if resp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(resp.Body)
					err = fmt.Errorf("hold response %d: %s", resp.StatusCode, body)
				}
				_ = resp.Body.Close()
			}
			results <- err
		}()
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		for range 4 {
			<-results
		}
	}()
	for range 4 {
		select {
		case <-started:
		case err := <-results:
			results <- err
			t.Fatalf("hold failed before guest: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("four requests did not reach guest")
		}
	}
	checkRefusal := func(endpoint string, upgrade bool) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/work", nil)
		if upgrade {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusTooManyRequests || !bytes.Contains(body, []byte(api.CodeConcurrencyThrottled)) || resp.Header.Get("Retry-After") != "1" {
			t.Fatalf("node refusal: %d %s", resp.StatusCode, body)
		}
	}
	for range 4 {
		checkRefusal(second.endpoint, false)
	}
	checkRefusal(second.endpoint, true) // refused before any Upgrade bridge/hijack
	second.stop()
	replacement := startNodeAdmissionProcess(t, listener.Addr().String(), port)
	checkRefusal(replacement.endpoint, false)
	state, _ := owner.HTTPAdmissionStatus("vm")
	if state.Inflight != 4 || peak.Load() != 4 {
		t.Fatalf("fleet cap escaped: %+v, peak %d", state, peak.Load())
	}
	for _, cancel := range cancels {
		cancel()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, _ = owner.HTTPAdmissionStatus("vm")
		if state.Inflight == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridges did not release after cleanup: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := http.Get(replacement.endpoint + "/work")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "guest served" {
		t.Fatalf("post-cleanup response: %s %v", body, err)
	}
	t.Logf("two HTTP processes and replacement preserved node cap %d; guest peak=%d", state.Limit, peak.Load())
}

// Namespace selection and VM startup remain fixtures; both forwarding RPCs,
// trusted admission, completion tracking and the reusable bridge are real.
type nodeAdmissionProcessFixture struct {
	owner                      *fcvm.Manager
	listener                   net.Listener
	port                       int
	started                    chan struct{}
	peak, guestCalls, rpcCalls *atomic.Int32
	rpcInstances               *sync.Map
}

func newNodeAdmissionProcessFixture(t *testing.T) nodeAdmissionProcessFixture {
	t.Helper()
	return newNodeAdmissionProcessFixtureWith(t, []nodeAdmissionFixtureInstance{
		{ID: "vm", Deployment: "dep", Plan: api.PlanFree},
		{ID: "untrusted", Deployment: "dep-untrusted"},
	}, false, nil)
}

type nodeAdmissionFixtureInstance struct {
	ID, Deployment string
	Plan           api.Plan
}

func newNodeAdmissionProcessFixtureWith(t *testing.T, instances []nodeAdmissionFixtureInstance, unix bool, guestHook func(http.ResponseWriter, *http.Request) bool) nodeAdmissionProcessFixture {
	t.Helper()
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v2")
	t.Setenv(streamBridgePersistentEnv, "1")
	shortDir, err := os.MkdirTemp("/tmp", "gregale-node-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
	bridgeBin := filepath.Join(shortDir, "bridge")
	build := exec.CommandContext(t.Context(), "go", "build", "-p=1", "-ldflags=-s -w", "-o", bridgeBin, "../../cmd/vmmd-stream-bridge")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bridge: %v: %s", err, out)
	}
	t.Setenv(streamBridgePathEnv, bridgeBin)
	started := make(chan struct{}, 16)
	var active, peak, calls atomic.Int32
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		calls.Add(1)
		if guestHook != nil && guestHook(w, r) {
			return
		}
		if r.URL.Path == "/hold" || r.URL.Path == "/body-hold" {
			if r.URL.Path == "/body-hold" {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
			}
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "guest served")
	}))
	t.Cleanup(guest.Close)
	_, guestPort, _ := net.SplitHostPort(strings.TrimPrefix(guest.URL, "http://"))
	port, _ := strconv.Atoi(guestPort)
	owner := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	// Initialize the routing fixture before serving; subsequent admission uses
	// the Manager's immutable trusted instance plan and generation.
	for _, spec := range instances {
		owner.RegisterInstanceForTest(spec.ID, spec.Deployment)
		inst := owner.LiveInstances()[spec.ID]
		inst.Plan = spec.Plan
		inst.Lease = fcvm.Lease{Instance: spec.ID, Netns: "fc-" + spec.ID, Plan: spec.Plan}
	}
	s := New(nodeAdmissionProcessVMM{owner: owner}, nil, "test", nil)
	var sockets sync.Map
	s.streamBridges.spawn = func(ctx context.Context, bin, _, socket, _ string, port uint32, deadline string, env []string) (*exec.Cmd, *bytes.Buffer, error) {
		localSocket := filepath.Join(shortDir, filepath.Base(socket))
		sockets.Store(socket, localSocket)
		cmd := exec.CommandContext(ctx, bin, localSocket, "127.0.0.1", strconv.FormatUint(uint64(port), 10), deadline)
		cmd.Env = append(os.Environ(), env...)
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		return cmd, stderr, cmd.Start()
	}
	s.streamBridges.waitSocket = func(socket string, timeout time.Duration) error {
		local, _ := sockets.Load(socket)
		return waitForUnixSock(local.(string), timeout)
	}
	s.streamBridges.newTransport = func(socket string) *http2.Transport {
		local, _ := sockets.Load(socket)
		return &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", local.(string))
		}}
	}
	network, address := "tcp", "127.0.0.1:0"
	if unix {
		network, address = "unix", filepath.Join(shortDir, "vmmd.sock")
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	rpcCalls := new(atomic.Int32)
	rpcInstances := new(sync.Map)
	rpc := grpc.NewServer(grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if strings.HasSuffix(info.FullMethod, "/ForwardHTTPStream") || strings.HasSuffix(info.FullMethod, "/ForwardRawStream") {
			rpcCalls.Add(1)
		}
		return handler(srv, nodeAdmissionObservedStream{ServerStream: stream, instances: rpcInstances})
	}))
	vmmdpb.RegisterVmmdServer(rpc, s)
	go func() { _ = rpc.Serve(listener) }()
	t.Cleanup(rpc.Stop)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return nodeAdmissionProcessFixture{owner: owner, listener: listener, port: port,
		started: started, peak: &peak, guestCalls: &calls, rpcCalls: rpcCalls, rpcInstances: rpcInstances}
}

type nodeAdmissionObservedStream struct {
	grpc.ServerStream
	instances *sync.Map
}

func (s nodeAdmissionObservedStream) RecvMsg(message any) error {
	err := s.ServerStream.RecvMsg(message)
	if request, ok := message.(*vmmdpb.ForwardHTTPStreamRequest); err == nil && ok && request.GetInit() != nil {
		value, _ := s.instances.LoadOrStore(request.GetInit().GetInstance(), new(atomic.Int32))
		value.(*atomic.Int32).Add(1)
	}
	return err
}
