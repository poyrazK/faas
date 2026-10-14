//go:build metal && linux

// adr: 1027 — production class: a standby on a second runtime seeds itself
// from the primary through the replication relay, streams synchronously,
// releases a blocked commit when the host turns synchronous mode off, and
// takes over without losing an acknowledged commit when the primary dies.
package dbvm

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// offsetLeaser keeps a second runtime's jail uids and vsock CIDs apart
// from the first's on one host, as two nodes' allocators would be.
type offsetLeaser struct {
	fakeLeaser
	offset int
}

func (o *offsetLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l, err := o.fakeLeaser.Acquire(instance)
	l.Slot += o.offset
	l.UID -= o.offset
	l.GID -= o.offset
	return l, err
}

func metalNodeRuntime(t *testing.T, env metalEnv, name string, leases Leaser) *Runtime {
	t.Helper()
	parent := "dbvm-repl-" + name
	parentDir := filepath.Join("/sys/fs/cgroup", parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(parentDir) })
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "rj"+name), VolumeRoot: filepath.Join(env.workdir, "repl-volumes-"+name),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: leases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	return rt
}

func replicationStatus(ctx context.Context, t *testing.T, rt *Runtime, id string, mode dbguest.ReplicationMode) dbguest.ReplicationStatus {
	t.Helper()
	resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpReplication, Replication: mode})
	if err != nil || resp.Error != "" || resp.Replication == nil {
		t.Fatalf("replication %q: %+v %v", mode, resp, err)
	}
	return *resp.Replication
}

