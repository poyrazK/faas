package diskpressure_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	// The control plane's static target scrapes loopback and has no node label.
	cp := []string{"job", "node", "instance", "127.0.0.1:9100", "mountpoint", "/", "device", "/dev/sda1", "fstype", "ext4"}
	return map[string][]promql.VectorSample{
		"fs_size": {
			row(100*gb, fs("fsn-2", "/", "ext4")...), row(400*gb, fs("fsn-2", "/srv/fc", "xfs")...),
			row(100*gb, fs("fsn-3", "/", "ext4")...), row(8*gb, fs("fsn-3", "/srv/fc/jail", "tmpfs")...),
			row(1*gb, fs("fsn-3", "/run/user/0", "tmpfs")...), row(50*gb, cp...),
		},
		"fs_avail": {
			row(4*gb, fs("fsn-2", "/", "ext4")...),                // 4% free: critical
			row(80*gb, fs("fsn-2", "/srv/fc", "xfs")...),          // 20% free and falling fast: filling
			row(60*gb, fs("fsn-3", "/", "ext4")...),               // 60% free; inodes low
			row(0.25*gb, fs("fsn-3", "/srv/fc/jail", "tmpfs")...), // 3% free RAM: not a disk state
			row(5*gb, cp...), // 10% free: low
		},
		"fs_files":      {row(1000, fs("fsn-3", "/", "ext4")...), row(1000, fs("fsn-2", "/", "ext4")...)},
		"fs_files_free": {row(50, fs("fsn-3", "/", "ext4")...), row(900, fs("fsn-2", "/", "ext4")...)},
		"fs_avail_deriv": {
			row(-80*gb/(2*3600.0), fs("fsn-2", "/srv/fc", "xfs")...), // full in 2 h
			row(-1, fs("fsn-3", "/", "ext4")...),                     // years away: not shown
			row(5, cp...),                                            // growing free space
		},
		"guest_pressure": {
			row(2, "node", "fsn-2", "job", "vmmd", "level", "near_full"), row(1, "node", "fsn-2", "job", "vmmd", "level", "full"),
			row(4, "job", "vmmd", "level", "full"), // no node label: skipped
		},
		"guest_top_ratio": {
			row(0.97, "node", "fsn-2", "exported_instance", guest, "instance", "10.0.0.5:9101"),
			row(0.5, "node", "fsn-3", "exported_instance", other),
			row(0.9, "node", "fsn-3", "exported_instance", "not-a-uuid"),
		},
		"guest_top_capacity": {row(2*gb, "node", "fsn-2", "exported_instance", guest, "instance", "10.0.0.5:9101")},
		// db-1 is seen only through vmmd: its disks were not read.
		"journal_bytes": {row(3*gb, "node", "fsn-2", "job", "vmmd"), row(1*gb, "node", "db-1", "job", "vmmd-database")},
	}
}

