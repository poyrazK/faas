package fcvm

// Dry-run classification of journal records against the adoption proof
// (vmmd restart adoption ADR, "The adoption proof" steps 1–6; slice 1).
//
// It only reads: /proc, cgroupfs, sysfs and two GET requests on each guest's
// Firecracker API socket. It never signals a process, writes a file or
// changes quarantine. Step 7, the ownership epoch, arrives with scheduler
// reconciliation (slice 3), so a record classified adoptable here is "would
// be adoptable once this vmmd holds the next epoch".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Refusal reasons. They are metric label values: keep them few and stable.
const (
	adoptionRefusedNoRecord       = "no_adoption_record"
	adoptionRefusedKind           = "kind"
	adoptionRefusedStopping       = "stopping"
	adoptionRefusedOperation      = "op_in_flight"
	adoptionRefusedConsolePipe    = "console_pipe"
	adoptionRefusedNoProcess      = "no_process"
	adoptionRefusedOtherBoot      = "other_boot"
	adoptionRefusedProcessAbsent  = "process_absent"
	adoptionRefusedProcessReused  = "process_reused"
	adoptionRefusedArgv           = "argv_mismatch"
	adoptionRefusedUID            = "uid_mismatch"
	adoptionRefusedExe            = "exe_mismatch"
	adoptionRefusedCgroup         = "cgroup_mismatch"
	adoptionRefusedNetns          = "netns_mismatch"
	adoptionRefusedVeth           = "veth_mismatch"
	adoptionRefusedAPIUnreachable = "api_unreachable"
	adoptionRefusedAPIIdentity    = "api_identity"
	adoptionRefusedMachineConfig  = "machine_config"
	adoptionRefusedUnreadable     = "unreadable"
	adoptionRefusedPrivateLink    = "private_link_mismatch"

	// adoptionAPITimeout bounds each Firecracker API request (ADR proof
	// step 6).
	adoptionAPITimeout = 250 * time.Millisecond
	// adoptionClassifyParallel bounds concurrent classifications.
	adoptionClassifyParallel = 32
)

// AdoptionVerdict is one record's classification.
type AdoptionVerdict struct {
	Instance string
	// AppID is the recorded app, when the record has an adoption block.
	AppID     string
	Adoptable bool
	// Reason names the first failed proof step of a refused record.
	Reason string
	// Detail is a human-readable explanation for logs.
	Detail string
	// FenceRepair marks an adoptable guest whose cgroup memory.max drifted
	// from the recorded fence; adoption repairs it first (proof step 4).
	FenceRepair bool
	// DevPatched: the guest was served a developer live patch and must never
	// be snapshotted (ADR-740); vmmd re-marks it after adoption.
	DevPatched bool
	// Resolved is the outcome of the operation in flight at the restart,
	// settled before adoption: AdoptionResolvedThawed for a freeze or thaw.
	Resolved string
	// Admission is set for an adopted guest booted under an ADR-595 receipt.
	Admission *AdoptionAdmission
}

// AdoptionAdmission is the serving receipt an adopted guest carries and the
// residency this vmmd adopted it with: what schedd's re-bind of the
// admission records (docs/adr/20261018-runtime-admission-rebind.md).
type AdoptionAdmission struct {
	Token               string
	IssuingIncarnation  string
	CompletedAtUnixNano int64
	Netns               string
	HostIP              string
	LeaseUID            int32
	Paused              bool
}

// Outcomes of an operation in flight at the restart, settled before the
// report. AdoptionResolvedThawed: a freeze or thaw was cut short, so adoption
// resumed the guest; it runs, whatever the row says. AdoptionResolvedStopped:
// a stop, destroy, park or warm snapshot was cut short, so the successor
// finished the teardown; the guest is gone and is not adopted.
// AdoptionResolvedResumed: a migration prepare was cut short before its
// capture answered, so adoption resumed the source. AdoptionResolvedKilled:
// an app task, execution or builder. Its command and result travel over one
// stream, or for a builder one export call, that the predecessor held and
// the restart ended, so the successor kills it through the ordinary teardown
// and its owner reports the broken stream, as for any transport failure:
// none of them is replayed.
const (
	AdoptionResolvedThawed  = "thawed"
	AdoptionResolvedStopped = "stopped"
	AdoptionResolvedResumed = "resumed"
	AdoptionResolvedKilled  = "killed"
)

