// adr: 20261027090013 step 7 — a persist writes the guest's archive to the
// key agentd reserved and answers only once agentd recorded it; a refusal or
// a missing record leaves no archive behind; a session from a snapshot boots
// only with its archive unpacked.

package vmmdgrpc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

const workspaceArchive = "a workspace archive"

// memStorage is an in-memory artifact store.
type memStorage struct {
	mu   sync.Mutex
	blob map[string][]byte
}

func (m *memStorage) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blob[key] = b
	return nil
}

func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blob[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blob, key)
	return nil
}

func (m *memStorage) keys() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.blob {
		out[k] = string(v)
	}
	return out
}

// workspaceVMM serves a guest that archives and unpacks its workspace.
type workspaceVMM struct {
	*sandboxRunVMM
	mu        sync.Mutex
	extracted []string
	archives  []sandboxproto.Request
}

func (v *workspaceVMM) DialSandboxSession(ctx context.Context, instance, sandboxID string) (net.Conn, error) {
	if instance != "sbx-vm" || sandboxID != "sbx-1" {
		return nil, fcvm.ErrSandboxNotFound
	}
	host, guest := net.Pipe()
	go func() { _ = sandboxproto.Serve(context.WithoutCancel(ctx), guest, v.guest) }()
	return host, nil
}

func (v *workspaceVMM) guest(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	switch req.Op {
	case sandboxproto.OpArchiveCreate:
		v.mu.Lock()
		v.archives = append(v.archives, req)
		v.mu.Unlock()
		if req.Path == "huge" {
			return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeTooLarge, Message: "over"}
		}
		_, _ = call.Data().Write([]byte(workspaceArchive))
		sum := sha256.Sum256([]byte(workspaceArchive))
		return sandboxproto.ArchiveResult{Bytes: int64(len(workspaceArchive)), Entries: 4, SHA256: hex.EncodeToString(sum[:])}, nil
	case sandboxproto.OpArchiveExtract:
		var buf bytes.Buffer
		n, err := call.ReadUpload(&buf, 1<<20)
		if err != nil {
			return nil, err
		}
		v.mu.Lock()
		v.extracted = append(v.extracted, buf.String())
		v.mu.Unlock()
		return sandboxproto.ArchiveResult{Bytes: n, Entries: 1}, nil
	}
	return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeUnsupported}
}

func newWorkspaceClient(t *testing.T, vmm vmmdgrpc.VmmdAPI, store storage.StorageBackend) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_sandbox_workspace_test"), "1.0.0", nil).WithAgentRuns(true).
		WithSandboxWorkspaces(store).Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

// agentdSide answers persist events on a session stream as agentd would:
// grant, then record when record is true.
func agentdSide(t *testing.T, stream vmmdpb.Vmmd_RunSandboxClient, grant *vmmdpb.RunSandboxPersistGrant, record bool) <-chan *vmmdpb.RunSandboxPersistDone {
	t.Helper()
	done := make(chan *vmmdpb.RunSandboxPersistDone, 1)
	go func() {
		for {
			ev, err := stream.Recv()
			if err != nil {
				return
			}
			switch {
			case ev.GetPersistRequested() != nil:
				g := proto.Clone(grant).(*vmmdpb.RunSandboxPersistGrant)
				g.RequestId = ev.GetPersistRequested().GetRequestId()
				_ = stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistGrant{PersistGrant: g}})
			case ev.GetPersistDone() != nil:
				d := ev.GetPersistDone()
				done <- d
				_ = stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistRecorded{PersistRecorded: &vmmdpb.RunSandboxPersistRecorded{
					RequestId: d.GetRequestId(), Recorded: record && !d.GetFailed()}}})
			}
		}
	}()
	return done
}

func persist(t *testing.T, cli vmmdpb.VmmdClient, path string) (*vmmdpb.PersistSandboxWorkspaceResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return cli.PersistSandboxWorkspace(ctx, &vmmdpb.PersistSandboxWorkspaceRequest{Instance: "sbx-vm", SandboxId: "sbx-1", Path: path})
}

const snapshotKey = "agents/workspaces/acct-1/snap-1.tar.gz"

func granted() *vmmdpb.RunSandboxPersistGrant {
	return &vmmdpb.RunSandboxPersistGrant{SnapshotId: "snap-1", StorageKey: snapshotKey, MaxBytes: 1 << 20, ExpiresAtUnixMs: 42}
}

