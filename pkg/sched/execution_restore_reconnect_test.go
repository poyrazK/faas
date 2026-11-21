// adr: 20261114010000 — reconnect before sending a fresh restore; never replay an uncertain request.
package sched_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type restoreReconnectServer struct {
	vmmdpb.UnimplementedVmmdServer
	calls     atomic.Int32
	uncertain bool
}

func (s *restoreReconnectServer) RestoreExecution(_ context.Context, r *vmmdpb.RestoreExecutionRequest) (*vmmdpb.RestoreExecutionResponse, error) {
	s.calls.Add(1)
	if s.uncertain {
		return nil, status.Error(codes.Unavailable, "injected uncertainty after admission")
	}
	return &vmmdpb.RestoreExecutionResponse{Instance: r.Instance}, nil
}

func (s *restoreReconnectServer) Destroy(_ context.Context, r *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	s.calls.Add(1)
	if s.uncertain {
		return nil, status.Error(codes.Unavailable, "injected stop uncertainty")
	}
	return &vmmdpb.DestroyResponse{Instance: r.Instance}, nil
}

func TestVMMClientExecutionRestoreWaitsForReconnectWithoutReplaying(t *testing.T) {
	runExecutionReconnect(t, false)
}
func TestVMMClientDestroyWaitsForReconnectWithoutReplaying(t *testing.T) {
	runExecutionReconnect(t, true)
}
func runExecutionReconnect(t *testing.T, destroy bool) {
	t.Helper()
	for _, mode := range []string{"reconnect", "deadline", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			server := &restoreReconnectServer{uncertain: mode == "uncertain"}
			listener := bufconn.Listen(1 << 20)
			rpc := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(rpc, server)
			go func() { _ = rpc.Serve(listener) }()
			t.Cleanup(func() { rpc.Stop(); _ = listener.Close() })
			var ready atomic.Bool
			ready.Store(mode == "uncertain")
			attempted := make(chan struct{}, 1)
			conn, err := grpc.NewClient("passthrough:///restore-reconnect", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { //nolint:forbidigo // Isolated, in-memory transport failure fixture.
				if !ready.Load() {
					select {
					case attempted <- struct{}{}:
					default:
					}
					return nil, errors.New("daemon restarting")
				}
				return listener.Dial()
			}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			client := sched.NewVMMClient(conn)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 250*time.Millisecond)
				defer stop()
			}
			done := make(chan error, 1)
			go func() {
				if destroy {
					done <- client.Destroy(ctx, "fresh-restore")
					return
				}
				_, err := client.RestoreExecution(ctx, sched.ExecutionRestoreRequest{ID: "fresh-restore", AccountID: "platform-test", Plan: api.PlanScale, Runtime: api.ExecutionRuntimeNode24, KernelKey: "kernel/test", BaseKey: "base/test", LayerKey: "layers/test", VcpuCount: 1, MemSizeMiB: 256, CPUMillicores: 1000})
				done <- err
			}()
			if mode == "uncertain" {
				if err := <-done; err == nil {
					t.Fatal("uncertain restore succeeded")
				}
				if server.calls.Load() != 1 {
					t.Fatal("uncertain restore was replayed")
				}
				return
			}
			select {
			case <-attempted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for state := conn.GetState(); state != connectivity.TransientFailure; state = conn.GetState() {
				if !conn.WaitForStateChange(ctx, state) {
					t.Fatal(ctx.Err())
				}
			}
			if mode == "deadline" {
				if err := <-done; err == nil {
					t.Fatal("unavailable restore ignored its deadline")
				}
				if server.calls.Load() != 0 {
					t.Fatal("expired restore reached server")
				}
				return
			}
			select {
			case err := <-done:
				t.Fatalf("restore failed before channel reconnected: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			ready.Store(true)
			conn.ResetConnectBackoff()
			conn.Connect()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if server.calls.Load() != 1 {
				t.Fatal("fresh restore did not execute exactly once")
			}
		})
	}
}
