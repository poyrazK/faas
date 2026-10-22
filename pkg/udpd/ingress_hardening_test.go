package udpd

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// One port that cannot be bound must not fail gatewayd-public's startup. The
// supervisor reports it, becomes ready, and binds it on a later refresh.
func TestSupervisorRetriesAFailedBind(t *testing.T) {
	source := &udpIntentSource{}
	source.set(udpIntent("one", "app", "account", api.UDPListenerPublicPortMin))
	bindErr := errors.New("port already bound")
	var attempts atomic.Int32
	bound := make(chan struct{})
	ready := make(chan struct{})
	reported := make(chan error, 4)
	supervisor := &Supervisor{
		Source: source, RefreshInterval: 20 * time.Millisecond,
		Forwarder: echoForwarder{},
		ResolveTarget: func(_ context.Context, r Route) (gateway.Target, error) {
			return gateway.Target{AppID: r.AppID, InstanceID: "guest", NodeID: "node"}, nil
		},
		Listen: func(_ string, _ *net.UDPAddr) (*net.UDPConn, error) {
			if attempts.Add(1) == 1 {
				return nil, bindErr
			}
			defer close(bound)
			return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		},
		OnReady: func() { close(ready) },
		OnError: func(err error) { reported <- err },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	for _, step := range []struct {
		name string
		ch   <-chan struct{}
	}{{"ready", ready}, {"rebind", bound}} {
		select {
		case <-step.ch:
		case err := <-done:
			t.Fatalf("Serve returned before %s: %v", step.name, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("supervisor did not reach %s", step.name)
		}
	}
	if err := <-reported; !errors.Is(err, bindErr) {
		t.Fatalf("reported error = %v, want the bind failure", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve after cancellation: %v", err)
	}
}
