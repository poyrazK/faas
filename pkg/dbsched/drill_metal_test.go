//go:build metal && linux

// adr: 1027 — a restore drill restores a real database from its node's
// archive into a throwaway database, records it, and leaves nothing.
package dbsched

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

func TestMetalRestoreDrill(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "drbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "dr", 20<<30, &topLeaser{}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: time.Minute}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	source := uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), source) })
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: source, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, source, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, a.rt, source, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'row ' || g FROM generate_series(1, 5000) g")
	// A drill recovers to a minute ago, which must follow the source's
	// creation and its base backup.
	time.Sleep(drillLag + 5*time.Second)

	started := time.Now()
	if got, err := s.RunDrills(ctx); err != nil || got.Started != 1 {
		t.Fatalf("start = %+v, %v", got, err)
	}
	running, _ := store.RunningDrills(ctx)
	if len(running) != 1 {
		t.Fatalf("running drills = %+v", running)
	}
	drill := running[0]
	s.restoreWG.Wait()
	got, err := s.RunDrills(ctx)
	if err != nil || got.OK != 1 {
		vm, _ := store.Get(ctx, drill.ID)
		t.Fatalf("settle = %+v, %v (drill database %+v)", got, err, vm)
	}
	sums, _ := store.DrillsByNode(ctx)
	t.Logf("restore drill passed in %v; recovered to within %v of its target",
		time.Since(started).Round(time.Millisecond), drill.TargetAt.Sub(recoveredTo(ctx, store, drill.ID)).Round(time.Millisecond))
	if sums["dr"].LastOK.IsZero() {
		t.Fatalf("node summary = %+v", sums)
	}
	if _, err := store.Get(ctx, drill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the drill's database survived: %v", err)
	}
	if vols, err := a.rt.ListVolumes(); err != nil || len(vols) != 1 || vols[0].ID != source {
		t.Fatalf("volumes after the drill = %+v, %v; want only the source's", vols, err)
	}
	if _, err := os.Stat(filepath.Join(a.archiveDir, drill.ID)); !os.IsNotExist(err) {
		t.Fatalf("the drill's archive survived: %v", err)
	}
	// The source is untouched and still answers.
	if _, err := s.Wake(ctx, source); err != nil {
		t.Fatal(err)
	}
	if n := query(ctx, t, a.rt, source, password, "SELECT count(*)::text FROM kept"); n != "5000" {
		t.Fatalf("source rows after the drill = %q", n)
	}
}

// recoveredTo reads a finished drill's recovery point from the store.
func recoveredTo(ctx context.Context, store *MemStore, id string) time.Time {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.drills[id].RecoveredTo
}
