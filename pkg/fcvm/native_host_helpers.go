package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const nativeHostHelperScope = "gregale-host-helpers"

// One helper and all its descendants are born into this exclusive cgroup.
// The recorded inode prevents a reused path from authorizing an unrelated kill.
type nativeHostHelperGroup struct {
	Path   string `json:"path"` // relative to the unified cgroup mount
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type nativeHostHelperRecord struct {
	OwnerGeneration string                `json:"owner_generation"`
	Launch          nativeLaunchRecord    `json:"launch"`
	Group           nativeHostHelperGroup `json:"group"`
}

func (r *nativeHostHelperRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"owner_generation", "launch", "group"})
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["group"], []string{"path", "device", "inode"}); err != nil {
		return err
	}
	type plain nativeHostHelperRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func (r nativeHostHelperRecord) validate(owner nativeLaunchRecord) error {
	if err := r.Launch.validate(owner.Lease.Instance); err != nil {
		return err
	}
	if !canonicalNativeHelperID(owner.Generation) || r.OwnerGeneration != owner.Generation || r.Launch.KernelBootID != owner.KernelBootID || r.Launch.Lease != owner.Lease {
		return errors.New("native helper: record belongs to another launch owner")
	}
	if !canonicalNativeHelperID(r.Launch.Generation) || !validNativeHelperGroupPath(r.Group.Path, r.Launch.Generation) || (r.Group.Inode == 0) != (r.Group.Device == 0) || r.Launch.Authorized && r.Group.Inode == 0 {
		return errors.New("native helper: invalid cgroup ownership")
	}
	return nil
}

func canonicalNativeHelperID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validNativeHelperGroupPath(path, id string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, "\\") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return filepath.Base(path) == id && filepath.Base(filepath.Dir(path)) == nativeHostHelperScope
}

// Create publishes a stable cgroup identity before fork. Attach must use
// CLONE_INTO_CGROUP: moving a running helper afterwards leaves a crash window.
// Retire kills and joins descendants before removing the exact owned cgroup.
type nativeHostHelperGroups interface {
	Plan(string) (nativeHostHelperGroup, error)
	Create(nativeHostHelperGroup) (nativeHostHelperGroup, error)
	Attach(*exec.Cmd, nativeHostHelperGroup) (io.Closer, error)
	Retire(context.Context, nativeHostHelperGroup) error
	Removed(nativeHostHelperGroup) error
	Inventory([]nativeHostHelperRecord) error
}

type nativeHostHelperJournal struct {
	owner      *nativeLaunchJournal
	groups     nativeHostHelperGroups
	startTime  func(int) (uint64, error)
	writeValue func(string, nativeHostHelperRecord) error
}

func (j *nativeHostHelperJournal) root(generation string) string {
	return filepath.Join(j.owner.root, "helpers", generation)
}

func (j *nativeHostHelperJournal) path(record nativeHostHelperRecord) string {
	return filepath.Join(j.root(record.OwnerGeneration), record.Launch.Generation+".json")
}

func (j *nativeHostHelperJournal) write(owner nativeLaunchRecord, record nativeHostHelperRecord) error {
	if err := record.validate(owner); err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(j.path(record), record)
	}
	return writeNativeJournalValue(j.path(record), record)
}

func (j *nativeHostHelperJournal) records(owner nativeLaunchRecord) ([]nativeHostHelperRecord, error) {
	root := j.root(owner.Generation)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(filepath.Dir(root), true); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var records []nativeHostHelperRecord
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() {
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !canonicalNativeHelperID(id) || !entry.Type().IsRegular() {
			return nil, errors.New("native helper: unexpected ownership journal entry")
		}
		path := filepath.Join(root, entry.Name())
		if err := checkNativeJournalPath(path, false); err != nil {
			return nil, err
		}
		file, err := openNativeJournalFile(path, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		var record nativeHostHelperRecord
		decoder := json.NewDecoder(file)
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				decodeErr = errors.New("native helper: trailing record data")
			}
		}
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return nil, err
		}
		if record.Launch.Generation != id {
			return nil, errors.New("native helper: record filename differs from its identity")
		}
		if err := record.validate(owner); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func newNativeHostHelperCommand(helper string, argv []string) (*exec.Cmd, error) {
	if !filepath.IsAbs(helper) || len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return nil, errors.New("native helper: resolved helper and executable paths are required")
	}
	args := append([]string{"--launch-host-command", "3", "--"}, argv...)
	cmd := exec.Command(helper, args...)
	cmd.Args[0] = "vmmd-host-helper"
	isolateLifecycleChild(cmd)
	return cmd, nil
}

