//go:build unix

package scheddgrpc_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc"
)

// admitOnlySchedd admits every wake and counts which generation served it.
type admitOnlySchedd struct {
	scheddpb.UnimplementedScheddServer
	served *atomic.Int64
}

func (s admitOnlySchedd) AdmitInstance(context.Context, *scheddpb.AdmitInstanceRequest) (*scheddpb.AdmitInstanceResponse, error) {
	time.Sleep(2 * time.Millisecond) // keep wakes in flight across GracefulStop
	s.served.Add(1)
	return &scheddpb.AdmitInstanceResponse{InstanceId: "i-1", NodeId: "n-1", WakeId: "w-1"}, nil
}

// TestWakesRideThroughScheddRestart restarts schedd the way a rollout does,
// without socket activation (a compute node's TCP listener): the old process
// stops gracefully and unlinks its socket, and the replacement binds a second
// later. Wakes issued meanwhile wait for the new process instead of failing
// with Unavailable (docs/adr/20261018072701-zero-downtime-daemon-restarts.md).
func TestWakesRideThroughScheddRestart(t *testing.T) {
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "schedd.sock")

	var served [2]atomic.Int64
	start := func(generation int) *grpc.Server {
		lis, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		srv := grpc.NewServer()
		scheddpb.RegisterScheddServer(srv, admitOnlySchedd{served: &served[generation]})
		go func() { _ = srv.Serve(lis) }()
		return srv
	}
	old := start(0)

	client, err := scheddgrpc.DialContext(context.Background(), "unix://"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var (
		failures atomic.Int64
		firstErr atomic.Value
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				// The gateway's wake hold bounds every wake.
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, _, _, _, _, _, _, err := client.AdmitInstance(ctx, "app-1", "", "", "gateway")
				cancel()
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprint(err))
				}
			}
		})
	}
	waitUntil(t, func() bool { return served[0].Load() > 100 })

	old.GracefulStop()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("test premise: the old schedd should have unlinked its socket")
	}
	time.Sleep(1500 * time.Millisecond) // schedd start-up
	replacement := start(1)
	t.Cleanup(replacement.Stop)
	waitUntil(t, func() bool { return served[1].Load() > 100 })
	close(stop)
	wg.Wait()

	if n := failures.Load(); n != 0 {
		t.Fatalf("%d wakes failed across the schedd restart; first: %v", n, firstErr.Load())
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
