//go:build metal && linux

package dbsched

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestMetalRollingMaintenanceKeepsAcknowledgedWrites(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT")
	}
	store := storeFactories(t)["pg"](t).(*PgStore)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	if base == "" {
		base = metalBase(ctx, t, workdir, guestInit, 17)
	}
	base, err := filepath.Abs(base)
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "refreshed.ext4")
	if err := os.Symlink(base, current); err != nil {
		t.Fatal(err)
	}
	bucket := &dbarchive.FileRemote{Dir: t.TempDir()}
	a := newMetalNode(t, workdir, map[int]string{17: base}, "ra", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 120}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "rb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 140}, bucket)
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(_ context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, SharedArchive: true, StandbySeedTimeout: 5 * time.Minute,
		StandbyResumeTimeout: time.Minute, ReadyTimeout: time.Minute}, nil)
	t.Cleanup(s.standbyWG.Wait)
	id, password := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30,
		AlwaysOn: true, Replicated: true, RestoreWindowSeconds: 3600})
	if err != nil || vm.NodeID != a.node.ID || vm.StandbyNode != b.node.ID {
		t.Fatalf("provision: %+v, %v", vm, err)
	}
	for _, statement := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, statement); err != nil || resp.Error != "" {
			t.Fatalf("create writer role/database: %+v, %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	query(ctx, t, a.rt, id, password, "CREATE TABLE rolling_commits (n bigint PRIMARY KEY)")
	registerRollingFixtureDatabase(t, store, vm)
	setRollingFixtureWindow(t, store, vm, -5*time.Minute)

	cfg, err := pgx.ParseConfig("host=orders user=app password=" + password + " dbname=app sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	runtimes := map[string]*dbvm.Runtime{a.node.ID: a.rt, b.node.ID: b.rt}
	cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		vm, err := s.Wake(ctx, id)
		if err != nil {
			return nil, err
		}
		return runtimes[vm.NodeID].Dial(ctx, id)
	}
	var acknowledged atomic.Int64
	writeCtx, stopWrites := context.WithCancel(ctx)
	writesDone := make(chan struct{})
	go func() {
		defer close(writesDone)
		for writeCtx.Err() == nil {
			write, cancel := context.WithTimeout(writeCtx, 4*time.Second)
			conn, err := pgx.ConnectConfig(write, cfg)
			if err == nil {
				_, err = conn.Exec(write, "INSERT INTO rolling_commits VALUES ($1) ON CONFLICT DO NOTHING", acknowledged.Load()+1)
				_ = conn.Close(write)
			}
			cancel()
			if err == nil {
				acknowledged.Add(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	defer func() { stopWrites(); <-writesDone }()
	waitFor := func(done func() bool) {
		t.Helper()
		for !done() {
			select {
			case <-ctx.Done():
				t.Fatal("timed out waiting for rolling maintenance")
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	waitFor(func() bool { return acknowledged.Load() >= 5 })
	a.rt.SetBase(ctx, 17, current)
	b.rt.SetBase(ctx, 17, current)
	if _, err := s.SampleStorage(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool {
		s.resumeRollingRefreshes(ctx, store)
		_, outcome, reason := rollingFixtureOutcome(t, store, vm)
		if outcome != "" && outcome != "succeeded" {
			t.Fatalf("rolling maintenance finished %s: %s", outcome, reason)
		}
		return outcome == "succeeded"
	})
	afterMaintenance := acknowledged.Load()
	waitFor(func() bool { return acknowledged.Load() >= afterMaintenance+5 })
	stopWrites()
	<-writesDone

	after, err := s.Get(ctx, id)
	if err != nil || after.NodeID != b.node.ID || !after.StandbySynced {
		t.Fatalf("after maintenance: %+v, %v", after, err)
	}
	last := acknowledged.Load()
	stmt := fmt.Sprintf("SELECT count(*)::text FROM rolling_commits WHERE n BETWEEN 1 AND %d", last)
	if got := query(ctx, t, b.rt, id, password, stmt); got != fmt.Sprint(last) {
		t.Fatalf("%s of %d acknowledged inserts survived", got, last)
	}
}
