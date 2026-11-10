// adr: 20261027090013 §1, §5 — agentd holds a claimed session on one schedd
// stream: it records the placement once the VM is up, freezes an idle VM,
// ends the session on a close request, its idle timeout or a lost VM, and
// queues one that never booted again.
package agentd_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentd"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeSandboxSchedd plays schedd's RunSandbox by the session's script.
type fakeSandboxSchedd struct {
	scheddpb.UnimplementedScheddServer
	mu      sync.Mutex
	scripts map[string]string
	starts  []*scheddpb.SandboxRunStart
	freezes int
	closes  int
	// persists is what agentd answered the "persist" script's exchanges.
	persists []persistAnswer
}

func (f *fakeSandboxSchedd) RunSandbox(stream scheddpb.Schedd_RunSandboxServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	f.mu.Lock()
	f.starts = append(f.starts, start)
	script := f.scripts[start.GetSandboxId()]
	f.mu.Unlock()
	switch script {
	case "full":
		return status.Error(codes.ResourceExhausted, "no capacity")
	case "reject":
		return status.Error(codes.FailedPrecondition, "outside the plan")
	case "no-workspace":
		return status.Error(codes.DataLoss, "the workspace could not be unpacked")
	}
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{
		Instance: "sandbox-" + start.GetSandboxId(), NodeId: "agents-1"}}}); err != nil {
		return err
	}
	if frame, err := stream.Recv(); err != nil || frame.GetProceed() == nil {
		return status.Error(codes.Aborted, "not told to proceed")
	}
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: "WAKE_COLD_BOOT", BootMs: 700}}}); err != nil {
		return err
	}
	// vmmd's usage of the session VM, cumulative (step 11).
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Usage{Usage: &scheddpb.AgentRunUsage{
		Method: 1, Samples: 1, ExpectedSamples: 1, MeasuredMs: 1000, BillableMbMs: 264_000, PeakPrivateMb: 100}}}); err != nil {
		return err
	}
	if script == "lose-vm" {
		time.Sleep(50 * time.Millisecond)
		return status.Error(codes.Unavailable, "vmmd went away")
	}
	if script == "persist" {
		if err := f.persist(stream); err != nil {
			return err
		}
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			// agentd left without closing; vmmd would destroy the VM.
			return err
		}
		switch {
		case frame.GetFreeze() != nil:
			f.mu.Lock()
			f.freezes++
			f.mu.Unlock()
			_ = stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Frozen{Frozen: &scheddpb.SandboxRunFrozen{}}})
		case frame.GetClose() != nil:
			f.mu.Lock()
			f.closes++
			f.mu.Unlock()
			return stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Usage{Usage: &scheddpb.AgentRunUsage{
				Method: 1, Samples: 4, ExpectedSamples: 4, MeasuredMs: 4000, PausedMs: 2000, FrozenMs: 1000, BillableMbMs: 528_000,
				PeakPrivateMb: 300, Final: true}}})
		}
	}
}

func (f *fakeSandboxSchedd) counts() (starts, freezes, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts), f.freezes, f.closes
}

func newSandboxes(t *testing.T, store *agentstore.Store, fake *fakeSandboxSchedd, scfg agentd.SandboxConfig) *agentd.Sandboxes {
	t.Helper()
	srv := grpc.NewServer()
	scheddpb.RegisterScheddServer(srv, fake)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := scheddpb.NewScheddClient(conn)
	replica := agentd.New(store, client, agentd.Config{Owner: "agentd-test", LeaseTTL: 300 * time.Millisecond,
		PollInterval: 10 * time.Millisecond}, nil)
	if scfg.Tick == 0 {
		scfg.Tick = 20 * time.Millisecond
	}
	return replica.NewSandboxes(store, client, scfg)
}

