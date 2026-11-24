package diskpressure_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/diskpressure"
	"github.com/onebox-faas/faas/pkg/promql"
)

var now = time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

const (
	gb    = 1 << 30
	guest = "33333333-3333-4333-8333-333333333333"
	other = "44444444-4444-4444-8444-444444444444"
)

func row(v float64, kv ...string) promql.VectorSample {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return promql.VectorSample{Labels: l, Value: v}
}

// fs returns the label pairs of one compute-node mount.
func fs(node, mount, fstype string) []string {
	return []string{"job", "node-compute", "node", node, "mountpoint", mount, "device", "/dev/" + node + strings.ReplaceAll(mount, "/", "_"), "fstype", fstype}
}

func samples() map[string][]promql.VectorSample {
	cp := []string{"job", "node", "instance", "cp-1:9100", "mountpoint", "/", "device", "/dev/sda1", "fstype", "ext4"}
	return map[string][]promql.VectorSample{
		"fs_size": {
			row(100*gb, fs("fsn-2", "/", "ext4")...), row(400*gb, fs("fsn-2", "/srv/fc", "xfs")...),
			row(100*gb, fs("fsn-3", "/", "ext4")...), row(8*gb, fs("fsn-3", "/srv/fc/jail", "tmpfs")...),
			row(1*gb, fs("fsn-3", "/run/user/0", "tmpfs")...), row(50*gb, cp...),
		},
		"fs_avail": {
			row(4*gb, fs("fsn-2", "/", "ext4")...),       // 4% free: critical
			row(80*gb, fs("fsn-2", "/srv/fc", "xfs")...), // 20% free and falling fast: filling
			row(60*gb, fs("fsn-3", "/", "ext4")...),      // 60% free; inodes low
			row(6*gb, fs("fsn-3", "/srv/fc/jail", "tmpfs")...),
			row(5*gb, cp...), // 10% free: low
		},
		"fs_files":      {row(1000, fs("fsn-3", "/", "ext4")...), row(1000, fs("fsn-2", "/", "ext4")...)},
		"fs_files_free": {row(50, fs("fsn-3", "/", "ext4")...), row(900, fs("fsn-2", "/", "ext4")...)},
		"fs_avail_deriv": {
			row(-80*gb/(2*3600.0), fs("fsn-2", "/srv/fc", "xfs")...), // full in 2 h
			row(-1, fs("fsn-3", "/", "ext4")...),                     // years away: not shown
			row(5, cp...),                                            // growing free space
		},
		"guest_pressure": {row(2, "node", "fsn-2", "level", "near_full"), row(1, "node", "fsn-2", "level", "full")},
		"guest_top_ratio": {
			row(0.97, "node", "fsn-2", "exported_instance", guest, "instance", "10.0.0.5:9101"),
			row(0.5, "node", "fsn-3", "exported_instance", other),
			row(0.9, "node", "fsn-3", "exported_instance", "not-a-uuid"),
		},
		"guest_top_capacity": {row(2*gb, "node", "fsn-2", "exported_instance", guest, "instance", "10.0.0.5:9101")},
		"journal_bytes":      {row(3*gb, "node", "fsn-2")},
	}
}

