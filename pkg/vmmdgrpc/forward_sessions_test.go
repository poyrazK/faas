// adr: 080
package vmmdgrpc

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestForwardSessionsCloseInstanceEndsOnlyThatInstance(t *testing.T) {
	var sessions forwardSessions
	first, releaseFirst := sessions.track(context.Background(), "inst-a")
	second, releaseSecond := sessions.track(context.Background(), "inst-a")
	other, releaseOther := sessions.track(context.Background(), "inst-b")

	if n := sessions.closeInstance("inst-a"); n != 2 {
		t.Fatalf("closeInstance(inst-a) = %d, want 2", n)
	}
	for name, ctx := range map[string]context.Context{"first": first, "second": second} {
		if ctx.Err() == nil || !instanceStopped(ctx) {
			t.Fatalf("%s session err=%v cause=%v, want ended by instance stop", name, ctx.Err(), context.Cause(ctx))
		}
	}
	if other.Err() != nil {
		t.Fatalf("other instance's session ended: %v", other.Err())
	}

	// Releasing after the teardown is the normal stream exit; it must not
	// disturb other instances' sessions.
	releaseFirst()
	releaseSecond()
	if n := sessions.closeInstance("inst-a"); n != 0 {
		t.Fatalf("second closeInstance(inst-a) = %d, want 0", n)
	}
	releaseOther()
	if other.Err() == nil || instanceStopped(other) {
		t.Fatalf("released session err=%v cause=%v, want plain cancellation", other.Err(), context.Cause(other))
	}
	if n := sessions.closeInstance("inst-b"); n != 0 {
		t.Fatalf("closeInstance after release = %d, want 0", n)
	}
}

// TestInstanceTeardownClosesForwardSessions pins H9-129: after `apps delete`
// or `app restart` an upgraded WebSocket stayed half-open because nothing
// ended its bridge when vmmd tore the instance down. Every teardown RPC must
// end the instance's forward sessions.
func TestInstanceTeardownClosesForwardSessions(t *testing.T) {
	cases := map[string]func(*Server, string) error{
		"Destroy": func(s *Server, inst string) error {
			_, err := s.Destroy(context.Background(), &vmmdpb.DestroyRequest{Instance: inst})
			return err
		},
		"StopInstance": func(s *Server, inst string) error {
			_, err := s.StopInstance(context.Background(), &vmmdpb.StopInstanceRequest{Instance: inst})
			return err
		},
		"PauseAndSnapshot": func(s *Server, inst string) error {
			_, err := s.PauseAndSnapshot(context.Background(), &vmmdpb.PauseAndSnapshotRequest{Instance: inst, StorageKey: "snap/mem", VmstatePath: "/var/lib/faas/snap/vmstate"})
			return err
		},
	}
	for name, teardown := range cases {
		t.Run(name, func(t *testing.T) {
			s := New(vmmStubBase{}, nil, "test", nil)
			ws, release := s.forwardSessions.track(context.Background(), "inst-ws")
			defer release()
			survivor, releaseSurvivor := s.forwardSessions.track(context.Background(), "inst-other")
			defer releaseSurvivor()

			if err := teardown(s, "inst-ws"); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !instanceStopped(ws) {
				t.Fatalf("session after %s: err=%v cause=%v, want ended by instance stop", name, ws.Err(), context.Cause(ws))
			}
			if survivor.Err() != nil {
				t.Fatalf("%s ended another instance's session: %v", name, survivor.Err())
			}
		})
	}
}

// TestBridgeFinishEndsSessionOnInstanceStop drives the real teardown shape: a
// bridge process bound to the session context and a client that has gone
// quiet (its Recv never returns). Stopping the instance must kill the bridge
// and end the stream with Unavailable, which gatewayd-internal answers by
// closing the client connection.
func TestBridgeFinishEndsSessionOnInstanceStop(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available")
	}
	finishers := map[string]func(bridgeWaiter, <-chan error, *sync.WaitGroup, context.Context, func() string) error{
		"raw": rawBridgeFinish,
		"tcp": tcpBridgeFinish,
	}
	for name, finish := range finishers {
		t.Run(name, func(t *testing.T) {
			s := New(vmmStubBase{}, nil, "test", nil)
			ctx, release := s.forwardSessions.track(context.Background(), "inst-ws")
			defer release()
			cmd := exec.CommandContext(ctx, sleep, "60")
			if err := cmd.Start(); err != nil {
				t.Fatalf("start bridge stand-in: %v", err)
			}

			quietClient := make(chan struct{})
			defer close(quietClient)
			var bodyWG sync.WaitGroup
			bodyWG.Add(1)
			go func() { defer bodyWG.Done(); <-quietClient }()

			time.AfterFunc(50*time.Millisecond, func() { s.forgetForwarding(context.Background(), "inst-ws") })
			done := make(chan error, 1)
			go func() { done <- finish(cmd, make(chan error, 1), &bodyWG, ctx, func() string { return "" }) }()
			select {
			case err := <-done:
				if status.Code(err) != codes.Unavailable {
					t.Fatalf("finish = %v, want Unavailable", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("session stayed open after its instance stopped")
			}
		})
	}
}