// The parent's stable lock fences the complete fork/publication/gate window.
// A VM launch revocation also fences provisioning helpers. Cleanup helpers are
// deliberately not enabled yet: they need durable resource provenance first.
func (j *nativeHostHelperJournal) launch(ctx context.Context, expected nativeLaunchRecord, cmd *exec.Cmd) (record nativeHostHelperRecord, started bool, err error) {
	if cmd == nil || !filepath.IsAbs(cmd.Path) || len(cmd.Args) < 5 || cmd.Args[0] != "vmmd-host-helper" || cmd.Args[1] != "--launch-host-command" || cmd.Args[2] != "3" || cmd.Args[3] != "--" || !filepath.IsAbs(cmd.Args[4]) || len(cmd.ExtraFiles) != 0 || j.groups == nil {
		return record, false, errors.New("native helper: command bypasses durable ownership")
	}
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return record, false, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return record, false, err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || owner.Lease != expected.Lease || owner.Revoked || owner.ResourcesRemoved {
		return record, false, errors.New("native helper: launch owner changed or was revoked")
	}
	id := uuid.NewString()
	group, err := j.groups.Plan(id)
	if err != nil {
		return record, false, err
	}
	record = nativeHostHelperRecord{OwnerGeneration: owner.Generation, Group: group, Launch: nativeLaunchRecord{Version: 1, Generation: id, KernelBootID: owner.KernelBootID, Lease: owner.Lease}}
	if err := record.validate(owner); err != nil {
		return record, false, err
	}
	for _, root := range []string{filepath.Dir(j.root(owner.Generation)), j.root(owner.Generation)} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return record, false, err
		}
		if err := checkNativeJournalPath(root, true); err != nil {
			return record, false, err
		}
	}
	// Persist the planned path before creating it. A crash before inode
	// publication can only have an empty group: fork requires the next write.
	if err := j.write(owner, record); err != nil {
		return record, false, err
	}
	record.Group, err = j.groups.Create(group)
	if err != nil {
		return record, false, err
	}
	if record.Group.Path != group.Path || record.Group.Inode == 0 || record.Group.Device == 0 {
		return record, false, errors.New("native helper: cgroup creation changed its planned identity")
	}
	if err := j.write(owner, record); err != nil {
		return record, false, err
	}
	groupFD, err := j.groups.Attach(cmd, record.Group)
	if err != nil {
		return record, false, err
	}
	if groupFD == nil {
		return record, false, errors.New("native helper: cgroup attachment has no pinned descriptor")
	}
	defer func() { err = errors.Join(err, groupFD.Close()) }()
	reader, writer, err := os.Pipe()
	if err != nil {
		return record, false, err
	}
	defer reader.Close()
	defer writer.Close()
	cmd.ExtraFiles = []*os.File{reader}
	if err := ctx.Err(); err != nil {
		return record, false, err
	}
	if err := cmd.Start(); err != nil {
		return record, false, err
	}
	started = true
	_ = reader.Close()
	startTime := j.startTime
	if startTime == nil {
		startTime = nativeHostHelperStartTime
	}
	start, err := startTime(cmd.Process.Pid)
	if err != nil {
		return record, true, err
	}
	if err := ctx.Err(); err != nil {
		return record, true, err
	}
	record.Launch.Authorized, record.Launch.PID, record.Launch.StartTime = true, cmd.Process.Pid, start
	if err := j.write(owner, record); err != nil {
		return record, true, err
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		return record, true, fmt.Errorf("native helper: release execution gate: %w", err)
	}
	return record, true, nil
}

// Retirement rereads durable ownership, then revokes before killing. Caller
// cancellation or an uncertain kernel acknowledgement never erases the frame.
func (j *nativeHostHelperJournal) retire(ctx context.Context, expected nativeLaunchRecord, id string) (err error) {
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || owner.Lease != expected.Lease {
		return errors.New("native helper: retirement owner changed")
	}
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Launch.Generation != id {
			continue
		}
		if j.groups == nil {
			return errors.New("native helper: kernel retirement is unavailable")
		}
		if record.Launch.ResourcesRemoved {
			return j.groups.Removed(record.Group)
		}
		if !record.Launch.Revoked {
			record.Launch.Revoked = true
			if err := j.write(owner, record); err != nil {
				return err
			}
		}
		if err := j.groups.Retire(ctx, record.Group); err != nil {
			return err
		}
		record.Launch.ExitConfirmed, record.Launch.ResourcesRemoved = true, true
		return j.write(owner, record)
	}
	return errors.New("native helper: retirement has no durable command frame")
}

func (j *nativeHostHelperJournal) retireAll(ctx context.Context, owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := j.retire(ctx, owner, record.Launch.Generation); err != nil {
			return err
		}
	}
	return nil
}

// Called with the parent's producer lock already held. Resource acknowledgement
// and fallback replacement cannot outrun an unfinished helper frame.
func (j *nativeHostHelperJournal) requireRetired(owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !record.Launch.Revoked || !record.Launch.ExitConfirmed || !record.Launch.ResourcesRemoved {
			return errors.New("native helper: command retirement remains uncertain")
		}
	}
	return nil
}

