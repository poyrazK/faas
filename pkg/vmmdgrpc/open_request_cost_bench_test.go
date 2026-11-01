//go:build linux

// adr: 20261101215228

package vmmdgrpc

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// The open-request cost bench (ADR-20261101215228) measures what one request
// held open costs the processes between gatewayd-internal and the guest:
// vmmd's ForwardHTTPStream server, the instance's persistent stream bridge
// (the real cmd/vmmd-stream-bridge binary, with bridge_stream_headroom on as
// in production) and the gRPC client that stands in for gatewayd-internal.
// The guest never answers, so every request stays open. vmmd and the guest
// run as child processes of the driver, so each process's RSS is its own.
// It is opt-in and runs on Linux only:
//
//	FAAS_OPEN_REQUEST_COST_STEPS         open-request counts to sample, e.g. 1,100,500,1000,2000
//	FAAS_OPEN_REQUEST_COST_BRIDGE        a built cmd/vmmd-stream-bridge
//	FAAS_OPEN_REQUEST_COST_PER_INSTANCE  open requests per instance (default 100)
//
// A bridge tracks at most api.TrafficBridgeCompletionMaxEntries open
// exchanges, so requests are spread over instances, one bridge each.
//
// The bridge's socket lives under /var/run/faas/stream, so run it as root.
const (
	openCostStepsEnv   = "FAAS_OPEN_REQUEST_COST_STEPS"
	openCostBridgeEnv  = "FAAS_OPEN_REQUEST_COST_BRIDGE"
	openCostRoleEnv    = "FAAS_OPEN_REQUEST_COST_ROLE"
	openCostSocketEnv  = "FAAS_OPEN_REQUEST_COST_SOCKET"
	openCostPerInstEnv = "FAAS_OPEN_REQUEST_COST_PER_INSTANCE"
	openCostInstance   = "open-cost"
	openCostOpenPath   = "/__open"
)

// openCostVMM supplies the two VmmdAPI methods the forward path calls for a
// live instance.
type openCostVMM struct {
	VmmdAPI
}

func (openCostVMM) AcquireHTTPForward(ctx context.Context, _ string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

func (openCostVMM) NetnsFor(instance string) (string, bool) {
	return "fc-" + instance, instance != ""
}

// TestOpenRequestCostHelper is the child-process side of the bench. It does
// nothing unless the driver starts it with a role.
func TestOpenRequestCostHelper(t *testing.T) {
	switch os.Getenv(openCostRoleEnv) {
	case "guest":
		runOpenCostGuest(t)
	case "vmmd":
		runOpenCostVmmd(t)
	default:
		t.Skip("child process of TestOpenRequestCostBench")
	}
}

// runOpenCostGuest serves requests that never answer, and reports how many
// are open on openCostOpenPath. It exits when the driver closes stdin.
func runOpenCostGuest(t *testing.T) {
	var open atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == openCostOpenPath {
			_, _ = fmt.Fprint(w, open.Load())
			return
		}
		open.Add(1)
		defer open.Add(-1)
		<-r.Context().Done()
	})}
	go func() { _ = srv.Serve(ln) }()
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = srv.Close()
}

// runOpenCostVmmd serves vmmd's gRPC API on a unix socket. Bridges run in
// the host network namespace and dial the guest on loopback.
func runOpenCostVmmd(t *testing.T) {
	t.Setenv(streamBridgePathEnv, os.Getenv(openCostBridgeEnv))
	streamBridgeSpawn = func(ctx context.Context, bridgePath, _ string, id bridgeIdentity, sockPath, _ string, port uint32, deadline string, env []string) (*exec.Cmd, *bytes.Buffer, error) {
		return streamBridgeSpawnReal(ctx, bridgePath, "", id, sockPath, "127.0.0.1", port, deadline, env)
	}
	server := New(openCostVMM{}, nil, "open-cost", nil).WithBridgeStreamHeadroom(true)
	g := grpc.NewServer()
	server.Register(g)
	ln, err := net.Listen("unix", os.Getenv(openCostSocketEnv))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = g.Serve(ln) }()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	g.Stop()
	_ = server.Close(context.Background())
}

