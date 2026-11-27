//go:build metal && linux

package dbsched

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbarchive"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMetalRegionalRecoveryUsesOnlyTheDestinationMirror(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT")
	}
	if base == "" {
		base = metalBase(t.Context(), t, workdir, guestInit, 17)
	}
	bucketRoot := t.TempDir()
	homeBucket := &dbarchive.FileRemote{Dir: filepath.Join(bucketRoot, "home")}
	mirror := &dbarchive.FileRemote{Dir: filepath.Join(bucketRoot, "mirror")}
	destinationBucket := &dbarchive.FileRemote{Dir: filepath.Join(bucketRoot, "destination")}
	bases, leases := map[int]string{17: base}, &topLeaser{}
	home := newMirroredMetalNode(t, workdir, bases, "reg-home", 20<<30, leases, homeBucket, mirror)
	destination := newMetalNodeWithArchives(t, workdir, bases, "reg-west", 20<<30, leases, destinationBucket, nil, mirror)
	destination.node.RecoveryRegion = "eu-west-1"
	vmm := NewGRPCVMMWithDial(nil, func(_ context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	sourceID, targetID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		for _, resource := range []struct {
			node *metalNode
			id   string
		}{{home, sourceID}, {destination, targetID}} {
			_ = resource.node.rt.Stop(context.Background(), resource.id)
			_ = resource.node.rt.DeleteVolume(context.Background(), resource.id)
		}
	})

	homeStore := NewMemStore()
	seedNodes(t, homeStore, home.node)
	homeScheduler := New(homeStore, vmm, Config{SharedArchive: true, ReadyTimeout: time.Minute}, nil)
	source, err := homeScheduler.Provision(ctx, ProvisionRequest{
		DatabaseID: sourceID, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if response, err := homeScheduler.Admin(ctx, sourceID, sql); err != nil || response.Error != "" {
			t.Fatalf("prepare recovery fixture: %+v, %v", response, err)
		}
	}
	query(ctx, t, home.rt, sourceID, password, "CREATE TABLE orders (label text); INSERT INTO orders VALUES ('known committed order')")
	point := time.Now().UTC()
	if err := homeScheduler.flushArchive(ctx, sourceID, point); err != nil {
		t.Fatal(err)
	}
	if _, err := (&dbarchive.Uploader{Store: home.archive, Remote: homeBucket, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := home.offsite.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	home.partition()
	if err := os.Rename(homeBucket.Dir, homeBucket.Dir+"-offline"); err != nil {
		t.Fatal(err)
	}

	store := NewMemStore()
	seedNodes(t, store, destination.node)
	sched := New(store, vmm, Config{SharedArchive: true, ReadyTimeout: time.Minute}, nil)
	check, err := sched.CheckRecoveryTarget(ctx, sourceID, destination.node.RecoveryRegion, time.Now().Add(-time.Millisecond), source.Major, true)
	if err != nil || check.Outcome != dbarchive.RestoreCovered || check.RecoveryPoint == nil || check.RecoveryPoint.Add(time.Millisecond).Before(point) {
		t.Fatalf("destination-only recovery evidence: %+v, %v", check, err)
	}
	request := restoreRequest(source, *check.RecoveryPoint)
	request.DatabaseID = targetID
	request.RecoveryRegion = destination.node.RecoveryRegion
	if vm, err := sched.Restore(ctx, request); err != nil || vm.NodeID != destination.node.ID {
		t.Fatalf("regional restore reserved %+v, %v", vm, err)
	}
	sched.restoreWG.Wait()
	if _, err := sched.Wake(ctx, targetID); err != nil {
		t.Fatal(err)
	}
	if got := query(ctx, t, destination.rt, targetID, password, "SELECT label FROM orders"); got != "known committed order" {
		t.Fatalf("application login read %q", got)
	}
}