func queueSandbox(t *testing.T, s *agentstore.Store, idleTimeoutSec int) string {
	t.Helper()
	sb, err := s.CreateSandbox(context.Background(), agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral", Preset: "codex",
		RAMMB: 1024, NetworkMode: "registries", IdleTimeoutSec: idleTimeoutSec, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	return sb.ID
}

func sandboxState(t *testing.T, s *agentstore.Store, id string) agentstore.Sandbox {
	t.Helper()
	sb, err := s.Sandbox(context.Background(), account, id)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// holdSandbox drives one claimed session in the background.
func holdSandbox(t *testing.T, sbx *agentd.Sandboxes) <-chan bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		found, err := sbx.ClaimOnce(context.Background())
		if err != nil {
			t.Errorf("claim: %v", err)
		}
		done <- found
	}()
	return done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSandboxReadyThenClosedOnRequest(t *testing.T) {
	_, store := openStore(t)
	id := queueSandbox(t, store, 600)
	fake := &fakeSandboxSchedd{}
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{}))
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	sb := sandboxState(t, store, id)
	if sb.NodeID != "agents-1" || sb.InstanceID != "sandbox-"+id {
		t.Fatalf("placement = %s / %s", sb.NodeID, sb.InstanceID)
	}
	if _, err := store.CloseSandbox(context.Background(), account, id); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb = sandboxState(t, store, id)
	if sb.State != agentstore.SandboxClosed || sb.OutcomeCode != "closed" {
		t.Fatalf("closed session = %s %s", sb.State, sb.OutcomeCode)
	}
	starts, _, closes := fake.counts()
	if starts != 1 || closes != 1 {
		t.Fatalf("%d starts, %d closes", starts, closes)
	}
	// adr: 20261027090013 step 11 — the final usage record, sent at close,
	// is kept before the session ends.
	usage, found, err := store.SandboxSessionUsage(context.Background(), account, id)
	want := agentstore.SandboxUsage{Usage: agentstore.Usage{Method: 1, Samples: 4, ExpectedSamples: 4, MeasuredMillis: 4000,
		PausedMillis: 2000, BillableMBMillis: 528_000, PeakPrivateMB: 300}, FrozenMillis: 1000, Final: true}
	if err != nil || !found || usage != want {
		t.Fatalf("usage = %+v %v %v", usage, found, err)
	}
	if start := fake.starts[0]; start.GetPlan() != "pro" || start.GetRamMb() != 1024 || start.GetVcpu() < 1 || start.GetCpuMillicores() != start.GetVcpu()*1000 {
		t.Fatalf("start = %v", start)
	}
}

func TestSandboxIdleFreezesThenExpires(t *testing.T) {
	_, store := openStore(t)
	id := queueSandbox(t, store, 1)
	fake := &fakeSandboxSchedd{}
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{IdleFreeze: 200 * time.Millisecond}))
	waitFor(t, "frozen", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxFrozen })
	<-done
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxExpired || sb.OutcomeCode != "idle" {
		t.Fatalf("idle session = %s %s", sb.State, sb.OutcomeCode)
	}
	if _, freezes, closes := fake.counts(); freezes != 1 || closes != 1 {
		t.Fatalf("%d freezes, %d closes", freezes, closes)
	}
}

func TestSandboxOutcomesBeforeAndAfterBoot(t *testing.T) {
	for _, tc := range []struct {
		script, state, outcome string
	}{
		{"full", agentstore.SandboxQueued, ""},
		{"reject", agentstore.SandboxFailed, "rejected"},
		{"lose-vm", agentstore.SandboxFailed, "vm_lost"},
	} {
		t.Run(tc.script, func(t *testing.T) {
			_, store := openStore(t)
			id := queueSandbox(t, store, 600)
			fake := &fakeSandboxSchedd{scripts: map[string]string{id: tc.script}}
			if found := <-holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{})); !found {
				t.Fatal("nothing was claimed")
			}
			if sb := sandboxState(t, store, id); sb.State != tc.state || sb.OutcomeCode != tc.outcome {
				t.Fatalf("session = %s %q, want %s %q", sb.State, sb.OutcomeCode, tc.state, tc.outcome)
			}
		})
	}
}
