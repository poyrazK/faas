package vmmdgrpc_test

// adr: 20261110210000

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sealingParkVMM seals captures (ADR-733's capability) and records the
// storage each park published through.
type sealingParkVMM struct {
	*fakeVMM
	store     storage.StorageBackend
	sealedIDs []string
	parked    []fcvm.SnapshotSpec
}

func (s *sealingParkVMM) SealCaptureStorage(captureID string) (storage.StorageBackend, []byte, error) {
	s.sealedIDs = append(s.sealedIDs, captureID)
	return s.store, []byte("sealed:" + captureID), nil
}

func newSealingServer(t *testing.T, vmm vmmdgrpc.VmmdAPI) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_sealed_test"), "1.0.0", nil).Register(srv)
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

func TestPauseAndSnapshot_Sealed(t *testing.T) {
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	mem := state.SnapshotCaptureMemKey("dep-1", state.SnapshotTierInit, token)
	vmstate := state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem})
	for _, tc := range []struct {
		name     string
		req      *vmmdpb.PauseAndSnapshotRequest
		wantCode codes.Code
	}{
		{name: "sealed with the key's token", req: &vmmdpb.PauseAndSnapshotRequest{StorageKey: mem, VmstateStorageKey: vmstate, SealCaptureId: token}},
		{name: "another id", req: &vmmdpb.PauseAndSnapshotRequest{StorageKey: mem, VmstateStorageKey: vmstate, SealCaptureId: uuid.NewString()}, wantCode: codes.InvalidArgument},
		{name: "host-path vmstate", req: &vmmdpb.PauseAndSnapshotRequest{StorageKey: mem, VmstatePath: "/snap/vmstate", SealCaptureId: token}, wantCode: codes.InvalidArgument},
		{name: "legacy key", req: &vmmdpb.PauseAndSnapshotRequest{StorageKey: "snap/dep-1/mem", VmstateStorageKey: "snap/dep-1/vmstate", SealCaptureId: token}, wantCode: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &sealingParkVMM{fakeVMM: &fakeVMM{}, store: local}
			vmm.parkFn = func(_ context.Context, _ string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
				vmm.parked = append(vmm.parked, spec)
				return fcvm.SnapshotInfo{MemBytes: 1 << 20}, nil
			}
			tc.req.Instance = "live-1"
			resp, err := newSealingServer(t, vmm).PauseAndSnapshot(context.Background(), tc.req)
			if tc.wantCode != codes.OK {
				if status.Code(err) != tc.wantCode || len(vmm.parked) != 0 {
					t.Fatalf("err = %v parks=%d, want %v before any capture", err, len(vmm.parked), tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("PauseAndSnapshot: %v", err)
			}
			if string(resp.GetSealedKey()) != "sealed:"+token || len(vmm.parked) != 1 || vmm.parked[0].Storage != local {
				t.Fatalf("sealed_key=%q parks=%+v, want the sealed key and the sealing storage", resp.GetSealedKey(), vmm.parked)
			}
		})
	}
}

func TestPauseAndSnapshot_SealRefusedWithoutASealer(t *testing.T) {
	token := uuid.NewString()
	mem := state.SnapshotCaptureMemKey("dep-1", state.SnapshotTierInit, token)
	f := &fakeVMM{}
	parked := 0
	f.parkFn = func(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
		parked++
		return fcvm.SnapshotInfo{}, nil
	}
	_, err := newSealingServer(t, f).PauseAndSnapshot(context.Background(), &vmmdpb.PauseAndSnapshotRequest{
		Instance: "live-1", StorageKey: mem, VmstateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem}), SealCaptureId: token})
	if status.Code(err) != codes.InvalidArgument || parked != 0 {
		t.Fatalf("err = %v parks=%d, want InvalidArgument and no plaintext capture", err, parked)
	}
}