// AdoptionEnds reports a resolution that tears the guest down instead of
// adopting it: the guest is gone, which is not a refusal.
func AdoptionEnds(resolved string) bool {
	return resolved == AdoptionResolvedStopped || resolved == AdoptionResolvedKilled
}

// AdoptionDryRunReport summarizes a dry run, or a real adoption pass, which
// also fills Adopted.
type AdoptionDryRunReport struct {
	Adoptable    int
	Adopted      int
	FenceRepairs int
	// Stopped counts guests whose interrupted stop the pass finished.
	Stopped int
	// Killed counts app tasks, executions and builders the pass killed.
	Killed   int
	Refused  map[string]int
	Verdicts []AdoptionVerdict
}

// firecrackerState is what the API reports for one guest. MachineConfigs is
// false when GET /machine-config failed after GET / succeeded.
type firecrackerState struct {
	ID, State      string
	VCPUs, MemMiB  int
	MachineConfigs bool
}

// errMachineConfigUnavailable reports a guest that answered GET / but not
// GET /machine-config; the state returned with it is still valid.
var errMachineConfigUnavailable = errors.New("firecracker machine-config unavailable")

type adoptionClassifier struct {
	procRoot, cgroupRoot, netRoot string
	bootID                        string
	api                           func(ctx context.Context, socket string) (firecrackerState, error)
	// pidfd re-reads start ticks through a pidfd, closing the PID-reuse
	// window between the scan and the check. nil skips it (portable tests).
	pidfd func(pid int, want resourceProcessIdentity) error
	// vethPeer reports whether the host veth's peer is the interface peer
	// inside the network namespace of process pid. A nil vethPeer skips the
	// check. It reads through the process, not the /run/netns name: the
	// successor may not see the predecessor's binding (fsn-4, V-A1a).
	vethPeer func(ctx context.Context, pid int, host, peer string) (bool, error)
}

// DryRunAdoption classifies every guest record in the resource journal
// against the adoption proof without changing anything. vmmd runs it in the
// background after startup and exports the result; quarantine is unchanged.
func (m *Manager) DryRunAdoption(ctx context.Context) (AdoptionDryRunReport, error) {
	if m.resourceJournal == nil {
		return AdoptionDryRunReport{Refused: map[string]int{}}, nil
	}
	boot, err := resourceKernelBootID("/proc")
	if err != nil {
		return AdoptionDryRunReport{}, fmt.Errorf("adoption dry run: kernel boot id: %w", err)
	}
	c := adoptionClassifier{
		procRoot: "/proc", cgroupRoot: cgroupRoot, netRoot: "/sys/class/net",
		bootID: boot, api: firecrackerAPIState, pidfd: adoptionPidfdCheck,
		vethPeer: vethPeerInProcessNetns,
	}
	return c.run(ctx, m.resourceJournal)
}

