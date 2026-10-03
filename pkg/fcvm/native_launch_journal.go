package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/google/uuid"
)

// The journal is outside the tenant chroot, on the same host-lifetime tmpfs.
// Records survive daemon death; host reboot destroys both records and VMs.
// A prepared record has no authorized process. An authorized record binds one
// exact PID/start-time incarnation; revocation is irreversible for that token.
type nativeLaunchRecord struct {
	Version          int    `json:"version"`
	Generation       string `json:"generation"`
	KernelBootID     string `json:"kernel_boot_id"`
	Lease            Lease  `json:"lease"`
	Authorized       bool   `json:"authorized"`
	PID              int    `json:"pid"`
	StartTime        uint64 `json:"start_time"`
	Revoked          bool   `json:"revoked"`
	ExitConfirmed    bool   `json:"exit_confirmed"`
	ResourcesRemoved bool   `json:"resources_removed"`
}

type nativeLaunchJournal struct {
	root         string
	loopMounts   nativeLoopMountBackend
	imageSources nativeImageSourceBackend
	tunBinds     nativeTunBindBackend
	jailDevices  nativeJailDeviceBackend
	// Native records cannot survive into another kernel incarnation even if
	// an operator places the journal on a persistent filesystem.
	bootID func() (string, error)
	// Tests inject write failure before the child receives authorization.
	writeRecord func(string, nativeLaunchRecord) error
}

// False/zero fields are still required. Missing revocation or authorization
// cannot turn a damaged record into fresh boot authority. The lease has the
// exact canonical fields emitted by this journal version, including zeroes.
func (r *nativeLaunchRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"version", "generation", "kernel_boot_id", "lease", "authorized", "pid", "start_time", "revoked", "exit_confirmed", "resources_removed"})
	if err != nil {
		return err
	}
	var leaseFields []string
	leaseType := reflect.TypeOf(Lease{})
	for i := 0; i < leaseType.NumField(); i++ {
		field := leaseType.Field(i)
		if field.IsExported() {
			leaseFields = append(leaseFields, field.Name)
		}
	}
	if _, err := nativeJournalObjectFields(fields["lease"], leaseFields); err != nil {
		return fmt.Errorf("native journal: lease fields: %w", err)
	}
	type plain nativeLaunchRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func nativeJournalObjectFields(data []byte, required []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("native journal: expected record object")
	}
	allowed := make(map[string]bool, len(required))
	for _, key := range required {
		allowed[key] = true
	}
	fields := make(map[string]json.RawMessage, len(required))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || fields[key] != nil {
			return nil, errors.New("native journal: unknown or duplicate record field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("native journal: null record field")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) || len(fields) != len(required) {
		return nil, errors.New("native journal: incomplete or trailing record data")
	}
	return fields, nil
}

type nativeLaunchTicket struct {
	journal *nativeLaunchJournal
	record  nativeLaunchRecord
	lock    *os.File
}

func (j *nativeLaunchJournal) path(instance string) string {
	return filepath.Join(j.root, instance+".json")
}

func (j *nativeLaunchJournal) lock(ctx context.Context, instance string) (*os.File, error) {
	if !validNativeInstanceName(instance) {
		return nil, errors.New("native journal: invalid instance identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return nil, fmt.Errorf("native journal: create private root: %w", err)
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return nil, err
	}
	return lockNativeJournalFile(ctx, filepath.Join(j.root, instance+".lock"))
}

func (j *nativeLaunchJournal) prepare(ctx context.Context, lease Lease) (err error) {
	if err := validateNativeJournalLease(lease); err != nil {
		return err
	}
	bootID, err := j.currentBootID()
	if err != nil {
		return err
	}
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if _, err := os.Lstat(j.path(lease.Instance)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("native journal: instance %s already has a launch record", lease.Instance)
	}
	return j.write(nativeLaunchRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: bootID, Lease: lease})
}

func (j *nativeLaunchJournal) currentBootID() (string, error) {
	get := j.bootID
	if get == nil {
		get = nativeKernelBootID
	}
	value, err := get()
	if err != nil {
		return "", fmt.Errorf("native journal: read kernel boot identity: %w", err)
	}
	bootID, err := uuid.Parse(value)
	if err != nil || bootID == uuid.Nil {
		return "", errors.New("native journal: kernel boot identity is invalid")
	}
	return bootID.String(), nil
}