func TestBuildGradesFilesystemsAndNodes(t *testing.T) {
	out := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{ByQuery: samples(), Source: diskpressure.SourceObserved},
		Lookup: func(id string) (diskpressure.GuestInfo, bool) {
			if id == guest {
				return diskpressure.GuestInfo{AppID: "app-1", AppSlug: "shop", AccountID: "acct-1"}, true
			}
			return diskpressure.GuestInfo{}, false
		}})
	states := map[string]string{}
	for _, f := range out.Filesystems {
		states[f.Node+" "+f.Mountpoint] = f.State
	}
	want := map[string]string{"fsn-2 /": "critical", "fsn-2 /srv/fc": "filling", "cp-1 /": "low", "fsn-3 /": "inodes_low", "fsn-3 /srv/fc/jail": "ok"}
	if len(states) != len(want) {
		t.Fatalf("filesystems = %v (other tmpfs must be dropped)", states)
	}
	for k, v := range want {
		if states[k] != v {
			t.Errorf("%s state = %q, want %q", k, states[k], v)
		}
	}
	if out.Filesystems[0].Mountpoint != "/" || out.Filesystems[0].Node != "fsn-2" || out.Filesystems[0].FreePercent != 4 {
		t.Fatalf("worst first = %+v", out.Filesystems[0])
	}
	var filling, jail, root3 = out.Filesystems[1], out.Filesystems[4], out.Filesystems[3]
	if filling.HoursToFull == nil || *filling.HoursToFull != 2 {
		t.Fatalf("filling = %+v", filling)
	}
	if !jail.JailTmpfs || jail.Mountpoint != "/srv/fc/jail" || root3.HoursToFull != nil || root3.InodesFreePercent == nil || *root3.InodesFreePercent != 5 {
		t.Fatalf("jail=%+v root3=%+v", jail, root3)
	}
	nodes := map[string]int{}
	for i, n := range out.Nodes {
		nodes[n.Node] = i
	}
	n2, n3, cp := out.Nodes[nodes["fsn-2"]], out.Nodes[nodes["fsn-3"]], out.Nodes[nodes["cp-1"]]
	if out.Nodes[0].Node != "fsn-2" || n2.State != "critical" || n2.Role != "compute" || n2.JournalBytes == nil || *n2.JournalBytes != 3*gb ||
		n2.GuestsNearFull != 2 || n2.GuestsFull != 1 || n2.WorstMountpoint != "/" {
		t.Fatalf("fsn-2 = %+v", n2)
	}
	if n3.State != "inodes_low" || n3.JailTmpfsUsedBytes != 2*gb || cp.Role != "control_plane" || cp.State != "low" || cp.JournalBytes != nil {
		t.Fatalf("fsn-3 = %+v cp = %+v", n3, cp)
	}
	tot := out.Totals
	if tot.Nodes != 3 || tot.NodesCritical != 1 || tot.NodesLow != 1 || tot.NodesInodesLow != 1 || tot.NodesFilling != 0 || tot.Filesystems != 5 ||
		tot.JailTmpfsUsedBytes != 2*gb || tot.GuestsNearFull != 2 || tot.GuestsFull != 1 {
		t.Fatalf("totals = %+v", tot)
	}
	if len(out.Guests) != 2 {
		t.Fatalf("guests = %+v (a label that is not an instance ID is dropped)", out.Guests)
	}
	g := out.Guests[0]
	if g.InstanceID != guest || g.UsedPercent != 97 || g.CapacityBytes != 2*gb || g.AppSlug != "shop" || g.AccountID != "acct-1" || g.Node != "fsn-2" {
		t.Fatalf("guest = %+v", g)
	}
	if out.Guests[1].CapacityBytes != 0 || out.Guests[1].AppID != "" {
		t.Fatalf("second guest = %+v", out.Guests[1])
	}
}

func TestBuildEmptyShape(t *testing.T) {
	out := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{Source: diskpressure.SourceNotConfigured}})
	if out.Source != "not_configured" || out.Nodes == nil || out.Filesystems == nil || out.Guests == nil || out.FailedQueries == nil ||
		out.CriticalFreePercent != 5 || out.FillingHorizonHours != 4 || out.GrowthWindowSeconds != 6*3600 {
		t.Fatalf("empty = %+v", out)
	}
}

type fakeProm struct {
	fail  string
	mu    sync.Mutex
	times map[time.Time]bool
}

func (f *fakeProm) QueryVectorAt(_ context.Context, q string, at time.Time) ([]promql.VectorSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.times == nil {
		f.times = map[time.Time]bool{}
	}
	f.times[at] = true
	if f.fail != "" && strings.Contains(q, f.fail) {
		return nil, errors.New("boom")
	}
	return nil, nil
}

func TestReadSources(t *testing.T) {
	ctx := context.Background()
	f := &fakeProm{}
	if got := diskpressure.ReadWith(ctx, f, now); got.Source != diskpressure.SourceObserved || len(f.times) != 1 || !f.times[now] {
		t.Fatalf("observed = %+v times=%v", got, f.times)
	}
	if got := diskpressure.ReadWith(ctx, &fakeProm{fail: "vmmd_journal"}, now); got.Source != diskpressure.SourcePartial || len(got.Failed) != 1 || got.Failed[0] != "journal_bytes" {
		t.Fatalf("partial = %+v", got)
	}
	if got := diskpressure.ReadWith(ctx, &fakeProm{fail: "_"}, now); got.Source != diskpressure.SourceUnavailable {
		t.Fatalf("unavailable = %+v", got)
	}
	if !strings.Contains(diskpressure.Queries()["fs_avail_deriv"], "[21600s]") {
		t.Fatal("growth window not in query")
	}
	if diskpressure.Read(ctx, nil, now).Source != diskpressure.SourceNotConfigured {
		t.Fatal("nil client should be not_configured")
	}
}
