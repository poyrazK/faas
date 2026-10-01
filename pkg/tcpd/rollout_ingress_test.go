package tcpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type rolloutIngressAdmitter struct {
	store *state.MemStore
	calls atomic.Int32
}

func (a *rolloutIngressAdmitter) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	if deployment == "" || scope != "" || trigger != "gateway" {
		return "", "", "", "", 0, false, 0, errors.New("admission did not pin deployment")
	}
	a.calls.Add(1)
	wake := uuid.NewString()
	instance, err := a.store.CreateInstance(ctx, app, deployment, string(state.StateRunning), 256, "node", wake)
	return instance.ID, "node", deployment, wake, 0, false, 9000, err
}

// Compose sockets, durable traffic changes, target selection and cold admission.
// Guest execution is substituted; native rollout qualification remains separate.
func TestTCPIngressTrafficSwitchAndRollbackPinsEstablishedSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tcp-rollout-ingress@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tcp-rollout-ingress", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range []state.Deployment{
		{ID: "stable", AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100},
		{ID: "candidate", AppID: app.ID, Status: state.DeployLive, TrafficPercentExplicit: true},
	} {
		if _, err := store.CreateDeployment(ctx, deployment); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateInstance(ctx, app.ID, "stable", string(state.StateRunning), 256, "node", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	const publicPort = 40125
	if _, err := store.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: publicPort, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	admit := &rolloutIngressAdmitter{store: store}
	server := &Server{Listener: &aliasedTCPListener{Listener: base, port: publicPort}, Routes: ListenerStoreResolver{Store: store}, Targets: &StoreTargetResolver{Instances: store, Admitter: admit}, Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, target gateway.Target) error {
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(conn, "%s:%s", target.DeploymentID, line); err != nil {
				return err
			}
		}
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("rollout ingress shutdown hung")
		}
	}()
	connect := func() net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	exchange := func(conn net.Conn, deployment, message string) {
		t.Helper()
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, message+"\n"); err != nil {
			t.Fatal(err)
		}
		got, err := bufio.NewReader(conn).ReadString('\n')
		want := deployment + ":" + message + "\n"
		if err != nil || got != want {
			t.Fatalf("reply=%q want=%q err=%v", got, want, err)
		}
	}
	stable := connect()
	exchange(stable, "stable", "before-release")
	if admit.calls.Load() != 0 {
		t.Fatal("warm stable connection caused admission")
	}
	if _, err := store.UpdateDeploymentTraffic(ctx, "candidate", 100); err != nil {
		t.Fatal(err)
	}
	candidate := connect()
	exchange(candidate, "candidate", "cold-release")
	if admit.calls.Load() != 1 {
		t.Fatalf("candidate admissions=%d, want 1", admit.calls.Load())
	}
	exchange(stable, "stable", "retained-after-release")
	freshCandidate := connect()
	exchange(freshCandidate, "candidate", "warm-release")
	if admit.calls.Load() != 1 {
		t.Fatal("warm candidate caused another admission")
	}
	if _, err := store.UpdateDeploymentTraffic(ctx, "stable", 100); err != nil {
		t.Fatal(err)
	}
	rollback := connect()
	exchange(rollback, "stable", "after-rollback")
	exchange(candidate, "candidate", "retained-after-rollback")
	exchange(freshCandidate, "candidate", "retained-warm-after-rollback")
	if admit.calls.Load() != 1 {
		t.Fatal("rollback admitted an unnecessary instance")
	}
	instances, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances=%d, want one per deployment", len(instances))
	}
}