// The file lock spans fork, incarnation publication AND gate opening. A second
// vmmd cannot revoke and acknowledge an empty record while a producer is still
// able to authorize its child. Process death releases the lock and closes the
// gate writer. Lock files are kept stable; unlinking them permits split locks.
func (j *nativeLaunchJournal) beginLaunch(ctx context.Context, lease Lease) (*nativeLaunchTicket, error) {
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return nil, err
	}
	record, err := j.read(lease.Instance)
	if err != nil || !sameNativeJournalLease(record.Lease, lease) || record.Revoked || record.Authorized {
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("native journal: launch record is not available to this lease")
	}
	loops := nativeLoopMountJournal{owner: j, backend: j.loopMounts}
	if err := loops.requireRetired(record); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if j.loopMounts != nil {
		if err := loops.requireRemoved(record); err != nil {
			return nil, errors.Join(err, lock.Close())
		}
	}
	images := nativeImageSourceJournal{owner: j, backend: j.imageSources}
	if err := images.require(ctx, record, false); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	tun := nativeTunBindJournal{owner: j, backend: j.tunBinds}
	if err := tun.require(record, false); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return &nativeLaunchTicket{journal: j, record: record, lock: lock}, nil
}

func (t *nativeLaunchTicket) authorize(pid int, startTime uint64) error {
	if t.lock == nil || t.record.Authorized || pid <= 0 || startTime == 0 {
		return errors.New("native journal: invalid launch authorization")
	}
	record := t.record
	record.Authorized, record.PID, record.StartTime = true, pid, startTime
	if err := t.journal.write(record); err != nil {
		return err
	}
	t.record = record
	return nil
}

func (t *nativeLaunchTicket) close() error {
	if t.lock == nil {
		return nil
	}
	lock := t.lock
	t.lock = nil
	return lock.Close()
}

// launch requires a release-matched --launch-jailer helper command. A true
// started result, even with error, obliges the caller to install its watchdog
// and join the child's exit. Failure always closes the gate before returning.
func (j *nativeLaunchJournal) launch(ctx context.Context, lease Lease, cmd *exec.Cmd, startTime func(int) (uint64, error)) (started bool, err error) {
	if err := j.validateLaunchCommand(lease, cmd); err != nil {
		return false, err
	}
	if len(cmd.ExtraFiles) != 0 {
		return false, errors.New("native journal: launch command already has inherited files")
	}
	ticket, err := j.beginLaunch(ctx, lease)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, ticket.close()) }()
	reader, writer, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer func() { _ = reader.Close() }() // The parent closes its inherited read end immediately after Start.
	defer func() { err = errors.Join(err, writer.Close()) }()
	cmd.ExtraFiles = []*os.File{reader}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	started = true
	if err := reader.Close(); err != nil {
		return true, err
	}
	if startTime == nil {
		startTime = func(pid int) (uint64, error) {
			raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			if err != nil {
				return 0, err
			}
			return nativeProcessStartTime(string(raw), pid)
		}
	}
	start, err := startTime(cmd.Process.Pid)
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if err := ticket.authorize(cmd.Process.Pid, start); err != nil {
		return true, err
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		return true, fmt.Errorf("native journal: release child gate: %w", err)
	}
	return true, nil
}

// No direct Firecracker command or daemonizing jailer may bypass the gate. The
// caller resolves the helper from the exact vmmd release before constructing
// this command; argv[0] remains recognizable across the shared tmpfs copy.
func newNativeLaunchCommand(helper string, jailerArgs []string) (*exec.Cmd, error) {
	if !filepath.IsAbs(helper) || len(jailerArgs) == 0 || !filepath.IsAbs(jailerArgs[0]) || !nativeExecutableName(filepath.Base(jailerArgs[0]), "jailer") {
		return nil, errors.New("native journal: launch requires resolved helper and jailer paths")
	}
	args := append([]string{"--launch-jailer", "3", "--"}, jailerArgs...)
	cmd := exec.Command(helper, args...)
	cmd.Args[0] = "vmmd-jail-helper"
	return cmd, nil
}