func TestOpenRequestCostBench(t *testing.T) {
	steps := openCostSteps(t)
	perInstance := 100
	if v := os.Getenv(openCostPerInstEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("%s=%q: want a positive count", openCostPerInstEnv, v)
		}
		perInstance = n
	}
	bridge := os.Getenv(openCostBridgeEnv)
	if bridge == "" {
		t.Fatalf("%s must name a built cmd/vmmd-stream-bridge", openCostBridgeEnv)
	}
	if err := os.MkdirAll("/var/run/faas/stream", 0o770); err != nil {
		t.Fatalf("bridge socket dir (run as root): %v", err)
	}
	dir, err := os.MkdirTemp("", "opencost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "vmmd.sock")

	guest, guestOut := startOpenCostChild(t, "guest", nil)
	port, err := strconv.Atoi(guestOut)
	if err != nil {
		t.Fatalf("guest port %q: %v", guestOut, err)
	}
	vmmd, _ := startOpenCostChild(t, "vmmd", []string{openCostSocketEnv + "=" + sock, openCostBridgeEnv + "=" + bridge})

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := vmmdpb.NewVmmdClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var rows []openCostRow
	opened := 0
	for _, n := range steps {
		for ; opened < n; opened++ {
			go holdOpenRequest(ctx, client, port, fmt.Sprintf("%s-%d", openCostInstance, opened/perInstance))
		}
		if err := waitGuestOpen(port, n, 2*time.Minute); err != nil {
			t.Fatalf("waiting for %d open requests: %v; request errors: %v", n, err, openCostErrors.summary())
		}
		time.Sleep(3 * time.Second)
		rows = append(rows, sampleOpenCost(t, n, vmmd.Process.Pid, guest.Process.Pid, bridge))
	}
	logOpenCost(t, rows, perInstance)
}

func openCostSteps(t *testing.T) []int {
	t.Helper()
	raw := os.Getenv(openCostStepsEnv)
	if raw == "" {
		t.Skipf("set %s (e.g. 1,100,500,1000,2000) and %s to run the open-request cost bench", openCostStepsEnv, openCostBridgeEnv)
	}
	var steps []int
	for _, f := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || (len(steps) > 0 && n <= steps[len(steps)-1]) {
			t.Fatalf("%s=%q: want increasing positive counts", openCostStepsEnv, raw)
		}
		steps = append(steps, n)
	}
	return steps
}

// startOpenCostChild re-runs this test binary as one helper role and returns
// it with the first line it prints.
func startOpenCostChild(t *testing.T, role string, env []string) (*exec.Cmd, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestOpenRequestCostHelper$", "-test.count=1")
	cmd.Env = append(append(os.Environ(), openCostRoleEnv+"="+role), env...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("%s helper: %v", role, err)
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	return cmd, strings.TrimSpace(line)
}

// openCostErrors counts how open requests ended, so a failed step says why.
var openCostErrors openCostErrorCounts

type openCostErrorCounts struct {
	mu     sync.Mutex
	counts map[string]int
	order  []string
}

func (c *openCostErrorCounts) add(stage string, err error) {
	key := stage + ": " + err.Error()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	if c.counts[key] == 0 {
		c.order = append(c.order, key)
	}
	c.counts[key]++
}

func (c *openCostErrorCounts) summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, k := range c.order {
		fmt.Fprintf(&b, "\n  %d × %s", c.counts[k], k)
	}
	return b.String()
}

// holdOpenRequest opens one ForwardHTTPStream to the guest and waits until
// ctx ends. The deadline header keeps the bridge from giving up on the
// guest's headers first, as the gateway's request budget does.
func holdOpenRequest(ctx context.Context, client vmmdpb.VmmdClient, port int, instance string) {
	stream, err := client.ForwardHTTPStream(ctx)
	if err != nil {
		openCostErrors.add("open", err)
		return
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{
		Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
			Instance:   instance,
			Method:     http.MethodGet,
			RequestUri: "/hold",
			Headers: []*vmmdpb.Header{
				{Name: "Host", Value: "open-cost.test"},
				{Name: api.DeadlineHeader, Value: "600000"},
			},
			Port:   uint32(port), //nolint:gosec // a listener port
			Stream: true,
		}},
	}); err != nil {
		openCostErrors.add("send", err)
		return
	}
	if err := stream.CloseSend(); err != nil {
		openCostErrors.add("close send", err)
		return
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			if ctx.Err() == nil {
				openCostErrors.add("recv", err)
			}
			return
		}
		if init := frame.GetInit(); init != nil && ctx.Err() == nil {
			openCostErrors.add("answered", fmt.Errorf("status %d %s", init.GetStatus(), init.GetError()))
		}
	}
}

func waitGuestOpen(port, want int, limit time.Duration) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, openCostOpenPath)
	deadline := time.Now().Add(limit)
	got := -1
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec,noctx // the bench's own loopback guest
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			got, _ = strconv.Atoi(string(b))
			if got >= want {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("guest reports %d open", got)
}

type openCostProc struct {
	RSSKB, Threads, FDs int
}

