//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// This adapter owns no VM, DB authority or network effects. The full-daemon
// fixture owns the real Firecracker attempt and its paid credits. It exposes
// the same live identity to the production RPC handler so each live rollback
// mode can be tested without killing/re-funding that attempt between cases.
type financialRemoteNativeVMM struct {
	vmmdgrpc.VmmdAPI
	record financialSystemRecord
	target netns.PublicForwardTarget
}

func (v financialRemoteNativeVMM) NetnsFor(instance string) (string, bool) {
	return v.record.Lease.Netns, instance == v.record.Lease.Instance
}
func (v financialRemoteNativeVMM) InstancePID(instance string) (int, bool) {
	return v.record.PID, instance == v.record.Lease.Instance
}
func (v financialRemoteNativeVMM) PublicForwardTarget(instance string) (*netns.PublicForwardTarget, error) {
	if instance != v.record.Lease.Instance {
		return nil, fmt.Errorf("unknown original attempt")
	}
	copy := v.target
	return &copy, nil
}

func financialRemoteVariantClient(t *testing.T, h *e2etest.Harness, record financialSystemRecord) vmmdpb.VmmdClient {
	t.Helper()
	iface, err := net.InterfaceByName(record.Lease.VethHost)
	if err != nil {
		t.Fatal(err)
	}
	if record.PID <= 0 || !record.Authorized || record.ResourcesRemoved {
		t.Fatal("remote fixture has no physical funded attempt")
	}
	t.Setenv("FAAS_VMMD_STREAM_BRIDGE_PATH", filepath.Join(h.BinDir, "vmmd-stream-bridge"))
	vmm := financialRemoteNativeVMM{record: record, target: netns.PublicForwardTarget{HostIP: record.Lease.HostIP, BridgeIP: netns.DefaultHostBridgeIP, Veth: iface.Name, IfIndex: iface.Index, AppPort: netns.AppPort}}
	service := vmmdgrpc.New(vmm, nil, "financial-remote-native", nil)
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, service)
	listener := bufconn.Listen(1 << 20)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///financial-native", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		server.Stop()
		_ = service.Close(t.Context())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		if err := service.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(err)
		}
	})
	return vmmdpb.NewVmmdClient(connection)
}

func financialRemoteDaemonClient(t *testing.T, h *e2etest.Harness) vmmdpb.VmmdClient {
	t.Helper()
	connection, err := grpc.NewClient("unix://"+h.VMMDSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return vmmdpb.NewVmmdClient(connection)
}

type financialRemoteResponse struct {
	body   []byte
	chunks int
	err    error
}

func financialRemoteRequest(ctx context.Context, client vmmdpb.VmmdClient, instance string) financialRemoteResponse {
	stream, err := client.ForwardHTTPStream(ctx)
	if err != nil {
		return financialRemoteResponse{err: err}
	}
	err = stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: instance, Method: http.MethodGet, RequestUri: "/", Port: netns.AppPort, Stream: true, ContentLengthKnown: true}}})
	if err != nil {
		return financialRemoteResponse{err: err}
	}
	if err := stream.CloseSend(); err != nil {
		return financialRemoteResponse{err: err}
	}
	var result financialRemoteResponse
	status := 0
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if status != http.StatusOK {
				result.err = fmt.Errorf("remote HTTP status %d", status)
			}
			return result
		}
		if err != nil {
			result.err = err
			return result
		}
		if init := frame.GetInit(); init != nil {
			status = int(init.Status)
		}
		if body := frame.GetBodyChunk(); len(body) > 0 {
			result.chunks++
			result.body = append(result.body, body...)
		}
	}
}