func (j *nativeLaunchJournal) validateLaunchCommand(lease Lease, cmd *exec.Cmd) error {
	if cmd == nil || len(cmd.Args) < 5 || cmd.Args[0] != "vmmd-jail-helper" {
		return errors.New("native journal: launch command bypasses the helper")
	}
	id, jailer, err := nativeCommandInstance(cmd.Args)
	if err != nil || !jailer || id != lease.Instance {
		return errors.New("native journal: launch command instance differs from its lease")
	}
	args := cmd.Args[4:]
	uid, uidOK := nativeCommandArgument(args, "--uid")
	gid, gidOK := nativeCommandArgument(args, "--gid")
	base, baseOK := nativeCommandArgument(args, "--chroot-base-dir")
	parent, parentOK := nativeCommandArgument(args, "--parent-cgroup")
	executable, executableOK := nativeCommandArgument(args, "--exec-file")
	version, versionOK := nativeCommandArgument(args, "--cgroup-version")
	wantParent := ParentCgroupFor(lease.Plan)
	if lease.IsBuilder {
		wantParent = BuilderCgroupParent
	}
	if !uidOK || uid != strconv.Itoa(lease.UID) || !gidOK || gid != strconv.Itoa(lease.GID) || !baseOK || filepath.Clean(base) != filepath.Dir(j.root) || !parentOK || parent != wantParent || !executableOK || !filepath.IsAbs(executable) || !nativeExecutableName(filepath.Base(executable), "firecracker") || !versionOK || version != "2" {
		return errors.New("native journal: launch command provenance differs from its lease")
	}
	netns, netnsOK := nativeCommandArgument(args, "--netns")
	if !lease.Networkless && (!netnsOK || netns != filepath.Join("/run/netns", lease.Netns)) {
		return errors.New("native journal: launch command namespace differs from its lease")
	}
	for _, arg := range args {
		if arg == "--daemonize" || arg == "--new-pid-ns" || lease.Networkless && arg == "--netns" {
			return errors.New("native journal: launch command cannot fork or change network ownership")
		}
	}
	return nil
}

func (j *nativeLaunchJournal) revoke(ctx context.Context, instance string) (record nativeLaunchRecord, err error) {
	lock, err := j.lock(ctx, instance)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err = j.read(instance)
	if err != nil {
		return record, err
	}
	if !record.Revoked {
		record.Revoked = true
		err = j.write(record)
	}
	return record, err
}

// Confirmation cannot manufacture a receipt from an unknown ID or a newer
// incarnation. The caller owes a pinned kernel exit acknowledgement first.
func (j *nativeLaunchJournal) confirmExit(ctx context.Context, expected nativeLaunchRecord) (err error) {
	lock, err := j.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err := j.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if !record.Revoked || record.Generation != expected.Generation || record.KernelBootID != expected.KernelBootID || record.PID != expected.PID || record.StartTime != expected.StartTime || !sameNativeJournalLease(record.Lease, expected.Lease) {
		return errors.New("native journal: exit confirmation identity changed")
	}
	record.ExitConfirmed = true
	return j.write(record)
}

// retire first closes the durable producer fence, then confirms the bound
// kernel task's exit. A complete scan must also exclude an unmatched duplicate
// with the same instance ID. It retains the record on every uncertain outcome.
// Resource cleanup and attempt-bound scheduler evidence remain separate work.
func (j *nativeLaunchJournal) retire(ctx context.Context, instance string, retirer nativeProcessRetirer) (nativeLaunchRecord, error) {
	record, err := j.revoke(ctx, instance)
	if err != nil {
		return record, err
	}
	if record.Authorized && !record.ExitConfirmed {
		plan := record.Lease.Plan
		if record.Lease.IsBuilder {
			plan = ""
		}
		target := nativeProcessIdentity{PID: record.PID, Instance: instance, UID: record.Lease.UID, StartTime: record.StartTime, Plan: plan, IsBuilder: record.Lease.IsBuilder}
		if err := retirer.stopProcess(ctx, target); err != nil {
			return record, err
		}
	}
	processes, err := retirer.probe.processes(ctx)
	if err != nil {
		return record, err
	}
	for _, process := range processes {
		if process.Instance == instance {
			return record, errors.New("native journal: instance still has an unmatched kernel task")
		}
	}
	if err := j.confirmExit(ctx, record); err != nil {
		return record, err
	}
	record.ExitConfirmed = true
	return record, nil
}

