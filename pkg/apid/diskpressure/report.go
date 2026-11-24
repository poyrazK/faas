// Package diskpressure builds the operator disk pressure report
// (ADR-20261124150000): every host filesystem's free space and inodes, how
// fast it is filling, the jail tmpfs (RAM until reboot), the systemd
// journal, and the guests whose writable disk is nearly full. It reads
// node-exporter and vmmd series from Prometheus and grades filesystems with
// the same thresholds as the FaasHostDisk* alerts.
package diskpressure

import (
	"context"
	"math"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Report sources.
const (
	SourceObserved      = "observed"
	SourcePartial       = "partial"
	SourceUnavailable   = "unavailable"
	SourceNotConfigured = "not_configured"
)

// Filesystem and node states, worst first.
const (
	StateCritical  = "critical"
	StateFilling   = "filling"
	StateLow       = "low"
	StateInodesLow = "inodes_low"
	StateOK        = "ok"
)

// Node roles, from the scrape job.
const (
	RoleCompute      = "compute"
	RoleDatabase     = "database"
	RoleControlPlane = "control_plane"
)

// JailMount is the jailer chroot tmpfs; it holds RAM until reboot.
const JailMount = "/srv/fc/jail"

// fsSelector matches the filesystems the disk alerts watch, plus tmpfs so
// the jail mount is visible; other tmpfs mounts are dropped in Go.
const fsSelector = `{job=~"node|node-compute|node-database",fstype!~"ramfs|devtmpfs|overlay|squashfs|iso9660|vfat|nsfs|autofs",device!~"/dev/loop.*"}`

const (
	qSize          = "fs_size"
	qAvail         = "fs_avail"
	qFiles         = "fs_files"
	qFilesFree     = "fs_files_free"
	qDeriv         = "fs_avail_deriv"
	qGuestPressure = "guest_pressure"
	qGuestRatio    = "guest_top_ratio"
	qGuestCapacity = "guest_top_capacity"
	qJournal       = "journal_bytes"
)

// Queries returns the report's PromQL by name.
func Queries() map[string]string {
	k := strconv.Itoa(api.ObsDiskGuestsMax)
	growth := strconv.Itoa(int(api.ObsDiskGrowthWindow.Seconds())) + "s"
	ratio := "topk(" + k + ", vmmd_guest_disk_used_bytes / vmmd_guest_disk_capacity_bytes > 0)"
	return map[string]string{
		qSize:          "node_filesystem_size_bytes" + fsSelector,
		qAvail:         "node_filesystem_avail_bytes" + fsSelector,
		qFiles:         "node_filesystem_files" + fsSelector,
		qFilesFree:     "node_filesystem_files_free" + fsSelector,
		qDeriv:         "deriv(node_filesystem_avail_bytes" + fsSelector + "[" + growth + "])",
		qGuestPressure: `count by (node, level) (vmmd_guest_disk_pressure{level=~"near_full|full"} == 1)`,
		qGuestRatio:    ratio,
		qGuestCapacity: "vmmd_guest_disk_capacity_bytes and " + ratio,
		qJournal:       "max by (node) (vmmd_journal_disk_usage_bytes)",
	}
}

// VectorQuerier is the slice of the Prometheus client the report uses.
type VectorQuerier interface {
	QueryVectorAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error)
}

// Samples is what Read returned.
type Samples struct {
	ByQuery map[string][]promql.VectorSample
	Failed  []string
	Source  string
}

// Read runs every query concurrently at one instant under
// ObsDiskPrometheusBudget.
func Read(ctx context.Context, client *promql.Client, at time.Time) Samples {
	if client == nil || client.BaseURL() == "" {
		return Samples{Source: SourceNotConfigured, ByQuery: map[string][]promql.VectorSample{}}
	}
	return read(ctx, client, at)
}

func read(ctx context.Context, q VectorQuerier, at time.Time) Samples {
	ctx, cancel := context.WithTimeout(ctx, api.ObsDiskPrometheusBudget)
	defer cancel()
	queries := Queries()
	out := Samples{ByQuery: make(map[string][]promql.VectorSample, len(queries))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := q.QueryVectorAt(ctx, query, at)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Failed = append(out.Failed, name)
				return
			}
			out.ByQuery[name] = rows
		}()
	}
	wg.Wait()
	sort.Strings(out.Failed)
	switch {
	case len(out.Failed) == 0:
		out.Source = SourceObserved
	case len(out.Failed) == len(queries):
		out.Source = SourceUnavailable
	default:
		out.Source = SourcePartial
	}
	return out
}

// GuestInfo is what the store knows about a guest's instance.
type GuestInfo struct {
	AppID     string
	AppSlug   string
	AccountID string
}