func financialRemoteVariants(t *testing.T, h *e2etest.Harness, record financialSystemRecord, body string, credit func(*testing.T) financial.ComputeLease) {
	t.Helper()
	check := func(t *testing.T, version, persistent string) {
		t.Helper()
		t.Setenv("FAAS_STREAM_BRIDGE_VERSION", version)
		t.Setenv("FAAS_STREAM_BRIDGE_PERSISTENT", persistent)
		client := financialRemoteVariantClient(t, h, record)
		before, err := netns.ReadVethRXBytes(record.Lease.VethHost)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		results := make(chan financialRemoteResponse, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				results <- financialRemoteRequest(ctx, client, record.Lease.Instance)
			}()
		}
		wg.Wait()
		close(results)
		payload := 0
		for result := range results {
			if result.err != nil || !bytes.Equal(result.body, []byte(body)) || result.chunks < 2 {
				t.Fatalf("remote streaming body=%d chunks=%d: %v", len(result.body), result.chunks, result.err)
			}
			payload += len(result.body)
		}
		after, err := netns.ReadVethRXBytes(record.Lease.VethHost)
		lease := credit(t)
		if err != nil || after < before+uint64(payload) || after-before >= uint64(2*payload) || after > uint64(lease.EgressGranted) {
			t.Fatalf("remote response bypassed canonical funding: before=%d after=%d payload=%d granted=%d err=%v", before, after, payload, lease.EgressGranted, err)
		}
		t.Logf("financial-remote-http: version=%s persistent=%s payload=%d interface_delta=%d cumulative=%d funded=%d", version, persistent, payload, after-before, after, lease.EgressGranted)
	}
	t.Run("legacy_v1", func(t *testing.T) { check(t, "v1", "0") })
	t.Run("per_request_v2", func(t *testing.T) { check(t, "v2", "0") })
	t.Run("persistent_v2", func(t *testing.T) { check(t, "v2", "1") })
}

// DB is already disconnected. Actual VMMD cannot buy/reset another quota;
// the kernel must stop the response independently of meterd and the gateway.
func financialRemoteExhaustion(t *testing.T, h *e2etest.Harness, record financialSystemRecord, lease financial.ComputeLease, body string) {
	t.Helper()
	if time.Until(lease.RunUntil) < 20*time.Second {
		t.Fatal("insufficient retained native window for independent quota measurement")
	}
	client := financialRemoteDaemonClient(t, h)
	before, err := netns.ReadVethRXBytes(record.Lease.VethHost)
	if err != nil {
		t.Fatal(err)
	}
	var delivered atomicRemoteBytes
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				response := financialRemoteRequest(ctx, client, record.Lease.Instance)
				cancel()
				if len(response.body) > len(body) {
					delivered.invalid()
					return
				}
				delivered.add(uint64(len(response.body)))
			}
		}()
	}
	wg.Wait()
	after, err := netns.ReadVethRXBytes(record.Lease.VethHost)
	payload, invalid := delivered.value()
	if err != nil || invalid || payload == 0 || after < before+payload || after > uint64(lease.EgressGranted) {
		t.Fatalf("remote exhaustion bytes: before=%d after=%d payload=%d granted=%d err=%v", before, after, payload, lease.EgressGranted, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response := financialRemoteRequest(ctx, client, record.Lease.Instance)
	final, err := netns.ReadVethRXBytes(record.Lease.VethHost)
	if err != nil || len(response.body) != 0 || final != after {
		t.Fatalf("exhausted response reopened credit: %d -> %d body=%d err=%v", after, final, len(response.body), err)
	}
	if financialSystemPhysicalAbsent(record) {
		t.Fatal("quota test passed only because the VM was retired")
	}
	// Authorized private replies retain their namespace-local path, and must
	// neither reset the public quota nor add a body to its canonical counter.
	private, privateCancel := context.WithTimeout(wire.WithPrivateForward(t.Context()), 5*time.Second)
	defer privateCancel()
	response = financialRemoteRequest(wire.ForwardContext(private), client, record.Lease.Instance)
	final, err = netns.ReadVethRXBytes(record.Lease.VethHost)
	if err != nil || response.err != nil || string(response.body) != body || final != after {
		t.Fatalf("private/public isolation: body=%d interface=%d -> %d err=%v/%v", len(response.body), after, final, response.err, err)
	}
	t.Logf("financial-remote-exhaustion: payload=%d interface_delta=%d cumulative=%d funded=%d private_payload=%d stable_after=%d private_counter=%d", payload, after-before, after, lease.EgressGranted, len(response.body), after, final)
}

type atomicRemoteBytes struct {
	mu    sync.Mutex
	bytes uint64
	bad   bool
}

func (b *atomicRemoteBytes) add(n uint64) { b.mu.Lock(); defer b.mu.Unlock(); b.bytes += n }
func (b *atomicRemoteBytes) invalid()     { b.mu.Lock(); defer b.mu.Unlock(); b.bad = true }
func (b *atomicRemoteBytes) value() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytes, b.bad
}
