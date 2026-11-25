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
	// StateUnknown: the filesystem's free space, or any filesystem of the
	// node, was not read.
	StateUnknown = "unknown"
	StateOK      = "ok"
	// StateRAM marks the jail tmpfs: shown, not graded as a disk and not
	// part of its node's state, as the disk alerts exclude tmpfs.
	StateRAM = "ram"
)

// ControlPlaneNode names the control plane's static node-exporter target,
// which has no discovery node label.
const ControlPlaneNode = "control-plane"

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
		qGuestPressure: `count by (node, job, level) (vmmd_guest_disk_pressure{level=~"near_full|full"} == 1)`,
		qGuestRatio:    ratio,
		qGuestCapacity: "vmmd_guest_disk_capacity_bytes and " + ratio,
		qJournal:       "max by (node, job) (vmmd_journal_disk_usage_bytes)",
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
		if f.JailTmpfs {
			// RAM, not disk: counted on its own, outside the node's state.
			if f.AvailBytes != nil {
				used := f.SizeBytes - *f.AvailBytes
				n.JailTmpfsUsedBytes += used
				out.Totals.JailTmpfsUsedBytes += used
			}
			continue
		}
		n.Filesystems++
		if stateRank(f.State) < stateRank(n.State) {
			n.State = f.State
		}
		if f.FreePercent != nil && (n.WorstMountpoint == "" || *f.FreePercent < n.WorstFreePercent) {
			n.WorstMountpoint, n.WorstFreePercent = f.Mountpoint, *f.FreePercent
		}
	}
	for _, r := range s[qJournal] {
		if r.Labels["node"] == "" {
			continue
		}
		v := int64(r.Value)
		node(r.Labels["node"], vmmdRole(r.Labels["job"])).JournalBytes = &v
	}
	for _, r := range s[qGuestPressure] {
		if r.Labels["node"] == "" {
			continue
		}
		n, v := node(r.Labels["node"], vmmdRole(r.Labels["job"])), int(math.Round(r.Value))
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
		if n.Filesystems == 0 {
			// Seen only through vmmd: no disk was read, so it is not healthy.
			n.State = StateUnknown
		}
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
		case StateUnknown:
			out.Totals.NodesUnknown++
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
	// raw keeps the unrounded ratios the grade compares, so it matches the
	// alerts exactly; only the response is rounded.
	raw := map[fsKey]*rawFS{}
	for k := range rows {
		raw[k] = &rawFS{}
	}
	for _, r := range s[qAvail] {
		if k, ok := keyOf(r); ok && rows[k] != nil && !math.IsNaN(r.Value) {
			avail := int64(r.Value)
			rows[k].AvailBytes = &avail
			free := r.Value / float64(rows[k].SizeBytes)
			raw[k].free = &free
		}
	}
	for _, r := range s[qFilesFree] {
		if k, ok := keyOf(r); ok && rows[k] != nil && inodes[k] > 0 {
			free := r.Value / inodes[k]
			raw[k].inodesFree = &free
			rounded := round1(free * 100)
			rows[k].InodesFreePercent = &rounded
		}
	}
	for _, r := range s[qDeriv] {
		k, ok := keyOf(r)
		if !ok || rows[k] == nil || rows[k].AvailBytes == nil || r.Value >= 0 || math.IsNaN(r.Value) {
			continue
		}
		hours := float64(*rows[k].AvailBytes) / -r.Value / 3600
		raw[k].hoursToFull = &hours
		if hours <= api.ObsDiskHoursToFullMax {
			h := round1(hours)
			rows[k].HoursToFull = &h
		}
	}
	out := make([]api.ObsDiskFilesystem, 0, len(rows))
	for k, f := range rows {
		if raw[k].free != nil {
			free := round1(*raw[k].free * 100)
			f.FreePercent = &free
		}
		f.State = grade(*f, *raw[k])
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		if fa, fb := freeOrMax(a), freeOrMax(b); fa != fb {
			return fa < fb
		}
		return a.Node+a.Mountpoint < b.Node+b.Mountpoint
	})
	return out
}

// rawFS is a filesystem's unrounded free ratios and time to full.
type rawFS struct {
	free, inodesFree, hoursToFull *float64
}

func freeOrMax(f api.ObsDiskFilesystem) float64 {
	if f.FreePercent == nil {
		return math.MaxFloat64
	}
	return *f.FreePercent
}

// grade mirrors FaasHostDiskSpaceCritical (free < 0.05),
// FaasHostDiskFillingFast (free < 0.25 and full within 4 h),
// FaasHostDiskSpaceLow (free < 0.15) and FaasHostInodesLow (inodes free <
// 0.10), worst first, on the unrounded ratios. Without a free-space reading
// the state is unknown; the jail tmpfs is not graded.
func grade(f api.ObsDiskFilesystem, r rawFS) string {
	const pc = 100.0
	switch {
	case f.JailTmpfs:
		return StateRAM
	case r.free == nil:
		return StateUnknown
	case *r.free*pc < api.ObsDiskCriticalFreePercent:
		return StateCritical
	case *r.free*pc < api.ObsDiskFillingFreePercent && r.hoursToFull != nil && *r.hoursToFull <= api.ObsDiskFillingHorizonHours:
		return StateFilling
	case *r.free*pc < api.ObsDiskLowFreePercent:
		return StateLow
	case r.inodesFree != nil && *r.inodesFree*pc < api.ObsDiskInodesLowFreePercent:
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

// nodeName is the discovery node label, else the scrape target's host. The
// control plane's static target scrapes loopback, which names no host, so
// it is reported as ControlPlaneNode.
func nodeName(l map[string]string) string {
	if n := l["node"]; n != "" {
		return n
	}
	host := l["instance"]
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() || host == "" {
		if role(l["job"]) == RoleControlPlane {
			return ControlPlaneNode
		}
	}
	return host
}

// vmmdRole is the node role of a vmmd series, from its scrape job.
func vmmdRole(job string) string {
	if job == "vmmd-database" {
		return RoleDatabase
	}
	return RoleCompute
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
	case StateUnknown:
		return 4
	default:
		return 5
	}
}

// round1 rounds to one decimal for the response.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
