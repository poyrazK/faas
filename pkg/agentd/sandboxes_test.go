// adr: 20261027090013 §1, §5 — agentd holds a claimed session on one schedd
// stream: it records the placement once the VM is up, freezes an idle VM,
// ends the session on a close request, its idle timeout or a lost VM, and
// queues one that never booted again.
package agentd_test

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	// actions are the browser actions the "actions" script was sent.
	actions []*scheddpb.SandboxRunAction
	// pauses are the capture IDs pauses named; the "pause-busy" script
	// refuses the first, "pause-refused" every one, "pause-late" answers
	// the first only after lateBy, and "pause-break" breaks the stream.
	pauses []string
	lateBy time.Duration
	// volumeBackups are the backups the fake vmmd reported, in order (a
	// running session's one, then each pause's); volumeDeletes the volume
	// deletions agentd asked for, as node/session.
	volumeBackups []string
	volumeDeletes []string
}

func (f *fakeSandboxSchedd) DeleteSandboxVolume(_ context.Context, req *scheddpb.DeleteSandboxVolumeRequest) (*scheddpb.DeleteSandboxVolumeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumeDeletes = append(f.volumeDeletes, req.GetNodeId()+"/"+req.GetSandboxId())
	return &scheddpb.DeleteSandboxVolumeResponse{Deleted: true}, nil
}