// Input is the instant, the samples and a guest lookup.
type Input struct {
	Now     time.Time
	Samples Samples
	// Lookup resolves an instance ID to its app; nil or a miss leaves it
	// empty.
	Lookup func(instanceID string) (GuestInfo, bool)
}

type fsKey struct{ node, mount, device string }

// Build folds the samples into the report.
func Build(in Input) api.ObsDiskPressureReport {
	out := api.ObsDiskPressureReport{
		AsOf: in.Now, Source: in.Samples.Source, FailedQueries: in.Samples.Failed,
		LowFreePercent: api.ObsDiskLowFreePercent, CriticalFreePercent: api.ObsDiskCriticalFreePercent,
		FillingFreePercent: api.ObsDiskFillingFreePercent, FillingHorizonHours: api.ObsDiskFillingHorizonHours,
		InodesLowFreePercent: api.ObsDiskInodesLowFreePercent, GrowthWindowSeconds: int(api.ObsDiskGrowthWindow.Seconds()),
		Nodes: []api.ObsDiskNode{}, Filesystems: []api.ObsDiskFilesystem{}, Guests: []api.ObsDiskGuest{},
	}
	if out.FailedQueries == nil {
		out.FailedQueries = []string{}
	}
	s := in.Samples.ByQuery
	fss := filesystems(s)
	nodes := map[string]*api.ObsDiskNode{}
	node := func(name, role string) *api.ObsDiskNode {
		if nodes[name] == nil {
			nodes[name] = &api.ObsDiskNode{Node: name, Role: role, State: StateOK}
		}
		if nodes[name].Role == "" {
			nodes[name].Role = role
		}
		return nodes[name]
	}
	for _, f := range fss {
		n := node(f.Node, f.Role)
		n.Filesystems++
		if stateRank(f.State) < stateRank(n.State) {
			n.State = f.State
		}
		if n.WorstMountpoint == "" || f.FreePercent < n.WorstFreePercent {
			n.WorstMountpoint, n.WorstFreePercent = f.Mountpoint, f.FreePercent
		}
		if f.JailTmpfs {
			used := f.SizeBytes - f.AvailBytes
			n.JailTmpfsUsedBytes += used
			out.Totals.JailTmpfsUsedBytes += used
		}
	}
	for _, r := range s[qJournal] {
		v := int64(r.Value)
		node(r.Labels["node"], RoleCompute).JournalBytes = &v
	}
	for _, r := range s[qGuestPressure] {
		n, v := node(r.Labels["node"], RoleCompute), int(math.Round(r.Value))
		switch r.Labels["level"] {
		case "near_full":
			n.GuestsNearFull += v
			out.Totals.GuestsNearFull += v
		case "full":
			n.GuestsFull += v
			out.Totals.GuestsFull += v
		}
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, *n)
		out.Totals.Nodes++
		switch n.State {
		case StateCritical:
			out.Totals.NodesCritical++
		case StateFilling:
			out.Totals.NodesFilling++
		case StateLow:
			out.Totals.NodesLow++
		case StateInodesLow:
			out.Totals.NodesInodesLow++
		}
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		a, b := out.Nodes[i], out.Nodes[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		return a.Node < b.Node
	})
	out.Totals.Filesystems = len(fss)
	if len(fss) > api.ObsDiskFilesystemsMax {
		fss, out.Truncated = fss[:api.ObsDiskFilesystemsMax], true
	}
	out.Filesystems = fss
	out.Guests = guests(s, in.Lookup)
	return out
}