func free(f api.ObsDiskFilesystem) float64 {
	if f.FreePercent == nil {
		return -1
	}
	return *f.FreePercent
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
	want := map[string]string{"fsn-2 /": "critical", "fsn-2 /srv/fc": "filling", "control-plane /": "low", "fsn-3 /": "inodes_low", "fsn-3 /srv/fc/jail": "ram"}
	if len(states) != len(want) {
		t.Fatalf("filesystems = %v (other tmpfs must be dropped)", states)
	}
	for k, v := range want {
		if states[k] != v {
			t.Errorf("%s state = %q, want %q", k, states[k], v)
		}
	}
	if out.Filesystems[0].Mountpoint != "/" || out.Filesystems[0].Node != "fsn-2" || free(out.Filesystems[0]) != 4 {
		t.Fatalf("worst first = %+v", out.Filesystems[0])
	}
	filling, root3, jail := out.Filesystems[1], out.Filesystems[3], out.Filesystems[4]
	if filling.HoursToFull == nil || *filling.HoursToFull != 2 {
		t.Fatalf("filling = %+v", filling)
	}
	if !jail.JailTmpfs || jail.Mountpoint != "/srv/fc/jail" || root3.HoursToFull != nil || root3.InodesFreePercent == nil || *root3.InodesFreePercent != 5 {
		t.Fatalf("jail=%+v root3=%+v", jail, root3)
	}
	nodes := map[string]api.ObsDiskNode{}
	for _, n := range out.Nodes {
		nodes[n.Node] = n
	}
	if len(nodes) != 4 {
		t.Fatalf("nodes = %+v (a vmmd row without a node label must be skipped)", out.Nodes)
	}
	n2, n3, cp, db := nodes["fsn-2"], nodes["fsn-3"], nodes["control-plane"], nodes["db-1"]
	if out.Nodes[0].Node != "fsn-2" || n2.State != "critical" || n2.Role != "compute" || n2.JournalBytes == nil || *n2.JournalBytes != 3*gb ||
		n2.GuestsNearFull != 2 || n2.GuestsFull != 1 || n2.WorstMountpoint != "/" {
		t.Fatalf("fsn-2 = %+v", n2)
	}
	// The jail tmpfs at 3% free is RAM: it neither makes fsn-3 critical nor
	// becomes its fullest mount.
	if n3.State != "inodes_low" || n3.Filesystems != 1 || n3.WorstMountpoint != "/" || n3.JailTmpfsUsedBytes != 7.75*gb {
		t.Fatalf("fsn-3 = %+v", n3)
	}
	if cp.Role != "control_plane" || cp.State != "low" || cp.JournalBytes != nil {
		t.Fatalf("control plane = %+v", cp)
	}
	if db.State != "unknown" || db.Role != "database" || db.Filesystems != 0 {
		t.Fatalf("vmmd-only node = %+v", db)
	}
	tot := out.Totals
	if tot.Nodes != 4 || tot.NodesCritical != 1 || tot.NodesLow != 1 || tot.NodesInodesLow != 1 || tot.NodesUnknown != 1 || tot.NodesFilling != 0 ||
		tot.Filesystems != 5 || tot.JailTmpfsUsedBytes != 7.75*gb || tot.GuestsNearFull != 2 || tot.GuestsFull != 1 {
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

// A failed free-space read leaves every filesystem unknown, never 0% free
// and critical.
func TestMissingFreeSpaceIsUnknown(t *testing.T) {
	s := samples()
	delete(s, "fs_avail")
	out := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{ByQuery: s, Source: diskpressure.SourcePartial, Failed: []string{"fs_avail"}}})
	for _, f := range out.Filesystems {
		if (f.State != "unknown" && !f.JailTmpfs) || f.AvailBytes != nil || f.FreePercent != nil || f.HoursToFull != nil {
			t.Fatalf("filesystem = %+v", f)
		}
	}
	if out.Totals.NodesCritical != 0 || out.Totals.NodesUnknown != out.Totals.Nodes {
		t.Fatalf("totals = %+v", out.Totals)
	}
	for _, n := range out.Nodes {
		if n.State != "unknown" || n.WorstMountpoint != "" {
			t.Fatalf("node = %+v", n)
		}
	}
}