func (f *fakeSandboxSchedd) volumeState() (backups, deletes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.volumeBackups...), append([]string(nil), f.volumeDeletes...)
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
	if script == "capture-node-busy" && start.GetResume() != nil {
		return status.Error(codes.Unavailable, "the capture's node cannot take it now")
	}
	node := "agents-1"
	if script == "elsewhere" {
		node = "agents-2"
	}
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{
		Instance: "sandbox-" + start.GetSandboxId(), NodeId: node}}}); err != nil {
		return err
	}
	if frame, err := stream.Recv(); err != nil || frame.GetProceed() == nil {
		return status.Error(codes.Aborted, "not told to proceed")
	}
	if script == "resume-stall" && start.GetResume() != nil {
		// The restore never reports booted; a close ends it.
		for {
			frame, err := stream.Recv()
			if err != nil {
				return err
			}
			if frame.GetClose() != nil {
				return nil
			}
		}
	}
	method := "WAKE_COLD_BOOT"
	if start.GetResume() != nil && script != "restore-fails" {
		method = "WAKE_RESTORE"
	}
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: method, BootMs: 700,
		Actions: script == "actions", EgressPolicy: script == "egress"}}}); err != nil {
		return err
	}
	// vmmd's usage of the session VM, cumulative (step 11).
	if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Usage{Usage: &scheddpb.AgentRunUsage{
		Method: 1, Samples: 1, ExpectedSamples: 1, MeasuredMs: 1000, BillableMbMs: 264_000, PeakPrivateMb: 100}}}); err != nil {
		return err
	}
	if script == "egress" {
		// vmmd's domain log (ADR-20261117091026 §4), a repeat included.
		if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_WebDomains{WebDomains: &scheddpb.SandboxRunWebDomains{
			Domains: []*scheddpb.SandboxRunWebDomain{{Domain: "example.com", AtUnixMs: 1_800_000_000_000},
				{Domain: "admin.example.com", Refused: "policy", AtUnixMs: 1_800_000_001_000},
				{Domain: "example.com", AtUnixMs: 1_800_000_002_000}}}}}); err != nil {
			return err
		}
	}
	if start.GetVolumeGb() > 0 {
		// vmmd backs a running session's volume up (ADR-20261122132055 §4).
		id := uuid.NewString()
		f.mu.Lock()
		f.volumeBackups = append(f.volumeBackups, id)
		f.mu.Unlock()
		if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_VolumeBackup{VolumeBackup: &scheddpb.SandboxRunVolumeBackup{
			BackupId: id, SizeBytes: 10 << 30, DataChunks: 3, UploadedBytes: 9000, BackupMs: 250}}}); err != nil {
			return err
		}
	}
	if script == "lose-vm" {
		time.Sleep(50 * time.Millisecond)
		return status.Error(codes.Unavailable, "vmmd went away")
	}
	switch script {
	case "task":
		if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_TaskDone{TaskDone: &scheddpb.SandboxRunTaskDone{
			ExitCode: 0, Output: []byte(`{"title":"Example"}`)}}}); err != nil {
			return err
		}
	case "task-timeout":
		if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_TaskDone{TaskDone: &scheddpb.SandboxRunTaskDone{
			ExitCode: -1, TimedOut: true, Output: []byte("partial")}}}); err != nil {
			return err
		}
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
		case frame.GetAction() != nil:
			a := frame.GetAction()
			f.mu.Lock()
			f.actions = append(f.actions, a)
			f.mu.Unlock()
			var shot int64
			if a.GetScreenshot() {
				shot = 2048
			}
			_ = stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_ActionDone{ActionDone: &scheddpb.SandboxRunActionDone{
				ActionId: a.GetActionId(), Output: []byte(`{"title":"` + a.GetKind() + `"}`), ScreenshotBytes: shot}}})
		case frame.GetFreeze() != nil:
			f.mu.Lock()
			f.freezes++
			f.mu.Unlock()
			_ = stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Frozen{Frozen: &scheddpb.SandboxRunFrozen{}}})
		case frame.GetPause() != nil:
			id := frame.GetPause().GetCaptureId()
			f.mu.Lock()
			f.pauses = append(f.pauses, id)
			refuse := script == "pause-busy" && len(f.pauses) == 1 || script == "pause-refused"
			lateBy := f.lateBy
			f.mu.Unlock()
			if script == "pause-late" {
				// vmmd captures inline: it reads no frame until it answers.
				time.Sleep(lateBy)
			}
			if script == "pause-break" {
				// The stream breaks after vmmd captured and destroyed the VM.
				return status.Error(codes.Unavailable, "the stream was reset")
			}
			if refuse {
				_ = stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PauseRefused{PauseRefused: &scheddpb.SandboxRunPauseRefused{Reason: "busy"}}})
				continue
			}
			// vmmd captures the VM, sends its last records and paused, and
			// ends the stream (ADR-20261122132055 §2).
			mem := "snap/sandbox-session-" + start.GetAccountId() + "/captures/" + id + "/v2/mem"
			var volumeBackup string
			if start.GetVolumeGb() > 0 {
				volumeBackup = uuid.NewString()
				f.mu.Lock()
				f.volumeBackups = append(f.volumeBackups, volumeBackup)
				f.mu.Unlock()
			}
			return stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Paused{Paused: &scheddpb.SandboxRunPaused{
				StorageKey: mem, VmstateStorageKey: strings.TrimSuffix(mem, "/mem") + "/vmstate", FcVersion: "1.10.0",
				MemBytes: 2 << 30, VmstateBytes: 40_000, PauseMs: 900, VolumeBackupId: volumeBackup}}})
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

