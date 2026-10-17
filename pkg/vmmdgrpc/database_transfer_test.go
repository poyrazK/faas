// adr: 1027 — a database volume crosses nodes through schedd's relay and is
// installed only if it arrives whole.
package vmmdgrpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

type nopLeaser struct{}

// Adopt is not exercised here: no VM outlives this test's runtime.
func (_ nopLeaser) Adopt(string, int) (fcvm.Lease, error) {
	return fcvm.Lease{}, errors.New("adopt: not used in this test")
}

func (nopLeaser) Acquire(instance string) (fcvm.Lease, error) {
	return fcvm.Lease{Instance: instance, UID: 29999, GID: 29999}, nil
}
func (nopLeaser) Release(string) error { return nil }

// volumeRuntime is a database runtime over temporary directories that can
// only hold and move volumes.
func volumeRuntime(t *testing.T) *dbvm.Runtime {
	t.Helper()
	dir := t.TempDir()
	fc := filepath.Join(dir, "firecracker-v1.7.0")
	if err := os.WriteFile(fc, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test stub
		t.Fatal(err)
	}
	// The jail's relay sockets need a short path; macOS temp dirs are long.
	short, err := os.MkdirTemp("/tmp", "v")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{JailBase: filepath.Join(short, "j"), VolumeRoot: filepath.Join(dir, "volumes"),
		KernelPath: filepath.Join(dir, "vmlinux"), ParentCgroup: "faas-db-test", FirecrackerBin: fc, Leases: nopLeaser{}})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func vmmdClient(t *testing.T, rt DatabaseRuntime) vmmdpb.VmmdClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt))
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

// relay pipes one node's export into another's import, as schedd does,
// letting tamper rewrite messages in flight.
func relay(ctx context.Context, from, to vmmdpb.VmmdClient, id string, tamper func(*vmmdpb.DatabaseVolumeChunk)) error {
	out, err := from.ExportDatabaseVolume(ctx, &vmmdpb.ExportDatabaseVolumeRequest{DatabaseId: id})
	if err != nil {
		return err
	}
	in, err := to.ImportDatabaseVolume(ctx)
	if err != nil {
		return err
	}
	for {
		msg, err := out.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if tamper != nil {
			tamper(msg)
		}
		if err := in.Send(msg); err != nil {
			break // the importer ended the stream; CloseAndRecv reports why
		}
	}
	_, err = in.CloseAndRecv()
	return err
}

func TestDatabaseVolumeMovesThroughTheRelay(t *testing.T) {
	src, dst := volumeRuntime(t), volumeRuntime(t)
	if err := os.MkdirAll(filepath.Dir(src.VolumePath(testDatabaseID)), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(src.VolumePath(testDatabaseID))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(dbvm.MinVolumeBytes)
	_, _ = f.WriteAt(bytes.Repeat([]byte("pg"), 3<<20), 1<<20) // spans several extents
	_ = f.Close()
	from := vmmdClient(t, &fakeDatabaseRuntime{vols: src})
	to := vmmdClient(t, &fakeDatabaseRuntime{vols: dst})
	ctx := context.Background()

	err = relay(ctx, from, to, testDatabaseID, func(m *vmmdpb.DatabaseVolumeChunk) {
		if len(m.GetSha256()) > 0 {
			m.Sha256[0] ^= 1
		}
	})
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("tampered transfer = %v, want DataLoss", err)
	}
	if _, err := os.Stat(dst.VolumePath(testDatabaseID)); !os.IsNotExist(err) {
		t.Fatalf("a tampered volume was installed: %v", err)
	}
	if err := relay(ctx, from, to, testDatabaseID, nil); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	want, _ := os.ReadFile(src.VolumePath(testDatabaseID))
	got, err := os.ReadFile(dst.VolumePath(testDatabaseID))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("moved volume differs (read err %v)", err)
	}
	if err := relay(ctx, from, to, testDatabaseID, nil); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("second import = %v, want AlreadyExists", err)
	}
}

func TestDatabaseTransferAndSyncRefuseOtherCallers(t *testing.T) {
	rt := &fakeDatabaseRuntime{}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if _, err := s.SyncDatabaseArchive(tlsCaller("pgproxyd.faas"), &vmmdpb.SyncDatabaseArchiveRequest{DatabaseId: testDatabaseID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("pgproxyd sync = %v", err)
	}
	resp, err := s.SyncDatabaseArchive(tlsCaller("schedd.faas"), &vmmdpb.SyncDatabaseArchiveRequest{DatabaseId: testDatabaseID})
	if err != nil || resp.GetUploaded() != 3 {
		t.Fatalf("schedd sync = %+v, %v", resp, err)
	}
	if _, err := s.DeleteDatabaseVolume(unixCaller(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: testDatabaseID, ReleaseArchive: true, Archive: true}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("release with drop = %v", err)
	}
	rt.calls = nil
	if _, err := s.DeleteDatabaseVolume(unixCaller(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: testDatabaseID, ReleaseArchive: true}); err != nil {
		t.Fatal(err)
	}
	if len(rt.calls) != 2 || rt.calls[0] != "delete_volume" || rt.calls[1] != "release_archive" {
		t.Fatalf("release calls = %v", rt.calls)
	}
}