// The production layout, as read from the OVH fleet's Prometheus on
// 2026-11-26: faas-fc-crypt is bind-mounted at /srv/fc and /var/lib/faas, and
// node_exporter cannot statfs the mode-1700 jail, so the jail comes from
// vmmd's gauges.
func TestProductionLayout(t *testing.T) {
	crypt := func(node, mount string) []string {
		return []string{"job", "node-compute", "node", node, "instance", "bhs.example:9100", "mountpoint", mount, "device", "/dev/mapper/faas-fc-crypt", "fstype", "xfs"}
	}
	jail := []string{"job", "node-compute", "node", "bhs-3.faas", "mountpoint", "/srv/fc/jail", "device", "tmpfs", "fstype", "tmpfs"}
	s := map[string][]promql.VectorSample{
		"fs_size": {
			row(900*gb, crypt("bhs-2.faas", "/var/lib/faas")...), row(900*gb, crypt("bhs-2.faas", "/srv/fc")...),
			row(900*gb, crypt("bhs-3.faas", "/srv/fc")...), row(12*gb, jail...),
		},
		"fs_avail": {
			row(700*gb, crypt("bhs-2.faas", "/var/lib/faas")...), row(700*gb, crypt("bhs-2.faas", "/srv/fc")...),
			row(800*gb, crypt("bhs-3.faas", "/srv/fc")...), row(11*gb, jail...),
		},
		// bhs-2's jail is only in vmmd; bhs-3's node_exporter reads its jail,
		// which wins over vmmd's; a row without a node is dropped.
		"jail_size":  {row(12*gb, "node", "bhs-2.faas", "job", "vmmd"), row(12*gb, "node", "bhs-3.faas", "job", "vmmd"), row(12*gb, "job", "vmmd")},
		"jail_avail": {row(10*gb, "node", "bhs-2.faas", "job", "vmmd"), row(2*gb, "node", "bhs-3.faas", "job", "vmmd")},
	}
	out := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{ByQuery: s, Source: diskpressure.SourceObserved}})
	got := map[string]api.ObsDiskFilesystem{}
	for _, f := range out.Filesystems {
		got[f.Node+" "+f.Mountpoint] = f
	}
	if len(got) != 4 || out.Totals.Filesystems != 4 {
		t.Fatalf("filesystems = %v (a bind mount is one filesystem)", got)
	}
	if f, ok := got["bhs-2.faas /srv/fc"]; !ok || f.State != "ok" || free(f) != 77.8 {
		t.Fatalf("bhs-2 /srv/fc = %+v (the shortest mountpoint names the device)", f)
	}
	j2 := got["bhs-2.faas /srv/fc/jail"]
	if !j2.JailTmpfs || j2.State != "ram" || j2.Role != "compute" || j2.SizeBytes != 12*gb || j2.AvailBytes == nil || *j2.AvailBytes != 10*gb {
		t.Fatalf("vmmd jail = %+v", j2)
	}
	if j3 := got["bhs-3.faas /srv/fc/jail"]; j3.AvailBytes == nil || *j3.AvailBytes != 11*gb {
		t.Fatalf("node_exporter jail = %+v (it wins over vmmd)", j3)
	}
	if out.Totals.JailTmpfsUsedBytes != 3*gb {
		t.Fatalf("jail used = %d, want 3 GiB", out.Totals.JailTmpfsUsedBytes)
	}
	for _, n := range out.Nodes {
		if n.Filesystems != 1 || n.State != "ok" {
			t.Fatalf("node = %+v (the jail is not a disk)", n)
		}
	}
}

// The grade uses unrounded ratios: 4.96% free is critical, as the alert
// pages, although it rounds to 5.0%.
func TestGradeMatchesAlertsAtThresholds(t *testing.T) {
	mk := func(availGB float64, mount string) map[string][]promql.VectorSample {
		return map[string][]promql.VectorSample{
			"fs_size":  {row(100*gb, fs("fsn-9", mount, "ext4")...)},
			"fs_avail": {row(availGB*gb, fs("fsn-9", mount, "ext4")...)},
		}
	}
	cases := []struct {
		avail float64
		state string
		pct   float64
	}{{4.96, "critical", 5}, {5.0, "low", 5}, {14.96, "low", 15}, {15.0, "ok", 15}}
	for _, c := range cases {
		out := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{ByQuery: mk(c.avail, "/")}})
		if f := out.Filesystems[0]; f.State != c.state || free(f) != c.pct {
			t.Errorf("avail %.2f%%: state %q free %v, want %q %v", c.avail, f.State, free(f), c.state, c.pct)
		}
	}
	// 4.04 h to full rounds to 4.0 h but is outside the 4 h horizon.
	s := mk(20, "/data")
	s["fs_avail_deriv"] = []promql.VectorSample{row(-20*gb/(4.04*3600), fs("fsn-9", "/data", "ext4")...)}
	f := diskpressure.Build(diskpressure.Input{Now: now, Samples: diskpressure.Samples{ByQuery: s}}).Filesystems[0]
	if f.State != "ok" || f.HoursToFull == nil || *f.HoursToFull != 4 {
		t.Fatalf("4.04 h = %+v", f)
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
	q := diskpressure.Queries()
	if !strings.Contains(q["fs_avail_deriv"], "[21600s]") || !strings.Contains(q["journal_bytes"], "by (node, job)") {
		t.Fatal("queries")
	}
	if diskpressure.Read(ctx, nil, now).Source != diskpressure.SourceNotConfigured {
		t.Fatal("nil client should be not_configured")
	}
}
