//go:build metal && linux

// adr: 1027 — the scheduler upgrades a crashed PostgreSQL 14 database to 17
// in place; the commit the crash left out of the archive reaches it before
// pg_upgrade, so a restore to just before the upgrade recovers it on 14,
// and a restore after the upgrade recovers on 17.
package dbsched

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

func TestMetalMajorUpgradeThroughScheduler(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the upgrade metal test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	bases := map[int]string{}
	for _, major := range []int{14, 17} {
		bases[major] = metalBase(ctx, t, workdir, guestInit, major)
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "upbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	node := newMetalNode(t, workdir, bases, "up", 20<<30, &topLeaser{}, bucket)
	node.node.Majors = []int{14, 17}
	store := NewMemStore()
	seedNodes(t, store, node.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: 2 * time.Minute}, nil)

	id, before, after := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		cleanup := context.WithoutCancel(ctx)
		for _, db := range []string{after, before, id} {
			_ = s.Delete(cleanup, db)
		}
	})
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 14, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, node.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'row ' || g FROM generate_series(1, 2000) g")
	// A commit, then a crash before archive_timeout ships its segment.
	query(ctx, t, node.rt, id, password, "INSERT INTO kept VALUES ('last on 14')")
	time.Sleep(time.Second)
	target := time.Now()
	time.Sleep(time.Second)
	killNodeVM(t, "up", id)
	time.Sleep(time.Second) // vmmd notices the exit

	started := time.Now()
	upgraded, err := s.Upgrade(ctx, id, 17, uuid.NewString())
	if err != nil || upgraded.Major != 17 || len(upgraded.MajorHistory) != 1 || upgraded.LastBackupAt.IsZero() {
		t.Fatalf("upgrade = %+v, %v", upgraded, err)
	}
	t.Logf("upgraded a crashed PostgreSQL 14 database to 17 through the scheduler in %v", time.Since(started).Round(time.Millisecond))
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	if v := query(ctx, t, node.rt, id, password, "SELECT current_setting('server_version_num')"); v[:2] != "17" {
		t.Fatalf("server version %s after the upgrade", v)
	}
	if n := query(ctx, t, node.rt, id, password, "SELECT count(*)::text FROM kept"); n != "2001" {
		t.Fatalf("rows after the upgrade = %s", n)
	}
	query(ctx, t, node.rt, id, password, "INSERT INTO kept VALUES ('first on 17')")

	// Before the upgrade: PostgreSQL 14, with the commit the crash left
	// unarchived.
	restore := func(dbID string, major int, at time.Time) {
		t.Helper()
		if _, err := s.Restore(ctx, RestoreRequest{ProvisionRequest: ProvisionRequest{DatabaseID: dbID, Major: major, VcpuCount: 1, MemMiB: 384,
			VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600}, Source: id, Target: at}); err != nil {
			t.Fatalf("restore on %d: %v", major, err)
		}
		s.restoreWG.Wait()
		if vm, err := store.Get(ctx, dbID); err != nil || vm.State != StateParked || vm.Major != major {
			t.Fatalf("restored on %d = %+v, %v", major, vm, err)
		}
		if _, err := s.Wake(ctx, dbID); err != nil {
			t.Fatal(err)
		}
	}
	restore(before, 14, target)
	if v := query(ctx, t, node.rt, before, password, "SELECT current_setting('server_version_num')"); v[:2] != "14" {
		t.Fatalf("restore before the upgrade runs %s", v)
	}
	if got := query(ctx, t, node.rt, before, password, "SELECT string_agg(v, ',' ORDER BY v) FROM kept WHERE v NOT LIKE 'row %'"); got != "last on 14" {
		t.Fatalf("restore before the upgrade holds %q; want the commit the crash left unarchived", got)
	}
	_ = s.Park(ctx, before)

	// After the upgrade: PostgreSQL 17 with its writes.
	time.Sleep(time.Second)
	restore(after, 17, time.Now())
	if got := query(ctx, t, node.rt, after, password, "SELECT string_agg(v, ',' ORDER BY v) FROM kept WHERE v NOT LIKE 'row %'"); got != "first on 17,last on 14" {
		t.Fatalf("restore after the upgrade holds %q", got)
	}
}

// metalBase is the PostgreSQL major's base with guestInit, built once per
// guest-init digest under workdir/major-bases.
func metalBase(ctx context.Context, t *testing.T, workdir, guestInit string, major int) string {
	t.Helper()
	initDigest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	image := dbvm.PostgresImages[major]
	path := dbvm.BasePath(filepath.Join(workdir, "major-bases"), major, image, initDigest)
	if _, err := os.Stat(path); err != nil {
		if err := dbvm.BuildBase(ctx, image, guestInit, path); err != nil {
			t.Fatalf("build the PostgreSQL %d base: %v", major, err)
		}
	}
	return path
}