// adr: 20261116182615 §3 — a task's start carries its kind and arguments,
// and its end records the result and closes the session, final usage
// included.
func TestSandboxTaskRecordsItsResultAndCloses(t *testing.T) {
	for _, tc := range []struct {
		script, state, outcome, output string
		exit                           int
	}{
		{"task", agentstore.SandboxClosed, "task_done", `{"title":"Example"}`, 0},
		{"task-timeout", agentstore.SandboxFailed, "task_failed", "partial", -1},
	} {
		t.Run(tc.script, func(t *testing.T) {
			_, store := openStore(t)
			sb, err := store.CreateSandbox(context.Background(), agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral",
				Preset: "codex", RAMMB: 1024, NetworkMode: "web", Browser: true, IdleTimeoutSec: 600, ExpiresAt: time.Now().Add(time.Hour),
				QueuedMax: 100, Task: &agentstore.SandboxTask{Kind: agentstore.SandboxTaskBrowse, URL: "https://example.com/", MaxChars: 300}})
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeSandboxSchedd{scripts: map[string]string{sb.ID: tc.script}}
			if found := <-holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{})); !found {
				t.Fatal("nothing was claimed")
			}
			if got := sandboxState(t, store, sb.ID); got.State != tc.state || got.OutcomeCode != tc.outcome {
				t.Fatalf("session = %s %q, want %s %q", got.State, got.OutcomeCode, tc.state, tc.outcome)
			}
			res, err := store.SandboxTaskResult(context.Background(), account, sb.ID)
			if err != nil || res.ExitCode == nil || *res.ExitCode != tc.exit || string(res.Output) != tc.output {
				t.Fatalf("result = %+v, %v", res, err)
			}
			task := fake.starts[0].GetTask()
			if task.GetKind() != "browse" || task.GetUrl() != "https://example.com/" || task.GetMaxChars() != 300 ||
				task.GetTimeoutMs() != 120_000 || task.GetMaxOutputBytes() != 256<<10 || !fake.starts[0].GetBrowser() {
				t.Fatalf("start task = %v", task)
			}
			if _, _, closes := fake.counts(); closes != 1 {
				t.Fatalf("%d closes; the final usage comes with the close", closes)
			}
			if _, found, err := store.SandboxSessionUsage(context.Background(), account, sb.ID); err != nil || !found {
				t.Fatalf("no final usage: %v %v", found, err)
			}
		})
	}
}