type openCostRow struct {
	Open                 int
	Bridges              int
	Vmmd, Bridge, Guest  openCostProc
	ClientHeapStackKB    int
	ClientGoroutines     int
	TCPInUse, TCPMemPage int
}

func sampleOpenCost(t *testing.T, open, vmmdPID, guestPID int, bridge string) openCostRow {
	t.Helper()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	row := openCostRow{
		Open:              open,
		Vmmd:              readOpenCostProc(vmmdPID),
		Guest:             readOpenCostProc(guestPID),
		ClientHeapStackKB: int((ms.HeapInuse + ms.StackInuse) / 1024),
		ClientGoroutines:  runtime.NumGoroutine(),
	}
	for _, pid := range findProcsByExe(bridge) {
		p := readOpenCostProc(pid)
		row.Bridges++
		row.Bridge.RSSKB += p.RSSKB
		row.Bridge.Threads += p.Threads
		row.Bridge.FDs += p.FDs
	}
	row.TCPInUse, row.TCPMemPage = readTCPSockstat()
	return row
}

func readOpenCostProc(pid int) openCostProc {
	var p openCostProc
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return p
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "VmRSS:":
			p.RSSKB, _ = strconv.Atoi(fields[1])
		case "Threads:":
			p.Threads, _ = strconv.Atoi(fields[1])
		}
	}
	if fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		p.FDs = len(fds)
	}
	return p
}

// findProcsByExe returns the pids of processes whose argv[0] is exe.
func findProcsByExe(exe string) []int {
	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if argv0, _, _ := strings.Cut(string(cmdline), "\x00"); argv0 == exe {
			pids = append(pids, pid)
		}
	}
	return pids
}

// readTCPSockstat returns the host's TCP sockets in use and the pages of
// kernel memory they hold.
func readTCPSockstat() (inUse, memPages int) {
	b, err := os.ReadFile("/proc/net/sockstat")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "TCP:" {
			continue
		}
		for i := 1; i+1 < len(fields); i += 2 {
			v, _ := strconv.Atoi(fields[i+1])
			switch fields[i] {
			case "inuse":
				inUse = v
			case "mem":
				memPages = v
			}
		}
	}
	return inUse, memPages
}

func logOpenCost(t *testing.T, rows []openCostRow, perInstance int) {
	t.Helper()
	t.Logf("%6s | %-22s | %-28s | %-16s | %-10s | %-14s", "open", "vmmd rss MB/thr/fds", "bridges n/rss MB/thr/fds", "client MB/gor", "guest MB", "tcp inuse/mem")
	for _, r := range rows {
		t.Logf("%6d | %7.1f %5d %7d | %3d %7.1f %5d %7d | %7.1f %7d | %10.1f | %6d %7d",
			r.Open,
			mb(r.Vmmd.RSSKB), r.Vmmd.Threads, r.Vmmd.FDs,
			r.Bridges, mb(r.Bridge.RSSKB), r.Bridge.Threads, r.Bridge.FDs,
			mb(r.ClientHeapStackKB), r.ClientGoroutines,
			mb(r.Guest.RSSKB),
			r.TCPInUse, r.TCPMemPage)
	}
	if len(rows) < 2 {
		return
	}
	first, last := rows[0], rows[len(rows)-1]
	d := float64(last.Open - first.Open)
	per := func(a, b int) float64 { return float64(b-a) / d }
	t.Logf("per open request, %d → %d: vmmd %.1f KB, client %.1f KB, guest %.1f KB, kernel TCP %.1f KB, vmmd fds %.2f",
		first.Open, last.Open,
		per(first.Vmmd.RSSKB, last.Vmmd.RSSKB),
		per(first.ClientHeapStackKB, last.ClientHeapStackKB),
		per(first.Guest.RSSKB, last.Guest.RSSKB),
		per(first.TCPMemPage, last.TCPMemPage)*float64(os.Getpagesize())/1024,
		per(first.Vmmd.FDs, last.Vmmd.FDs))
	// One bridge: its cost at one open request, and per request up to
	// perInstance.
	for _, r := range rows {
		if r.Open == perInstance && first.Bridges == 1 && r.Bridges == 1 && perInstance > first.Open {
			t.Logf("one bridge: %.1f MB at %d open; %.1f KB and %.2f fds per open request up to %d",
				mb(first.Bridge.RSSKB), first.Open,
				float64(r.Bridge.RSSKB-first.Bridge.RSSKB)/float64(perInstance-first.Open),
				float64(r.Bridge.FDs-first.Bridge.FDs)/float64(perInstance-first.Open), perInstance)
		}
	}
}

func mb(kb int) float64 { return float64(kb) / 1024 }
