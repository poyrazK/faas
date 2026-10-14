//go:build metal && linux

// adr: 1027 — a wake that adopts a spare: the database's volume replaces the
// spare's placeholder behind its drive, guest-init mounts it, and the
// database serves its data; the spare is gone when the database parks, and
// the pool is refilled.
package dbvm

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestMetalSpareAdoption(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the spare metal test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// The spare mode is this release's guest-init, so the base is built
	// with it, keyed as vmmd keys bases.
	digest, err := FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	base := BasePath(filepath.Join(workdir, "major-bases"), 17, PostgresImages[17], digest)
	if _, err := os.Stat(base); err != nil {
		if err := BuildBase(ctx, PostgresImages[17], guestInit, base); err != nil {
			t.Fatal(err)
		}
	}
	parent := "dbvm-spare-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	shape := SpareShape{Major: 17, VcpuCount: 1, MemSizeMiB: 384}
	leases := &fakeLeaser{}
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(workdir, "sparejail"), VolumeRoot: filepath.Join(workdir, "sparevolumes"),
		KernelPath: envOr("FAAS_DBVM_KERNEL", "/srv/fc/kernel/1.7.0"), Bases: map[int]string{17: base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: envOr("FAAS_FIRECRACKER", "firecracker"), Leases: leases,
		Spares: map[SpareShape]int{shape: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	id := "5c2f1e8a-7b3d-4c9e-a1f0-2d4e6b8c0a1e"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rt.Stop(context.Background(), id)
		rt.StopSpares(context.Background())
		_ = rt.DeleteVolume(context.Background(), id)
	})
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}

	// Provision with a cold boot: no spare is waiting yet.
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("spare-metal-test-app-password!!!"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return rt.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, rt, id, password)
	if _, err := conn.Exec(ctx, "create table kept(v text); insert into kept values ('across the spare')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}

	// A cold wake, for comparison.
	coldStarted := time.Now()
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	cold := time.Since(coldStarted)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}

	filled := time.Now()
	if n, err := rt.FillSpares(ctx); err != nil || n != 1 {
		t.Fatalf("fill = %d, %v", n, err)
	}
	spareBoot := time.Since(filled)
	if rt.waitingSpares(shape) != 1 {
		t.Fatal("no spare waiting")
	}

	// The wake adopts the spare.
	warmStarted := time.Now()
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("wake into the spare: %v", err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	warm := time.Since(warmStarted)
	vm, err := rt.running(id)
	if err != nil || vm.spareDir == "" {
		t.Fatalf("the wake did not adopt the spare: %+v %v", vm, err)
	}
	conn = connectRuntime(ctx, t, rt, id, password)
	var v string
	if err := conn.QueryRow(ctx, "select v from kept").Scan(&v); err != nil || v != "across the spare" {
		t.Fatalf("data in the adopted spare = %q, %v", v, err)
	}
	if _, err := conn.Exec(ctx, "insert into kept values ('written in the spare')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	spareDir := vm.spareDir
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spareDir); !os.IsNotExist(err) {
		t.Fatalf("the adopted spare's directory survived: %v", err)
	}
	if _, err := os.Stat(rt.VolumePath(id)); err != nil {
		t.Fatalf("volume after the spare parked: %v", err)
	}

	// The pool refills in the background; the next wake adopts again, and
	// the spare's writes are on the volume.
	deadline := time.Now().Add(time.Minute)
	for rt.waitingSpares(shape) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the pool never refilled")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	conn = connectRuntime(ctx, t, rt, id, password)
	var n int
	if err := conn.QueryRow(ctx, "select count(*) from kept").Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after a second adoption = %d, %v", n, err)
	}
	_ = conn.Close(ctx)
	t.Logf("cold wake to ready %v; spare boot %v; wake into a spare to ready %v",
		cold.Round(time.Millisecond), spareBoot.Round(time.Millisecond), warm.Round(time.Millisecond))
}
