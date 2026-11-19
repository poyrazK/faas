// adr: 20261118154632

package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
)

// borrowedNode is a fake node: a cgroupfs with the tenant and borrowed
// slices, and /proc/meminfo.
type borrowedNode struct {
	t    *testing.T
	root string
}

func newBorrowedNode(t *testing.T) borrowedNode {
	t.Helper()
	n := borrowedNode{t: t, root: t.TempDir()}
	for _, dir := range []string{builderslots.TenantSlice, builderslots.BorrowedSlice} {
		if err := os.MkdirAll(filepath.Join(n.root, "cg", dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A pin slice is a systemd child, never a VM.
	if err := os.MkdirAll(filepath.Join(n.root, "cg", builderslots.BorrowedSlice, "faas-borrowed-fence.slice"), 0o755); err != nil {
		t.Fatal(err)
	}
	n.memory(1_000, 10_000, 0, 8_000)
	return n
}

func (n borrowedNode) reader() builderslots.Reader {
	return builderslots.Reader{CgroupRoot: filepath.Join(n.root, "cg"), MemInfoPath: filepath.Join(n.root, "meminfo")}
}

func (n borrowedNode) write(rel, body string) {
	n.t.Helper()
	if err := os.WriteFile(filepath.Join(n.root, rel), []byte(body), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

// memory sets tenant memory.current and memory.max, borrowed memory.current
// and MemAvailable, in MiB.
func (n borrowedNode) memory(tenant, tenantMax, borrowed, available int64) {
	n.write(filepath.Join("cg", builderslots.TenantSlice, "memory.current"), fmt.Sprintf("%d\n", tenant<<20))
	n.write(filepath.Join("cg", builderslots.TenantSlice, "memory.max"), fmt.Sprintf("%d\n", tenantMax<<20))
	n.write(filepath.Join("cg", builderslots.BorrowedSlice, "memory.current"), fmt.Sprintf("%d\n", borrowed<<20))
	n.write("meminfo", fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", 16_000<<10, available<<10))
}

// builder adds a borrowed builder scope started at.
func (n borrowedNode) builder(instance string, at time.Time) {
	n.t.Helper()
	dir := filepath.Join(n.root, "cg", builderslots.BorrowedSlice, instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		n.t.Fatal(err)
	}
	if err := os.Chtimes(dir, at, at); err != nil {
		n.t.Fatal(err)
	}
}

func TestBorrowedBuildersForceGiveBack(t *testing.T) {
	node := newBorrowedNode(t)
	b := NewBorrowedBuilders(node.reader(), nil, nil)
	clock := time.Unix(1_800_000_000, 0)
	b.now = func() time.Time { return clock }
	var killed []string
	kill := func(_ context.Context, instance string) error {
		killed = append(killed, instance)
		return nil
	}
	tick := func(advance time.Duration) {
		clock = clock.Add(advance)
		b.tick(context.Background(), kill)
	}

	// No borrowed builder: nothing to give back, whatever the memory says.
	node.memory(9_900, 10_000, 0, 100)
	tick(time.Second)
	tick(time.Second)
	if len(killed) != 0 {
		t.Fatalf("killed %v with no borrowed builder", killed)
	}

	node.builder("build-old", clock.Add(-time.Minute))
	node.builder("build-new", clock.Add(-time.Second))

	// Below the force watermark nothing happens, however long it lasts.
	node.memory(5_000, 10_000, 3_000, 4_000)
	for i := 0; i < 8; i++ {
		tick(api.BuilderBorrowCheckInterval)
	}
	if len(killed) != 0 {
		t.Fatalf("killed %v at 80%% of the tenant fence", killed)
	}

	// Above it, vmmd waits for the condition to hold for the sustain.
	node.memory(6_500, 10_000, 3_000, 2_000)
	tick(api.BuilderBorrowCheckInterval)
	tick(api.BuilderBorrowForceSustain / 2)
	if len(killed) != 0 {
		t.Fatalf("killed %v before the force condition held for %s", killed, api.BuilderBorrowForceSustain)
	}
	tick(api.BuilderBorrowForceSustain)
	if len(killed) != 1 || killed[0] != "build-new" {
		t.Fatalf("killed %v, want the newest borrowed builder build-new", killed)
	}
	if !b.TakePreempted("build-new") || b.TakePreempted("build-new") {
		t.Fatal("TakePreempted must report the forced builder exactly once")
	}

	// The next kill needs another sustained reading, and skips a builder
	// already given back whose scope is still on disk.
	b.preempted["build-new"] = true
	tick(api.BuilderBorrowCheckInterval)
	if len(killed) != 1 {
		t.Fatalf("killed %v without a fresh sustained reading", killed)
	}
	tick(api.BuilderBorrowForceSustain)
	if len(killed) != 2 || killed[1] != "build-old" {
		t.Fatalf("killed %v, want build-old next", killed)
	}

	// Pressure that clears resets the sustain.
	node.builder("build-third", clock)
	tick(api.BuilderBorrowCheckInterval)
	node.memory(1_000, 10_000, 3_000, 8_000)
	tick(api.BuilderBorrowCheckInterval)
	node.memory(6_500, 10_000, 3_000, 2_000)
	tick(api.BuilderBorrowForceSustain / 2)
	if len(killed) != 2 {
		t.Fatalf("killed %v though pressure cleared in between", killed)
	}
}

func TestBorrowedBuildersHostCriticalAndUnreadable(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(borrowedNode)
	}{
		{name: "host critical", setup: func(n borrowedNode) { n.memory(1_000, 10_000, 3_000, api.BuilderBorrowHostCriticalMB-1) }},
		{name: "unreadable meminfo", setup: func(n borrowedNode) { _ = os.Remove(filepath.Join(n.root, "meminfo")) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			node := newBorrowedNode(t)
			b := NewBorrowedBuilders(node.reader(), nil, nil)
			clock := time.Unix(1_800_000_000, 0)
			b.now = func() time.Time { return clock }
			node.builder("build-a", clock)
			tt.setup(node)
			var killed []string
			kill := func(_ context.Context, instance string) error { killed = append(killed, instance); return nil }
			b.tick(context.Background(), kill)
			clock = clock.Add(api.BuilderBorrowForceSustain)
			b.tick(context.Background(), kill)
			if len(killed) != 1 {
				t.Fatalf("killed %v, want build-a", killed)
			}
		})
	}
}

func TestBorrowedBuildersFailedKillIsRetried(t *testing.T) {
	node := newBorrowedNode(t)
	b := NewBorrowedBuilders(node.reader(), nil, nil)
	clock := time.Unix(1_800_000_000, 0)
	b.now = func() time.Time { return clock }
	node.builder("build-a", clock)
	node.memory(6_500, 10_000, 3_000, 2_000)
	calls := 0
	kill := func(context.Context, string) error {
		calls++
		if calls == 1 {
			return errors.New("instance busy")
		}
		return nil
	}
	for i := 0; i < 4; i++ {
		b.tick(context.Background(), kill)
		clock = clock.Add(api.BuilderBorrowForceSustain)
	}
	if calls != 2 || !b.TakePreempted("build-a") {
		t.Fatalf("kill calls %d, preempted %v; want a retry that sticks", calls, b.preempted)
	}
}

func TestServerBorrowedBuilderCapability(t *testing.T) {
	node := newBorrowedNode(t)
	borrowed := NewBorrowedBuilders(node.reader(), nil, nil)
	server := New(vmmStubBase{}, nil, "1.10.0", nil).WithBorrowedBuilders(borrowed)
	ctx := context.Background()

	ping := func() bool {
		t.Helper()
		resp, err := server.Ping(ctx, &vmmdpb.PingRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetSupportsBorrowedBuilders()
	}
	coldBoot := func() error {
		_, err := server.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{
			Instance: "build-x",
			Build:    &vmmdpb.BuildSpec{ExportDir: t.TempDir(), Borrowed: true},
		})
		return err
	}

	if ping() {
		t.Fatal("Ping reports borrowed builders while builder_elastic_slots is off")
	}
	if err := coldBoot(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("borrowed cold boot with the flag off: %v, want FailedPrecondition", err)
	}
	borrowed.SetEnabled(true)
	if !ping() {
		t.Fatal("Ping hides borrowed builders with the flag on and the slice present")
	}
	if err := os.RemoveAll(filepath.Join(node.root, "cg", builderslots.BorrowedSlice)); err != nil {
		t.Fatal(err)
	}
	if ping() {
		t.Fatal("Ping reports borrowed builders without faas-borrowed.slice")
	}
	if err := coldBoot(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("borrowed cold boot without the slice: %v, want FailedPrecondition", err)
	}

	// Destroy reports a builder vmmd gave back, once.
	borrowed.preempted["build-x"] = true
	resp, err := server.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: "build-x"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetPreemptedForTenants() {
		t.Fatal("Destroy hides that vmmd gave the builder back")
	}
	resp, err = server.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: "build-x"})
	if err != nil || resp.GetPreemptedForTenants() {
		t.Fatalf("second Destroy preempted = %v, %v; want false", resp.GetPreemptedForTenants(), err)
	}

	// A server without the component runs no borrowed builders.
	plain := New(vmmStubBase{}, nil, "1.10.0", nil)
	if resp, err := plain.Ping(ctx, &vmmdpb.PingRequest{}); err != nil || resp.GetSupportsBorrowedBuilders() {
		t.Fatalf("plain Ping = %v, %v", resp.GetSupportsBorrowedBuilders(), err)
	}
}
