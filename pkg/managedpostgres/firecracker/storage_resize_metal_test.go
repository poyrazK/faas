//go:build metal && linux

// adr: 1027 — a customer storage resize end to end: the managed PostgreSQL
// service journals the target, its reconciler drives the driver's Update,
// schedd parks the running VM, vmmd grows the volume offline, and the
// database comes back on the larger filesystem with its data and logins.
package firecracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestMetalStorageResizeThroughService(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	connect := connectThroughProxy(st.proxyAddr)
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu-central", MaxDatabasesPerAccount: 3,
		Defaults: map[string]string{"eu-central": "native"},
		Backends: []managedpostgres.BackendConfig{{ID: "native", Driver: Driver, Region: "eu-central", Namespace: "native"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{Driver: func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return st.p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	account := uuid.NewString()
	// settle reconciles until the database is ready on want.
	settle := func(id string, want func(managedpostgres.Spec) bool) (managedpostgres.Database, time.Duration) {
		t.Helper()
		started := time.Now()
		for {
			d, err := svc.Reconcile(ctx, account, id)
			if err == nil && d.State == managedpostgres.StateReady && want(d.Spec) {
				return d, time.Since(started)
			}
			if ctx.Err() != nil {
				t.Fatalf("database %s never settled: %+v %v", id, d, err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	started := time.Now()
	created, err := svc.Create(ctx, managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: devSpec()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.sched.Delete(context.Background(), created.ID) })
	database, _ := settle(created.ID, func(s managedpostgres.Spec) bool { return s == devSpec() })
	provisioned := time.Since(started)
	volume := filepath.Join(workdir, "drivervolumes", database.ProviderResourceID+".img")
	if fs := filesystemBytes(t, volume); fs != devSpec().StorageLimitBytes {
		t.Fatalf("new filesystem = %d bytes, want %d", fs, devSpec().StorageLimitBytes)
	}

	writer, err := st.p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: database.ProviderResourceID,
		IdentityKey: "storage-writer", IdempotencyKey: "storage-writer", Access: managedpostgres.CredentialMigration})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connect(ctx, writer)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE notes AS SELECT g AS id, repeat('x', 100) AS body FROM generate_series(1, 10000) g"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = conn.Close(ctx)
	rows := func() int {
		t.Helper()
		conn, err := connect(ctx, writer)
		if err != nil {
			t.Fatalf("login after resize: %v", err)
		}
		defer func() { _ = conn.Close(ctx) }()
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM notes").Scan(&n); err != nil {
			t.Fatalf("read after resize: %v", err)
		}
		return n
	}

	// A running database: the reconciler parks it, the volume grows and it
	// wakes on the new filesystem.
	if vm, err := st.sched.Get(ctx, database.ProviderResourceID); err != nil || vm.State != dbsched.StateRunning {
		t.Fatalf("before the resize the database is %+v, %v; want running", vm, err)
	}
	grown := int64(2 << 30)
	request := managedpostgres.ResizeDatabaseRequest{AccountID: account, DatabaseID: database.ID, RequestID: uuid.NewString(), TargetStorageLimitBytes: grown}
	if _, err := svc.Resize(ctx, request); err != nil {
		t.Fatal(err)
	}
	database, runningResize := settle(database.ID, func(s managedpostgres.Spec) bool { return s.StorageLimitBytes == grown })
	if op, err := svc.GetResize(ctx, account, database.ID, request.RequestID); err != nil || op.State != managedpostgres.ResizeSucceeded {
		t.Fatalf("resize receipt = %+v, %v", op, err)
	}
	if vm, err := st.sched.Get(ctx, database.ProviderResourceID); err != nil || vm.State != dbsched.StateRunning || vm.ResizePending {
		t.Fatalf("after a running resize the database is %+v, %v; want running", vm, err)
	}
	if fs := filesystemBytes(t, volume); fs != grown {
		t.Fatalf("filesystem after the running resize = %d bytes, want %d", fs, grown)
	}
	if n := rows(); n != 10000 {
		t.Fatalf("rows after the running resize = %d", n)
	}

	// A parked database grows offline and stays parked: nothing wakes it
	// for a storage change.
	if err := st.sched.Park(ctx, database.ProviderResourceID); err != nil {
		t.Fatal(err)
	}
	grown = 3 << 30
	request.RequestID, request.TargetStorageLimitBytes = uuid.NewString(), grown
	if _, err := svc.Resize(ctx, request); err != nil {
		t.Fatal(err)
	}
	database, parkedResize := settle(database.ID, func(s managedpostgres.Spec) bool { return s.StorageLimitBytes == grown })
	if vm, err := st.sched.Get(ctx, database.ProviderResourceID); err != nil || vm.State != dbsched.StateParked || vm.ResizePending {
		t.Fatalf("after a parked resize the database is %+v, %v; want parked", vm, err)
	}
	if fs := filesystemBytes(t, volume); fs != grown {
		t.Fatalf("filesystem after the parked resize = %d bytes, want %d", fs, grown)
	}
	woke := time.Now()
	if n := rows(); n != 10000 {
		t.Fatalf("rows after the parked resize = %d", n)
	}
	t.Logf("provision %v; running 1 -> 2 GiB %v; parked 2 -> 3 GiB %v; first query after %v",
		provisioned.Round(time.Millisecond), runningResize.Round(time.Millisecond), parkedResize.Round(time.Millisecond), time.Since(woke).Round(time.Millisecond))

	// Storage never shrinks.
	request.RequestID, request.TargetStorageLimitBytes = uuid.NewString(), 2<<30
	if _, err := svc.Resize(ctx, request); err == nil {
		t.Fatal("shrink accepted")
	}
}

// filesystemBytes reads the ext4 superblock of a volume image.
func filesystemBytes(t *testing.T, volume string) int64 {
	t.Helper()
	out, err := exec.Command("dumpe2fs", "-h", volume).Output() //nolint:gosec // test-owned path
	if err != nil {
		t.Fatalf("dumpe2fs %s: %v", volume, err)
	}
	n, err := dbvm.FilesystemBytes(out)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
