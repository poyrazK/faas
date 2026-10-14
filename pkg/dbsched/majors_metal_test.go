//go:build metal && linux

// adr: 1027 — every qualified PostgreSQL major runs the same way: a base is
// built from its pinned image with this release's guest-init, and a
// database of that major provisions, takes writes, backs up and restores to
// a point in time.
package dbsched

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

func TestMetalPostgresMajors(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the majors metal test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	initDigest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	majors := make([]int, 0, len(dbvm.PostgresImages))
	for major := range dbvm.PostgresImages {
		majors = append(majors, major)
	}
	slices.Sort(majors)
	if only := os.Getenv("FAAS_DBVM_MAJORS"); only != "" {
		n, err := strconv.Atoi(only)
		if err != nil {
			t.Fatal(err)
		}
		majors = []int{n}
	}
	// Bases are keyed by image and guest-init, as vmmd keys them, and kept
	// between runs.
	baseDir := filepath.Join(workdir, "major-bases")
	bases := map[int]string{}
	for _, major := range majors {
		image := dbvm.PostgresImages[major]
		path := dbvm.BasePath(baseDir, major, image, initDigest)
		if _, err := os.Stat(path); err != nil {
			started := time.Now()
			if err := dbvm.BuildBase(ctx, image, guestInit, path); err != nil {
				t.Fatalf("build the PostgreSQL %d base: %v", major, err)
			}
			t.Logf("built the PostgreSQL %d base in %v", major, time.Since(started).Round(time.Millisecond))
		}
		bases[major] = path
	}

	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "majbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	node := newMetalNode(t, workdir, bases, "pm", 40<<30, &topLeaser{}, bucket)
	node.node.Majors = majors
	store := NewMemStore()
	seedNodes(t, store, node.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: 2 * time.Minute}, nil)

	for _, major := range majors {
		t.Run(fmt.Sprintf("postgres-%d", major), func(t *testing.T) {
			id, restored := uuid.NewString(), uuid.NewString()
			t.Cleanup(func() {
				cleanup := context.WithoutCancel(ctx)
				_ = s.Delete(cleanup, restored)
				_ = s.Delete(cleanup, id)
			})
			started := time.Now()
			vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: major, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600})
			if err != nil || vm.State != StateParked || vm.LastBackupAt.IsZero() {
				t.Fatalf("provision = %+v, %v; want parked with its first base backup", vm, err)
			}
			provisioned := time.Since(started)
			password := uuid.NewString()
			for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
				if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
					t.Fatalf("admin: %+v %v", resp, err)
				}
			}
			if got := query(ctx, t, node.rt, id, password, "SELECT current_setting('server_version_num')"); got[:len(strconv.Itoa(major))] != strconv.Itoa(major) {
				t.Fatalf("server_version_num = %s, want major %d", got, major)
			}
			query(ctx, t, node.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept VALUES ('kept')")
			time.Sleep(10 * time.Millisecond)
			target := time.Now()
			time.Sleep(10 * time.Millisecond)
			query(ctx, t, node.rt, id, password, "INSERT INTO kept VALUES ('after the target')")

			began := time.Now()
			vm, err = s.Restore(ctx, RestoreRequest{
				ProvisionRequest: ProvisionRequest{DatabaseID: restored, Major: major, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600},
				Source:           id, Target: target,
			})
			if err != nil {
				t.Fatal(err)
			}
			for vm.State == StateProvisioning {
				if time.Since(began) > 5*time.Minute {
					t.Fatal("restore never finished")
				}
				time.Sleep(200 * time.Millisecond)
				vm, _ = store.Get(ctx, restored)
			}
			if vm.State != StateParked {
				t.Fatalf("restore = %+v", vm)
			}
			if _, err := s.Wake(ctx, restored); err != nil {
				t.Fatal(err)
			}
			if got := query(ctx, t, node.rt, restored, password, "SELECT string_agg(v, ',') FROM kept"); got != "kept" {
				t.Fatalf("restored rows = %q, want only the row before the target", got)
			}
			t.Logf("PostgreSQL %d: provision %v, point-in-time restore %v", major, provisioned.Round(time.Millisecond), time.Since(began).Round(time.Millisecond))
		})
	}
}