func TestPersistWritesTheReservedKeyAndWaitsForTheRecord(t *testing.T) {
	store := &memStorage{blob: map[string][]byte{}}
	vmm := &workspaceVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newWorkspaceClient(t, vmm, store)
	stream := bootSandbox(t, cli)
	done := agentdSide(t, stream, granted(), true)
	resp, err := persist(t, cli, "src")
	if err != nil {
		t.Fatal(err)
	}
	d := <-done
	sum := sha256.Sum256([]byte(workspaceArchive))
	if resp.GetSnapshotId() != "snap-1" || resp.GetBytes() != int64(len(workspaceArchive)) || resp.GetEntries() != 4 ||
		resp.GetSha256() != hex.EncodeToString(sum[:]) || resp.GetExpiresAtUnixMs() != 42 {
		t.Fatalf("persist answer %+v", resp)
	}
	if d.GetSnapshotId() != "snap-1" || d.GetBytes() != resp.GetBytes() || d.GetSha256() != resp.GetSha256() || d.GetFailed() {
		t.Fatalf("done report %+v", d)
	}
	if got := store.keys()[snapshotKey]; got != workspaceArchive {
		t.Fatalf("stored %q", got)
	}
	if req := vmm.archives[0]; req.Path != "src" || req.MaxBytes != 1<<20 {
		t.Fatalf("guest archive request %+v", req)
	}
}

func TestPersistRefusalsLeaveNoArchive(t *testing.T) {
	limit, err := json.Marshal(api.ErrAgentLimit(api.PlanPro, "SandboxSnapshotBytesPerAccount", 10, 12))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		grant  *vmmdpb.RunSandboxPersistGrant
		record bool
		path   string
		code   string
	}{
		{"quota refusal", &vmmdpb.RunSandboxPersistGrant{Refusal: limit}, true, "", api.CodeAgentLimit},
		{"not recorded", granted(), false, "", api.CodeSandboxWorkspaceUnavailable},
		{"guest refusal", granted(), true, "huge", api.CodeSandboxTooLarge},
	} {
		store := &memStorage{blob: map[string][]byte{}}
		cli := newWorkspaceClient(t, &workspaceVMM{sandboxRunVMM: newSandboxRunVMM()}, store)
		stream := bootSandbox(t, cli)
		done := agentdSide(t, stream, tc.grant, tc.record)
		_, err := persist(t, cli, tc.path)
		p, _ := grpcerr.FromStatus(err)
		if err == nil || p == nil || p.Code != tc.code {
			t.Errorf("%s: persist = %v (%+v), want %s", tc.name, err, p, tc.code)
		}
		if keys := store.keys(); len(keys) != 0 {
			t.Errorf("%s: archive left behind: %v", tc.name, keys)
		}
		if tc.path == "huge" {
			if d := <-done; !d.GetFailed() {
				t.Errorf("%s: agentd was not told the reservation failed: %+v", tc.name, d)
			}
		}
	}
}

func TestPersistNeedsTheSessionStream(t *testing.T) {
	store := &memStorage{blob: map[string][]byte{}}
	cli := newWorkspaceClient(t, &workspaceVMM{sandboxRunVMM: newSandboxRunVMM()}, store)
	if _, err := persist(t, cli, ""); status.Code(err) != codes.NotFound {
		t.Fatalf("persist without a session = %v", err)
	}
	if _, err := persist(t, cli, "../etc"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("persist outside the workspace = %v", err)
	}
}

func TestSessionFromASnapshotBootsWithItsArchive(t *testing.T) {
	store := &memStorage{blob: map[string][]byte{snapshotKey: []byte(workspaceArchive)}}
	vmm := &workspaceVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newWorkspaceClient(t, vmm, store)
	for _, tc := range []struct {
		key    string
		booted bool
	}{
		{snapshotKey, true},
		{"agents/workspaces/acct-2/snap-9.tar.gz", false},
		{"agents/workspaces/acct-1/missing.tar.gz", false},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := cli.RunSandbox(ctx)
		if err != nil {
			t.Fatal(err)
		}
		start := sandboxRunStart()
		start.GetStart().WorkspaceKey = tc.key
		if err := stream.Send(start); err != nil {
			t.Fatal(err)
		}
		ev, err := stream.Recv()
		cancel()
		if tc.booted != (err == nil && ev.GetBooted() != nil) {
			t.Fatalf("%s: first event = %v, %v", tc.key, ev, err)
		}
		if !tc.booted {
			if p, _ := grpcerr.FromStatus(err); p == nil || p.Code != api.CodeSandboxWorkspaceUnavailable {
				t.Fatalf("%s: refusal %v", tc.key, err)
			}
		}
	}
	if len(vmm.extracted) != 1 || vmm.extracted[0] != workspaceArchive {
		t.Fatalf("unpacked %q", vmm.extracted)
	}
	if _, destroyed, _, _ := vmm.state(); len(destroyed) != 3 {
		t.Fatalf("destroyed %v; every session's VM ends with its stream", destroyed)
	}
}