// filesystems joins size, free space, inodes and growth per mount, graded
// and sorted worst first.
func filesystems(s map[string][]promql.VectorSample) []api.ObsDiskFilesystem {
	rows := map[fsKey]*api.ObsDiskFilesystem{}
	keyOf := func(r promql.VectorSample) (fsKey, bool) {
		mount, fstype := r.Labels["mountpoint"], r.Labels["fstype"]
		if fstype == "tmpfs" && mount != JailMount && !strings.HasPrefix(mount, JailMount+"/") {
			return fsKey{}, false
		}
		return fsKey{nodeName(r.Labels), mount, r.Labels["device"]}, true
	}
	for _, r := range s[qSize] {
		k, ok := keyOf(r)
		if !ok || r.Value <= 0 {
			continue
		}
		rows[k] = &api.ObsDiskFilesystem{Node: k.node, Role: role(r.Labels["job"]), Mountpoint: k.mount, Device: k.device,
			FSType: r.Labels["fstype"], SizeBytes: int64(r.Value), JailTmpfs: r.Labels["fstype"] == "tmpfs"}
	}
	inodes := map[fsKey]float64{}
	for _, r := range s[qFiles] {
		if k, ok := keyOf(r); ok {
			inodes[k] = r.Value
		}
	}
	for _, r := range s[qAvail] {
		if k, ok := keyOf(r); ok && rows[k] != nil {
			rows[k].AvailBytes = int64(r.Value)
		}
	}
	for _, r := range s[qFilesFree] {
		if k, ok := keyOf(r); ok && rows[k] != nil && inodes[k] > 0 {
			free := pct(r.Value, inodes[k])
			rows[k].InodesFreePercent = &free
		}
	}
	for _, r := range s[qDeriv] {
		k, ok := keyOf(r)
		if !ok || rows[k] == nil || r.Value >= 0 || math.IsNaN(r.Value) {
			continue
		}
		hours := float64(rows[k].AvailBytes) / -r.Value / 3600
		if hours <= api.ObsDiskHoursToFullMax {
			h := math.Round(hours*10) / 10
			rows[k].HoursToFull = &h
		}
	}
	out := make([]api.ObsDiskFilesystem, 0, len(rows))
	for _, f := range rows {
		f.FreePercent = pct(float64(f.AvailBytes), float64(f.SizeBytes))
		f.State = grade(*f)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		if a.FreePercent != b.FreePercent {
			return a.FreePercent < b.FreePercent
		}
		return a.Node+a.Mountpoint < b.Node+b.Mountpoint
	})
	return out
}

// grade mirrors FaasHostDiskSpaceCritical, FaasHostDiskFillingFast,
// FaasHostDiskSpaceLow and FaasHostInodesLow, worst first.
func grade(f api.ObsDiskFilesystem) string {
	switch {
	case f.FreePercent < api.ObsDiskCriticalFreePercent:
		return StateCritical
	case f.FreePercent < api.ObsDiskFillingFreePercent && f.HoursToFull != nil && *f.HoursToFull <= api.ObsDiskFillingHorizonHours:
		return StateFilling
	case f.FreePercent < api.ObsDiskLowFreePercent:
		return StateLow
	case f.InodesFreePercent != nil && *f.InodesFreePercent < api.ObsDiskInodesLowFreePercent:
		return StateInodesLow
	default:
		return StateOK
	}
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// guestID is vmmd's instance label. Prometheus renames it exported_instance
// because the scrape target has its own instance label.
func guestID(l map[string]string) string {
	for _, k := range []string{"exported_instance", "instance"} {
		if uuidRE.MatchString(l[k]) {
			return l[k]
		}
	}
	return ""
}

// guests lists the fullest guest disks, fullest first.
func guests(s map[string][]promql.VectorSample, lookup func(string) (GuestInfo, bool)) []api.ObsDiskGuest {
	capacity := map[string]float64{}
	for _, r := range s[qGuestCapacity] {
		if id := guestID(r.Labels); id != "" {
			capacity[id] = r.Value
		}
	}
	out := []api.ObsDiskGuest{}
	for _, r := range s[qGuestRatio] {
		id := guestID(r.Labels)
		if id == "" || math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
			continue
		}
		g := api.ObsDiskGuest{InstanceID: id, Node: r.Labels["node"], UsedPercent: math.Round(r.Value*1000) / 10}
		if c := capacity[id]; c > 0 {
			g.CapacityBytes, g.UsedBytes = int64(c), int64(c*r.Value)
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UsedPercent != out[j].UsedPercent {
			return out[i].UsedPercent > out[j].UsedPercent
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	if len(out) > api.ObsDiskGuestsMax {
		out = out[:api.ObsDiskGuestsMax]
	}
	for i := range out {
		if lookup == nil {
			break
		}
		if info, ok := lookup(out[i].InstanceID); ok {
			out[i].AppID, out[i].AppSlug, out[i].AccountID = info.AppID, info.AppSlug, info.AccountID
		}
	}
	return out
}

// nodeName is the discovery node label, else the scrape target's host.
func nodeName(l map[string]string) string {
	if n := l["node"]; n != "" {
		return n
	}
	if host, _, err := net.SplitHostPort(l["instance"]); err == nil {
		return host
	}
	return l["instance"]
}

func role(job string) string {
	switch job {
	case "node-compute":
		return RoleCompute
	case "node-database":
		return RoleDatabase
	default:
		return RoleControlPlane
	}
}

func stateRank(s string) int {
	switch s {
	case StateCritical:
		return 0
	case StateFilling:
		return 1
	case StateLow:
		return 2
	case StateInodesLow:
		return 3
	default:
		return 4
	}
}

// pct is part of whole as a percentage with one decimal.
func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(part*1000/whole) / 10
}
