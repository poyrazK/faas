// adr: 570

package vmmdgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bridgecompletion"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// refusedExchangeTestLimit is the test bridge's open-exchange limit; production
// bridges allow api.TrafficBridgeCompletionMaxEntries.
const refusedExchangeTestLimit = 128

// A bridge refuses the exchange past its open-exchange limit
// open ones. vmmd used to wait for that exchange's completion, get 404 and
// fence the bridge, which ended every open exchange on the instance. Now the
// refused request alone fails as "App busy" and the others finish.
func TestForwardHTTPStreamV2_RefusedExchangeKeepsTheBridge(t *testing.T) {
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	savedSpawn := streamBridgeSpawn
	var starts atomic.Int32
	streamBridgeSpawn = func(ctx context.Context, _, _ string, _ bridgeIdentity, _, _ string, _ uint32, _ string, _ []string) (*exec.Cmd, *bytes.Buffer, error) {
		cmd := exec.CommandContext(ctx, "sleep", "60")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		starts.Add(1)
		return cmd, &bytes.Buffer{}, nil
	}
	t.Cleanup(func() { streamBridgeSpawn = savedSpawn })

	server := New(forwardV2IntegrationVMM{}, nil, "test", nil)
	server.streamBridges.waitSocket = func(string, time.Duration) error { return nil }
	server.streamBridges.reapInterval = time.Hour

	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var open atomic.Int32
	bridge := bridgecompletion.WrapLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		open.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
	}), refusedExchangeTestLimit)
	var connsMu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		connsMu.Lock()
		defer connsMu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	// Each dial is a new in-memory connection to the same bridge handler, as
	// vmmd opens several to a real bridge.
	server.streamBridges.newTransport = func(string, bridgeIdentity) *http2.Transport {
		return &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				connsMu.Lock()
				conns = append(conns, clientConn, serverConn)
				connsMu.Unlock()
				go (&http2.Server{}).ServeConn(serverConn, &http2.ServeConnOpts{Handler: bridge})
				return clientConn, nil
			},
		}
	}

	grpcServer := grpc.NewServer()
	server.Register(grpcServer)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(func() { grpcServer.Stop(); _ = lis.Close() })
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := vmmdpb.NewVmmdClient(conn)

	const instance = "instance-refused"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		status int32
		err    error
	}
	held := make(chan result, refusedExchangeTestLimit)
	for range refusedExchangeTestLimit {
		go func() {
			status, _, err := forwardV2LifecycleRoundTrip(ctx, client, instance, "hold")
			held <- result{status, err}
		}()
	}
	for open.Load() < refusedExchangeTestLimit {
		if ctx.Err() != nil {
			t.Fatalf("only %d of %d exchanges reached the bridge", open.Load(), refusedExchangeTestLimit)
		}
		time.Sleep(time.Millisecond)
	}

	_, _, err = forwardV2LifecycleRoundTrip(ctx, client, instance, "refused")
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil || p.Code != api.CodeConcurrencyThrottled {
		t.Fatalf("exchange past the bridge's limit = %v, want %s", err, api.CodeConcurrencyThrottled)
	}
	server.streamBridges.mu.Lock()
	entry := server.streamBridges.entries[instance]
	alive := entry != nil && !entry.closed
	server.streamBridges.mu.Unlock()
	if !alive {
		t.Fatal("a refused exchange fenced the bridge")
	}

	releaseOnce.Do(func() { close(release) })
	for range refusedExchangeTestLimit {
		r := <-held
		if r.err != nil || r.status != http.StatusOK {
			t.Fatalf("open exchange after a refusal = (%d, %v), want 200", r.status, r.err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("bridge starts = %d, want the one bridge kept", got)
	}
}