func (j *nativeHostHelperJournal) requireRemoved(owner nativeLaunchRecord) error {
	if err := j.requireRetired(owner); err != nil {
		return err
	}
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if j.groups == nil {
			return errors.New("native helper: kernel resource proof is unavailable")
		}
		if err := j.groups.Removed(record.Group); err != nil {
			return err
		}
	}
	return nil
}

// Inspect current and immutable archived generations. Unknown helper owners,
// corrupt frames and changed kernel boots cannot disappear from recovery.
func (j *nativeHostHelperJournal) allRecords(ctx context.Context, current []nativeLaunchRecord) ([]nativeHostHelperRecord, error) {
	root := filepath.Join(j.owner.root, "helpers")
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return nil, err
	}
	owners := make(map[string]nativeLaunchRecord, len(current))
	for _, owner := range current {
		owners[owner.Generation] = owner
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var records []nativeHostHelperRecord
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !canonicalNativeHelperID(entry.Name()) {
			return nil, errors.New("native helper: unexpected generation directory")
		}
		owner, ok := owners[entry.Name()]
		if !ok {
			owner, err = j.archivedOwner(entry.Name())
			if err != nil {
				return nil, err
			}
		}
		lock, err := j.owner.lock(ctx, owner.Lease.Instance)
		if err != nil {
			return nil, err
		}
		frames, readErr := j.records(owner)
		if err := errors.Join(readErr, lock.Close()); err != nil {
			return nil, err
		}
		if owner.ResourcesRemoved || !ok {
			for _, frame := range frames {
				if !frame.Launch.ResourcesRemoved {
					return nil, errors.New("native helper: finished VM ownership retains an unfinished helper")
				}
			}
		}
		records = append(records, frames...)
	}
	return records, nil
}

func (j *nativeHostHelperJournal) archivedOwner(generation string) (record nativeLaunchRecord, err error) {
	archive := filepath.Join(j.owner.root, "retired")
	if err := checkNativeJournalPath(archive, true); err != nil {
		return record, err
	}
	path := filepath.Join(archive, generation+".json")
	if err := checkNativeJournalPath(path, false); err != nil {
		return record, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, errors.New("native helper: trailing owner archive data")
	}
	if err := record.validate(record.Lease.Instance); err != nil {
		return record, err
	}
	bootID, err := j.owner.currentBootID()
	if err != nil {
		return record, err
	}
	if record.Generation != generation || record.KernelBootID != bootID || !record.Revoked || !record.ExitConfirmed {
		return record, errors.New("native helper: immutable launch owner differs from helper provenance")
	}
	return record, nil
}

// Always install the single Wait owner once fork succeeds, even if publication
// fails. Draining the cgroup joins descendants that outlive the command leader.
func (j *nativeHostHelperJournal) run(ctx context.Context, owner nativeLaunchRecord, cmd *exec.Cmd, cleanupBudget time.Duration) error {
	record, started, launchErr := j.launch(ctx, owner, cmd)
	var done chan error
	if started {
		done = make(chan error, 1)
		go func() { done <- cmd.Wait() }()
	}
	var waitErr error
	if started && launchErr == nil {
		select {
		case waitErr = <-done:
			done = nil
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	if record.Launch.Generation == "" {
		return errors.Join(launchErr, waitErr)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupBudget)
	defer cancel()
	retireErr := j.retire(cleanupCtx, owner, record.Launch.Generation)
	if done != nil {
		select {
		case joinedErr := <-done:
			waitErr = errors.Join(waitErr, joinedErr)
		case <-cleanupCtx.Done():
			waitErr = errors.Join(waitErr, cleanupCtx.Err())
		}
	}
	return errors.Join(launchErr, waitErr, retireErr)
}

// This covers jail setup now. Other mounts, network commands, materialisation
// and bind provenance still need this protocol before restart cleanup is safe.
func (v *JailerVMM) runHostHelper(ctx context.Context, instance string, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("native helper: empty command")
	}
	if v.nativeRecovery == nil {
		return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	}
	r := v.nativeRecovery
	owner, err := r.journal.read(instance)
	if err != nil {
		return nil, err
	}
	if r.generation(instance) != owner.Generation {
		return nil, errors.New("native helper: instance has no local command producer")
	}
	executable, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	argv = append([]string{executable}, argv[1:]...)
	helper := r.helper
	if helper == "" {
		helper, err = v.ensureMountHelper()
		if err != nil {
			return nil, err
		}
	}
	cmd, err := newNativeHostHelperCommand(helper, argv)
	if err != nil {
		return nil, err
	}
	var output nativeHostHelperOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	budget := v.destroyWait
	if budget <= 0 {
		budget = 10 * time.Minute // same lifecycle cleanup fallback as killNative
	}
	j := nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups, startTime: r.startTime}
	err = j.run(ctx, owner, cmd, budget)
	return output.Bytes(), err
}

// A timed out, unjoinable helper retains its watchdog and ownership. Snapshot
// output under a mutex so returning that error cannot race an outstanding pipe.
type nativeHostHelperOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *nativeHostHelperOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *nativeHostHelperOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}