func (c adoptionClassifier) run(ctx context.Context, j *ResourceJournal) (AdoptionDryRunReport, error) {
	records, err := j.snapshot()
	if err != nil {
		return AdoptionDryRunReport{}, err
	}
	verdicts := make([]AdoptionVerdict, 0, len(records))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, adoptionClassifyParallel)
	)
	for _, r := range records {
		if r.preparedSpare() {
			continue // Spares are never adopted (ADR §2 "Prepared spares").
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r resourceJournalRecord) {
			defer wg.Done()
			defer func() { <-sem }()
			v := c.classify(ctx, r)
			if r.Adoption != nil {
				v.AppID, v.DevPatched = r.Adoption.Instance.AppID, r.Adoption.Instance.DevPatched
			}
			mu.Lock()
			verdicts = append(verdicts, v)
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	rep := AdoptionDryRunReport{Refused: map[string]int{}, Verdicts: verdicts}
	for _, v := range verdicts {
		if v.Adoptable {
			rep.Adoptable++
			if v.FenceRepair {
				rep.FenceRepairs++
			}
			continue
		}
		rep.Refused[v.Reason]++
	}
	return rep, ctx.Err()
}

func refuse(instance, reason, detail string) AdoptionVerdict {
	return AdoptionVerdict{Instance: instance, Reason: reason, Detail: detail}
}

// classify applies proof steps 1–6 in order and reports the first failure.
func (c adoptionClassifier) classify(ctx context.Context, r resourceJournalRecord) AdoptionVerdict {
	resolved, refused, ok := classifyRecord(r)
	if !ok {
		return refused
	}
	repair, v, ok := c.prove(ctx, r)
	if !ok {
		return v
	}
	return AdoptionVerdict{Instance: r.Lease.Instance, Adoptable: true, FenceRepair: repair, Resolved: resolved}
}

// classifyRecord is proof step 1, which reads only the record: the part a
// candidate vmmd binary can judge before a release restarts the running one
// (ADR §4 "Capability check"). It returns how an interrupted operation
// resolves, or the refusal.
func classifyRecord(r resourceJournalRecord) (resolved string, refused AdoptionVerdict, ok bool) {
	id := r.Lease.Instance
	a := r.Adoption
	if r.Version != resourceAdoptionVersion || a == nil {
		return "", refuse(id, adoptionRefusedNoRecord, fmt.Sprintf("journal version %d without an adoption block", r.Version)), false
	}
	// An app task, execution or builder is killed, not adopted: the one
	// stream or call that owned it ended with the predecessor
	// (AdoptionResolvedKilled).
	kill := a.Kind == adoptionKindAppTask || a.Kind == adoptionKindExecution || a.Kind == adoptionKindBuilder
	if a.Kind != adoptionKindApp && a.Kind != adoptionKindJob && !kill {
		return "", refuse(id, adoptionRefusedKind, "kind "+a.Kind+" is not adoptable"), false
	}
	if a.Lifecycle.State == adoptionStateStopping {
		return "", refuse(id, adoptionRefusedStopping, "teardown was in progress"), false
	}
	if kill {
		resolved = AdoptionResolvedKilled
	}
	// An interrupted freeze or thaw resolves by resuming the guest, and an
	// interrupted stop or destroy by finishing its teardown (slice 3). Both
	// still need the full proof. Any other interrupted operation refuses.
	if op := a.Lifecycle.Op; op != nil && !kill {
		switch op.Kind {
		case adoptionOpFreeze, adoptionOpThaw:
			resolved = AdoptionResolvedThawed
		case adoptionOpStop, adoptionOpPark, adoptionOpWarmSnapshot:
			// The scheduler destroys the VM when a park or warm snapshot
			// fails, so finishing the teardown matches what it will do.
			resolved = AdoptionResolvedStopped
		case adoptionOpSnapshot:
			// A migration prepare cut short: its capture never answered, so
			// the source resumes, as CancelLiveMigration would. One that did
			// answer is recorded as migration_paused and stays paused.
			if !a.Instance.MigrationPaused {
				resolved = AdoptionResolvedResumed
			}
		default:
			return "", refuse(id, adoptionRefusedOperation, "operation "+op.Kind+" was in flight"), false
		}
	}
	if !a.Firecracker.ConsoleFile {
		return "", refuse(id, adoptionRefusedConsolePipe, "launched with a pipe console, which wedges its API once vmmd dies"), false
	}
	if r.Process == nil {
		return "", refuse(id, adoptionRefusedNoProcess, "no recorded process"), false
	}
	return resolved, AdoptionVerdict{}, true
}

// prove runs proof steps 2–6 against the live host. The record has passed
// classifyRecord. repair reports a cgroup fence that drifted and adoption
// rewrites.
func (c adoptionClassifier) prove(ctx context.Context, r resourceJournalRecord) (repair bool, refused AdoptionVerdict, ok bool) {
	id := r.Lease.Instance
	// 2. Same boot.
	if r.Process.BootID != c.bootID {
		return false, refuse(id, adoptionRefusedOtherBoot, "recorded on boot "+r.Process.BootID), false
	}
	// 3. Same process.
	if v, ok := c.checkProcess(r); !ok {
		return false, v, false
	}
	// 4. Same cgroup.
	repair, v, ok := c.checkCgroup(r)
	if !ok {
		return false, v, false
	}
	// 5. Same network.
	if v, ok := c.checkNetwork(ctx, r); !ok {
		return false, v, false
	}
	// 6. Firecracker answers.
	if v, ok := c.checkAPI(ctx, r); !ok {
		return false, v, false
	}
	return repair, AdoptionVerdict{}, true
}

func (c adoptionClassifier) checkProcess(r resourceJournalRecord) (AdoptionVerdict, bool) {
	id, want := r.Lease.Instance, *r.Process
	current, err := readResourceProcessIdentity(c.procRoot, want.PID)
	switch {
	case errors.Is(err, errResourceProcessAbsent):
		return refuse(id, adoptionRefusedProcessAbsent, "pid "+strconv.Itoa(want.PID)+" is gone"), false
	case err != nil:
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	case current != want:
		return refuse(id, adoptionRefusedProcessReused, "pid "+strconv.Itoa(want.PID)+" names another process"), false
	}
	path := filepath.Join(c.procRoot, strconv.Itoa(want.PID))
	argv, err := os.ReadFile(filepath.Join(path, "cmdline"))
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if gotID, jailer := restartProcessID(strings.Split(string(argv), "\x00")); jailer || gotID != PerInstanceScope(id) {
		return refuse(id, adoptionRefusedArgv, "process is not this instance's Firecracker"), false
	}
	status, err := os.ReadFile(filepath.Join(path, "status"))
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if restartProcessSlot(string(status)) != r.Lease.Slot {
		return refuse(id, adoptionRefusedUID, "kernel uids differ from the lease uid"), false
	}
	exe, err := statIdentity(filepath.Join(path, "exe"))
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if exe != r.Adoption.Firecracker.Exe {
		return refuse(id, adoptionRefusedExe, "executable inode differs from the record"), false
	}
	if c.pidfd != nil {
		if err := c.pidfd(want.PID, want); err != nil {
			return refuse(id, adoptionRefusedProcessReused, err.Error()), false
		}
	}
	return AdoptionVerdict{}, true
}

// checkCgroup verifies the process sits in its plan scope. A memory.max that
// drifted from the recorded fence is repairable, not a refusal.
func (c adoptionClassifier) checkCgroup(r resourceJournalRecord) (repair bool, _ AdoptionVerdict, _ bool) {
	id := r.Lease.Instance
	raw, err := os.ReadFile(filepath.Join(c.procRoot, strconv.Itoa(r.Process.PID), "cgroup"))
	if err != nil {
		return false, refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	scope := filepath.Join(vmCgroupParent(r.Lease), PerInstanceScope(id))
	if strings.TrimSpace(string(raw)) != "0::/"+scope {
		return false, refuse(id, adoptionRefusedCgroup, "process is in "+strings.TrimSpace(string(raw))), false
	}
	maxRaw, err := os.ReadFile(filepath.Join(c.cgroupRoot, scope, "memory.max"))
	if err != nil {
		return false, refuse(id, adoptionRefusedCgroup, "memory.max unreadable: "+err.Error()), false
	}
	return strings.TrimSpace(string(maxRaw)) != r.Adoption.Firecracker.MemoryMax, AdoptionVerdict{}, true
}

func (c adoptionClassifier) checkNetwork(ctx context.Context, r resourceJournalRecord) (AdoptionVerdict, bool) {
	id, l := r.Lease.Instance, r.Lease
	if l.Networkless {
		return AdoptionVerdict{}, true
	}
	var ns, veth *resourceAsset
	for i := range r.Assets {
		switch a := &r.Assets[i]; a.Kind {
		case "netns":
			ns = a
		case "veth":
			if a.Link != nil && a.Link.Name == l.VethHost {
				veth = a
			}
		}
	}
	if ns == nil || ns.File == nil {
		return refuse(id, adoptionRefusedNetns, "no recorded namespace identity"), false
	}
	got, err := statIdentity(filepath.Join(c.procRoot, strconv.Itoa(r.Process.PID), "ns", "net"))
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if got != *ns.File {
		return refuse(id, adoptionRefusedNetns, "process is in another network namespace"), false
	}
	if veth == nil {
		return refuse(id, adoptionRefusedVeth, "no recorded host veth"), false
	}
	dir := filepath.Join(c.netRoot, l.VethHost)
	index, errIndex := os.ReadFile(filepath.Join(dir, "ifindex"))
	address, errAddr := os.ReadFile(filepath.Join(dir, "address"))
	if errIndex != nil || errAddr != nil {
		return refuse(id, adoptionRefusedVeth, "host veth "+l.VethHost+" is missing"), false
	}
	if strings.TrimSpace(string(index)) != strconv.Itoa(veth.Link.Index) || !strings.EqualFold(strings.TrimSpace(string(address)), veth.Link.Address) {
		return refuse(id, adoptionRefusedVeth, "host veth "+l.VethHost+" is not the recorded link"), false
	}
	if c.vethPeer == nil {
		return AdoptionVerdict{}, true
	}
	paired, err := c.vethPeer(ctx, r.Process.PID, l.VethHost, l.VethPeer)
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if !paired {
		return refuse(id, adoptionRefusedVeth, "host veth "+l.VethHost+" is not paired with "+l.VethPeer+" in the guest's namespace"), false
	}
	if r.Adoption != nil && r.Adoption.Network.Private != nil {
		return c.checkPrivateLink(ctx, r)
	}
	if host, _ := privateVethNames(l.Slot); privateLinkAsset(r, host) != nil {
		// A side-link the block does not describe: an attachment changed
		// after the block was written.
		return refuse(id, adoptionRefusedPrivateLink, "private side-link "+host+" is not in the adoption block"), false
	}
	return AdoptionVerdict{}, true
}

func privateLinkAsset(r resourceJournalRecord, host string) *resourceAsset {
	for i := range r.Assets {
		if a := &r.Assets[i]; a.Kind == "veth" && a.Link != nil && a.Link.Name == host {
			return a
		}
	}
	return nil
}

// checkPrivateLink proves a private-network member's side-link the way
// checkNetwork proves the public veth, and that it is still a port of the
// recorded account bridge.
func (c adoptionClassifier) checkPrivateLink(ctx context.Context, r resourceJournalRecord) (AdoptionVerdict, bool) {
	id := r.Lease.Instance
	host, peer := privateVethNames(r.Lease.Slot)
	link := privateLinkAsset(r, host)
	if link == nil {
		return refuse(id, adoptionRefusedPrivateLink, "no recorded private side-link "+host), false
	}
	dir := filepath.Join(c.netRoot, host)
	index, errIndex := os.ReadFile(filepath.Join(dir, "ifindex"))
	address, errAddr := os.ReadFile(filepath.Join(dir, "address"))
	if errIndex != nil || errAddr != nil {
		return refuse(id, adoptionRefusedPrivateLink, "private side-link "+host+" is missing"), false
	}
	if strings.TrimSpace(string(index)) != strconv.Itoa(link.Link.Index) || !strings.EqualFold(strings.TrimSpace(string(address)), link.Link.Address) {
		return refuse(id, adoptionRefusedPrivateLink, "private side-link "+host+" is not the recorded link"), false
	}
	master, err := os.Readlink(filepath.Join(dir, "master"))
	if err != nil || filepath.Base(master) != r.Adoption.Network.Private.Bridge {
		return refuse(id, adoptionRefusedPrivateLink, "private side-link "+host+" is not a port of "+r.Adoption.Network.Private.Bridge), false
	}
	if c.vethPeer == nil {
		return AdoptionVerdict{}, true
	}
	paired, err := c.vethPeer(ctx, r.Process.PID, host, peer)
	if err != nil {
		return refuse(id, adoptionRefusedUnreadable, err.Error()), false
	}
	if !paired {
		return refuse(id, adoptionRefusedPrivateLink, "private side-link "+host+" is not paired with "+peer+" in the guest's namespace"), false
	}
	return AdoptionVerdict{}, true
}

func (c adoptionClassifier) checkAPI(ctx context.Context, r resourceJournalRecord) (AdoptionVerdict, bool) {
	id := r.Lease.Instance
	var root string
	for _, a := range r.Assets {
		if a.Kind == "jail" && filepath.Base(a.Path) == "root" {
			root = a.Path
		}
	}
	if root == "" {
		return refuse(id, adoptionRefusedAPIUnreachable, "no recorded jail root"), false
	}
	state, err := c.api(ctx, filepath.Join(root, APISockName))
	if err != nil && !errors.Is(err, errMachineConfigUnavailable) {
		return refuse(id, adoptionRefusedAPIUnreachable, err.Error()), false
	}
	if state.ID != PerInstanceScope(id) || (state.State != "Running" && state.State != "Paused") {
		return refuse(id, adoptionRefusedAPIIdentity, fmt.Sprintf("api reports id %q state %q", state.ID, state.State)), false
	}
	fc := r.Adoption.Firecracker
	if !state.MachineConfigs || state.VCPUs != fc.VCPUs || state.MemMiB != fc.MemoryMiB {
		return refuse(id, adoptionRefusedMachineConfig, fmt.Sprintf("api reports %d vcpus %d MiB, recorded %d vcpus %d MiB", state.VCPUs, state.MemMiB, fc.VCPUs, fc.MemoryMiB)), false
	}
	return AdoptionVerdict{}, true
}

// vethPeerInProcessNetns checks the veth pair from both ends: the host
// link's peer index (iflink) must be the guest-side link's index inside the
// process's network namespace, and that link's peer index must be the host
// link's. Reads only.
func vethPeerInProcessNetns(ctx context.Context, pid int, host, peer string) (bool, error) {
	read := func(name, file string) (int, error) {
		raw, err := os.ReadFile(filepath.Join("/sys/class/net", name, file))
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	hostIndex, err := read(host, "ifindex")
	if err != nil {
		return false, err
	}
	hostPeer, err := read(host, "iflink")
	if err != nil {
		return false, err
	}
	out, err := exec.CommandContext(ctx, "nsenter", "--net=/proc/"+strconv.Itoa(pid)+"/ns/net", "--",
		"ip", "-j", "link", "show", "dev", peer).Output()
	if err != nil {
		return false, fmt.Errorf("guest-side veth %s: %w", peer, err)
	}
	var links []struct {
		Index int `json:"ifindex"`
		Link  int `json:"link_index"`
	}
	if err := json.Unmarshal(out, &links); err != nil {
		return false, fmt.Errorf("parse guest-side veth %s: %w", peer, err)
	}
	return len(links) == 1 && links[0].Index == hostPeer && links[0].Link == hostIndex, nil
}

func statIdentity(path string) (resourceFileIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return resourceFileIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return resourceFileIdentity{}, errors.New("no inode identity for " + path)
	}
	return resourceFileIdentity{Device: uint64(st.Dev), Inode: st.Ino}, nil //nolint:unconvert // Dev is int32 on darwin
}

// firecrackerAPIState issues GET / and GET /machine-config on a guest's API
// socket. Both are read-only.
func firecrackerAPIState(ctx context.Context, socket string) (firecrackerState, error) {
	client := &http.Client{
		Timeout: adoptionAPITimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			DisableKeepAlives: true,
		},
	}
	var info struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := firecrackerGET(ctx, client, "/", &info); err != nil {
		return firecrackerState{}, err
	}
	var machine struct {
		VCPUs  int `json:"vcpu_count"`
		MemMiB int `json:"mem_size_mib"`
	}
	if err := firecrackerGET(ctx, client, "/machine-config", &machine); err != nil {
		return firecrackerState{ID: info.ID, State: info.State}, fmt.Errorf("%w: %w", errMachineConfigUnavailable, err)
	}
	return firecrackerState{ID: info.ID, State: info.State, VCPUs: machine.VCPUs, MemMiB: machine.MemMiB, MachineConfigs: true}, nil
}

func firecrackerGET(ctx context.Context, client *http.Client, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, adoptionAPITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(out)
}