// adr: 20261117065818 §2 — agentd sends a browser session its pending
// actions one at a time once vmmd announced actions, records each end
// under the lease, and ends the open ones as the session ends.
func TestSandboxActionsRunInOrderAndEndWithTheSession(t *testing.T) {
	pool, store := openStore(t)
	ctx := context.Background()
	const endpoint = "55555555-5555-5555-5555-555555555555"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	sb, err := store.CreateSandbox(ctx, agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral", Preset: "codex",
		RAMMB: 1024, NetworkMode: "web", Browser: true, IdleTimeoutSec: 600, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	act := func(call, kind string, args agentstore.SandboxActionArgs) agentstore.SandboxAction {
		a, _, err := store.CreateSandboxAction(ctx, account, sb.ID, kind, args, agentstore.SandboxTaskCall{EndpointID: endpoint,
			ThreadID: "thread-1", ToolCallID: call, IssuedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	nav := act("call_1", "navigate", agentstore.SandboxActionArgs{URL: "https://example.com/"})
	click := act("call_2", "click", agentstore.SandboxActionArgs{Ref: 4, MaxChars: 900, Screenshot: true})
	fake := &fakeSandboxSchedd{scripts: map[string]string{sb.ID: "actions"}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, sb.ID).State == agentstore.SandboxReady })
	actionState := func(id string) (state string, exit *int, output string) {
		var out []byte
		if err := pool.QueryRow(ctx, `SELECT state, exit_code, output FROM sandbox_actions WHERE id=$1`, id).Scan(&state, &exit, &out); err != nil {
			t.Fatal(err)
		}
		return state, exit, string(out)
	}
	waitFor(t, "both actions", func() bool {
		if err := sbx.PumpActionsOnce(ctx); err != nil {
			t.Fatal(err)
		}
		state, _, _ := actionState(click.ID)
		return state == agentstore.SandboxActionDone
	})
	for _, tc := range []struct{ id, title string }{{nav.ID, "navigate"}, {click.ID, "click"}} {
		if state, exit, out := actionState(tc.id); state != agentstore.SandboxActionDone || exit == nil || *exit != 0 ||
			out != `{"title":"`+tc.title+`"}` {
			t.Fatalf("action %s = %s %v %q", tc.title, state, exit, out)
		}
	}
	fake.mu.Lock()
	sent := fake.actions
	fake.mu.Unlock()
	if len(sent) != 2 || sent[0].GetKind() != "navigate" || sent[0].GetUrl() != "https://example.com/" ||
		sent[0].GetMaxChars() != 4000 || sent[0].GetTimeoutMs() != 30_000 || sent[1].GetKind() != "click" ||
		sent[1].GetRef() != 4 || sent[1].GetMaxChars() != 900 || sent[1].GetMaxOutputBytes() != 256<<10 || sent[0].GetScreenshot() ||
		!sent[1].GetScreenshot() {
		t.Fatalf("sent %v", sent)
	}
	// adr: 20261117065818 §5 — a kept screenshot is recorded at the key vmmd
	// derives from the session and the action.
	var navKey, clickKey *string
	var clickBytes *int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT screenshot_key FROM sandbox_actions WHERE id=$1), screenshot_key, screenshot_bytes
		FROM sandbox_actions WHERE id=$2`, nav.ID, click.ID).Scan(&navKey, &clickKey, &clickBytes); err != nil {
		t.Fatal(err)
	}
	if want := "sandbox-screenshots/" + account + "/" + sb.ID + "/" + click.ID + ".jpg"; navKey != nil || clickKey == nil || *clickKey != want ||
		clickBytes == nil || *clickBytes != 2048 {
		t.Fatalf("screenshots: navigate %v, click %v (%v)", navKey, clickKey, clickBytes)
	}
	pending := act("call_3", "snapshot", agentstore.SandboxActionArgs{})
	if _, err := store.CloseSandbox(ctx, account, sb.ID); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	if state, exit, _ := actionState(pending.ID); state != agentstore.SandboxActionDone || exit != nil {
		t.Fatalf("a pending action at the session's end = %s %v", state, exit)
	}
}

// adr: 20261117091026 §3 — agentd sends a session's domain policy on its
// start frame, and closes a session with a policy whose node boots it
// without enforcing one.
func TestSandboxEgressPolicyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		script, state, outcome string
	}{
		{"egress", agentstore.SandboxReady, ""},
		{"", agentstore.SandboxFailed, "egress_policy_unsupported"},
	} {
		t.Run("script="+tc.script, func(t *testing.T) {
			_, store := openStore(t)
			sb, err := store.CreateSandbox(context.Background(), agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral",
				Preset: "codex", RAMMB: 1024, NetworkMode: "web", IdleTimeoutSec: 600, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100,
				EgressAllow: []string{"example.com"}, EgressDeny: []string{"admin.example.com"}})
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeSandboxSchedd{scripts: map[string]string{sb.ID: tc.script}}
			done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{}))
			if tc.state == agentstore.SandboxReady {
				waitFor(t, "ready", func() bool { return sandboxState(t, store, sb.ID).State == agentstore.SandboxReady })
				var log []agentstore.SandboxDomain
				waitFor(t, "the domain log", func() bool {
					log, err = store.SandboxDomains(context.Background(), account, sb.ID)
					return err == nil && len(log) == 2
				})
				if log[0].Domain != "example.com" || log[0].Refused != "" || !log[0].FirstSeen.Equal(time.UnixMilli(1_800_000_000_000)) ||
					log[1].Domain != "admin.example.com" || log[1].Refused != "policy" {
					t.Fatalf("domain log %+v", log)
				}
				if _, err := store.CloseSandbox(context.Background(), account, sb.ID); err != nil {
					t.Fatal(err)
				}
			}
			if !<-done {
				t.Fatal("nothing was claimed")
			}
			if tc.outcome != "" {
				if got := sandboxState(t, store, sb.ID); got.State != tc.state || got.OutcomeCode != tc.outcome {
					t.Fatalf("session = %s %q", got.State, got.OutcomeCode)
				}
			}
			start := fake.starts[0]
			if !start.GetWebDomainLog() {
				t.Fatal("agentd did not ask for the domain log")
			}
			if !slices.Equal(start.GetEgressAllow(), []string{"example.com"}) || !slices.Equal(start.GetEgressDeny(), []string{"admin.example.com"}) {
				t.Fatalf("start policy %v / %v", start.GetEgressAllow(), start.GetEgressDeny())
			}
		})
	}
}