func (j *nativeLaunchJournal) read(instance string) (record nativeLaunchRecord, err error) {
	if !validNativeInstanceName(instance) {
		return record, errors.New("native journal: invalid instance identity")
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return record, err
	}
	record, err = readNativeLaunchRecord(j.path(instance), instance)
	if err != nil {
		return record, err
	}
	bootID, err := j.currentBootID()
	if err != nil {
		return record, err
	}
	if record.KernelBootID != bootID {
		return record, errors.New("native journal: record belongs to another kernel boot")
	}
	return record, nil
}

func readNativeLaunchRecord(path, instance string) (record nativeLaunchRecord, err error) {
	if err := checkNativeJournalPath(path, false); err != nil {
		return record, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, fmt.Errorf("native journal: read record: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, errors.New("native journal: trailing record data")
	}
	if err := record.validate(instance); err != nil {
		return record, err
	}
	return record, nil
}

func (j *nativeLaunchJournal) write(record nativeLaunchRecord) error {
	if err := record.validate(record.Lease.Instance); err != nil {
		return err
	}
	if j.writeRecord != nil {
		return j.writeRecord(j.path(record.Lease.Instance), record)
	}
	return writeNativeLaunchRecord(j.path(record.Lease.Instance), record)
}

func (r nativeLaunchRecord) validate(instance string) error {
	if err := validateNativeJournalLease(r.Lease); err != nil {
		return err
	}
	if r.Version != 1 || r.Lease.Instance != instance {
		return errors.New("native journal: record version or instance is invalid")
	}
	if generation, err := uuid.Parse(r.Generation); err != nil || generation == uuid.Nil {
		return errors.New("native journal: record generation is invalid")
	}
	if bootID, err := uuid.Parse(r.KernelBootID); err != nil || bootID == uuid.Nil {
		return errors.New("native journal: record kernel boot identity is invalid")
	}
	if r.Authorized && (r.PID <= 0 || r.PID > math.MaxInt32 || r.StartTime == 0) || !r.Authorized && (r.PID != 0 || r.StartTime != 0) || r.ExitConfirmed && !r.Revoked || r.ResourcesRemoved && !r.ExitConfirmed {
		return errors.New("native journal: launch state is inconsistent")
	}
	return nil
}

func validateNativeJournalLease(l Lease) error {
	if !validNativeInstanceName(l.Instance) || l.Slot < 0 || l.Slot >= MaxSlots || l.UID != JailUIDBase+l.Slot || l.GID != l.UID || !l.HostIP.Is4() || l.Netns != "fc-"+l.Instance || l.VethHost != fmt.Sprintf("vh%d", l.Slot) || l.VethPeer != fmt.Sprintf("vp%d", l.Slot) || l.Plan != "" && !l.Plan.Valid() {
		return errors.New("native journal: lease identity is inconsistent")
	}
	return nil
}

// sameNativePhysicalLease compares every durable field. processGeneration is
// a local callback fence, deliberately absent from disk and native authority.
func sameNativePhysicalLease(a, b Lease) bool {
	a.processGeneration = 0
	b.processGeneration = 0
	return a == b
}

func sameNativeJournalLease(a, b Lease) bool {
	return a.Instance == b.Instance && a.Slot == b.Slot && a.UID == b.UID && a.GID == b.GID &&
		a.Plan == b.Plan && a.IsBuilder == b.IsBuilder && a.Networkless == b.Networkless &&
		a.Netns == b.Netns && a.VethHost == b.VethHost && a.VethPeer == b.VethPeer && a.HostIP == b.HostIP
}

func writeNativeLaunchRecord(path string, record nativeLaunchRecord) (err error) {
	return writeNativeJournalValue(path, record)
}

func writeNativeJournalValue(path string, record any) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".launch-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.Sync()
}