func awaitSynchronous(ctx context.Context, t *testing.T, rt *Runtime, id string) time.Duration {
	t.Helper()
	started := time.Now()
	replicationStatus(ctx, t, rt, id, dbguest.ReplicationSync)
	for {
		if status := replicationStatus(ctx, t, rt, id, ""); status.Synchronous {
			return time.Since(started)
		}
		select {
		case <-ctx.Done():
			t.Fatal("the standby never became synchronous")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestMetalStandbyReplicationAndFailover(t *testing.T) {
	env := loadMetalEnv(t)
	primary := metalNodeRuntime(t, env, "a", &fakeLeaser{})
	standby := metalNodeRuntime(t, env, "b", &offsetLeaser{offset: 5})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := "3c9e1a7b-5d2f-4e8a-b6c4-9f0e1d2c3b4a"
	for _, rt := range []*Runtime{primary, standby} {
		_ = rt.DeleteVolume(ctx, id)
		if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	}
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}
	if err := primary.Start(ctx, boot); err != nil {
		t.Fatalf("start primary: %v", err)
	}
	if resp, err := primary.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := primary.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("replication-metal-test-password!"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return primary.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, primary, id, password)
	if _, err := conn.Exec(ctx, "create table rows(v text); insert into rows select 'seeded-' || g from generate_series(1, 20000) g"); err != nil {
		t.Fatal(err)
	}
	if status := replicationStatus(ctx, t, primary, id, dbguest.ReplicationAsync); status.Mode != dbguest.ReplicationAsync || status.Streaming {
		t.Fatalf("before a standby: %+v", status)
	}

	// The standby seeds itself from the primary over the relay.
	standbyBoot := boot
	standbyBoot.Primary = func(ctx context.Context) (net.Conn, error) { return primary.DialReplication(ctx, id) }
	seedStarted := time.Now()
	if err := standby.Start(ctx, standbyBoot); err != nil {
		t.Fatalf("start standby: %v", err)
	}
	if _, err := standby.WaitState(ctx, id, dbguest.StateStandby); err != nil {
		t.Fatalf("standby never caught up: %v", err)
	}
	seed := time.Since(seedStarted)
	if err := connectAttempt(ctx, standby, id, password); err == nil {
		t.Fatal("a standby served a client session")
	}
	toSync := awaitSynchronous(ctx, t, primary, id)
	if _, err := conn.Exec(ctx, "insert into rows values ('sync-1')"); err != nil {
		t.Fatal(err)
	}

	// Without its standby a synchronous primary holds commits; turning
	// synchronous mode off releases them.
	if err := standby.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	committed := make(chan error, 1)
	go func() {
		_, err := conn.Exec(context.WithoutCancel(ctx), "insert into rows values ('held')")
		committed <- err
	}()
	select {
	case err := <-committed:
		t.Fatalf("a synchronous commit finished without its standby: %v", err)
	case <-time.After(2 * time.Second):
	}
	if status := replicationStatus(ctx, t, primary, id, dbguest.ReplicationAsync); status.Mode != dbguest.ReplicationAsync {
		t.Fatalf("degrade: %+v", status)
	}
	select {
	case err := <-committed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("turning synchronous mode off did not release the held commit")
	}

	// A seeded standby resumes from its own volume and catches up.
	resumeStarted := time.Now()
	if err := standby.Start(ctx, standbyBoot); err != nil {
		t.Fatalf("restart standby: %v", err)
	}
	if _, err := standby.WaitState(ctx, id, dbguest.StateStandby); err != nil {
		t.Fatal(err)
	}
	resume := time.Since(resumeStarted)
	awaitSynchronous(ctx, t, primary, id)
	if _, err := conn.Exec(ctx, "insert into rows values ('sync-last')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	// The primary's node dies: no clean shutdown, nothing more shipped.
	killVM(t, primary, id)
	promoteStarted := time.Now()
	resp, err := standby.Control(ctx, id, dbguest.Request{Op: dbguest.OpPromote})
	if err != nil || resp.Error != "" || resp.State != dbguest.StateReady {
		t.Fatalf("promote: %+v %v", resp, err)
	}
	promote := time.Since(promoteStarted)
	conn = connectRuntime(ctx, t, standby, id, password)
	var count int
	var last, walFile string
	if err := conn.QueryRow(ctx, "select count(*), max(v) filter (where v like 'sync-%') from rows").Scan(&count, &last); err != nil {
		t.Fatal(err)
	}
	if count != 20003 || last != "sync-last" {
		t.Fatalf("after failover: %d rows, last synchronous %q; want 20003 and sync-last", count, last)
	}
	if _, err := conn.Exec(ctx, "insert into rows values ('after-failover')"); err != nil {
		t.Fatalf("write after promotion: %v", err)
	}
	if err := conn.QueryRow(ctx, "select pg_walfile_name(pg_current_wal_lsn())").Scan(&walFile); err != nil || !strings.HasPrefix(walFile, "00000002") {
		t.Fatalf("promoted onto WAL file %q (%v); want timeline 2", walFile, err)
	}
	_ = conn.Close(ctx)
	if status := replicationStatus(ctx, t, standby, id, ""); status.Mode == dbguest.ReplicationSync {
		t.Fatalf("a promoted standby waits for a standby it does not have: %+v", status)
	}
	if err := standby.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed to standby %v; to synchronous %v; standby resume %v; promote %v",
		seed.Round(time.Millisecond), toSync.Round(time.Millisecond), resume.Round(time.Millisecond), promote.Round(time.Millisecond))
}

// connectAttempt reports the error of a session to a VM, nil if it
// connected.
func connectAttempt(ctx context.Context, rt *Runtime, id, password string) error {
	cfg, err := pgx.ParseConfig("host=dbvm user=app dbname=app sslmode=disable connect_timeout=5 password=" + password)
	if err != nil {
		return err
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return rt.Dial(ctx, id) }
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	_ = conn.Close(ctx)
	return nil
}

// killVM ends a VM the way a lost node does: its Firecracker is killed,
// and PostgreSQL gets no shutdown.
func killVM(t *testing.T, rt *Runtime, id string) {
	t.Helper()
	vm, err := rt.running(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	<-vm.exited
	rt.teardown(id, vm)
}
