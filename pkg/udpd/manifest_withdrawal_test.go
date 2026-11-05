// adr: 20261104143000
package udpd_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/udpd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestUDPManifestWithdrawalCancelsExistingStream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []api.WorkloadPort
	}{
		{"removed", nil},
		{"renamed", []api.WorkloadPort{{Name: "new-name", Port: 5353, Protocol: api.WorkloadPortUDP}}},
		{"changed-port", []api.WorkloadPort{{Name: "echo", Port: 5354, Protocol: api.WorkloadPortUDP}}},
		{"changed-protocol", []api.WorkloadPort{{Name: "echo", Port: 5353, Protocol: api.WorkloadPortTCP}}},
		{"internal", []api.WorkloadPort{{Name: "echo", Port: 5353, Protocol: api.WorkloadPortUDP, Internal: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "withdrawal@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 5353, Protocol: api.WorkloadPortUDP}}}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "withdrawal", Status: state.AppActive, RAMMB: 256, Manifest: manifest})
			if err != nil {
				t.Fatal(err)
			}
			intent, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: 40100, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{ID: "deployment", AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateInstance(ctx, app.ID, "deployment", string(state.StateRunning), 256, "node", "wake"); err != nil {
				t.Fatal(err)
			}
			listener := bufconn.Listen(1 << 20)
			rpc := grpc.NewServer()
			guest := &udpGRPCGuest{started: make(chan *vmmdpb.ForwardUDPRequestInit, 4), stopped: make(chan struct{}, 4)}
			vmmdpb.RegisterVmmdServer(rpc, guest)
			go func() { _ = rpc.Serve(listener) }()
			defer rpc.Stop()
			defer listener.Close()
			connection, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			bound := make(chan *net.UDPConn, 4)
			resolver := &udpd.StoreTargetResolver{Store: store}
			supervisor := &udpd.Supervisor{Source: store, ResolveTarget: resolver.ResolveTarget, RefreshInterval: 5 * time.Millisecond,
				AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
				Forwarder:      gateway.UDPForwarder{Nodes: udpGRPCNodes{vmmdpb.NewVmmdClient(connection)}, IdleTimeout: time.Minute},
				Listen: func(_ string, _ *net.UDPAddr) (*net.UDPConn, error) {
					socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
					if err == nil {
						bound <- socket
					}
					return socket, err
				}}
			done := make(chan error, 1)
			go func() { done <- supervisor.Serve(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(2 * time.Second):
					t.Error("supervisor shutdown hung")
				}
			}()
			nextClient := func() *net.UDPConn {
				t.Helper()
				var socket *net.UDPConn
				select {
				case socket = <-bound:
				case <-time.After(2 * time.Second):
					t.Fatal("UDP listener not bound")
				}
				client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				return client
			}
			exchange := func(client *net.UDPConn) {
				t.Helper()
				if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := client.Write([]byte("echo")); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 128)
				n, err := client.Read(buf)
				if err != nil || string(buf[:n]) != "echo" {
					t.Fatalf("UDP exchange=%q err=%v", buf[:n], err)
				}
			}
			client := nextClient()
			exchange(client)
			withdrawn := state.AppManifest{Ports: tc.ports}
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &withdrawn}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-guest.stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("withdrawn declaration kept its admitted UDP stream alive")
			}
			if err := client.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write([]byte("after withdrawal")); err == nil {
				if _, err := client.Read(make([]byte, 128)); err == nil {
					t.Fatal("withdrawn listener still forwarded datagrams")
				}
			}
			retained, err := store.UDPListenerByID(ctx, intent.ID)
			if err != nil || !retained.Enabled {
				t.Fatalf("manifest withdrawal changed durable reservation: %+v err=%v", retained, err)
			}
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			exchange(nextClient())
		})
	}
}
