package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Recovery identifies the kernel incarnation, not merely a numeric PID or a
// missing in-memory record. It keeps every match, including duplicate IDs.
type nativeProcessIdentity struct {
	PID         int
	Instance    string
	UID         int
	ObservedUID int
	StartTime   uint64
	Jailer      bool
	Plan        api.Plan
	IsBuilder   bool
}

func (p nativeProcessIdentity) lease() Lease {
	lease := leaseForSlot(p.Instance, p.UID-JailUIDBase)
	lease.Plan, lease.IsBuilder = p.Plan, p.IsBuilder
	return lease
}

type nativeProcessProbe struct {
	root       string
	chrootBase string
	readFile   func(string) ([]byte, error)
}

func (p nativeProcessProbe) processes(ctx context.Context) ([]nativeProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, fmt.Errorf("native recovery: read process root: %w", err)
	}
	var processes []nativeProcessIdentity
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		identity, found, err := p.process(pid)
		if err != nil {
			return nil, fmt.Errorf("native recovery: inspect process %d: %w", pid, err)
		}
		if found {
			processes = append(processes, identity)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.root); err != nil {
		return nil, fmt.Errorf("native recovery: process root disappeared during scan: %w", err)
	}
	return processes, nil
}

func (p nativeProcessProbe) process(pid int) (nativeProcessIdentity, bool, error) {
	identity, found, err := p.readProcess(pid)
	// A process disappearing while inspected is a normal exit. Missing the
	// process root or being unable to inspect a surviving process is unknown.
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Join(p.root, strconv.Itoa(pid))); errors.Is(statErr, os.ErrNotExist) {
			return nativeProcessIdentity{}, false, nil
		}
	}
	return identity, found, err
}

func (p nativeProcessProbe) readProcess(pid int) (nativeProcessIdentity, bool, error) {
	read := p.readFile
	if read == nil {
		read = os.ReadFile
	}
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	raw, err := read(filepath.Join(dir, "cmdline"))
	if err != nil {
		return nativeProcessIdentity{}, false, err
	}
	args := strings.Split(string(raw), "\x00")
	instance, jailer, err := nativeCommandInstance(args)
	if err != nil || instance == "" {
		return nativeProcessIdentity{}, false, err
	}
	status, err := read(filepath.Join(dir, "status"))
	if err != nil {
		return nativeProcessIdentity{}, false, err
	}
	uid, err := nativeStatusUID(string(status))
	if err != nil {
		return nativeProcessIdentity{}, false, err
	}
	identity := nativeProcessIdentity{PID: pid, Instance: instance, UID: uid, ObservedUID: uid, Jailer: jailer}
	if jailer && uid == 0 {
		value, ok := nativeCommandArgument(args, "--uid")
		if !ok {
			return identity, false, errors.New("root jailer has no unique target UID")
		}
		identity.UID, err = strconv.Atoi(value)
		if err != nil {
			return identity, false, errors.New("root jailer target UID is invalid")
		}
		base, ok := nativeCommandArgument(args, "--chroot-base-dir")
		executable, executableOK := nativeCommandArgument(args, "--exec-file")
		if !ok || filepath.Clean(base) != filepath.Clean(p.chrootBase) || !executableOK || !nativeExecutableName(filepath.Base(executable), "firecracker") {
			return identity, false, errors.New("root jailer provenance is unknown")
		}
		parent, ok := nativeCommandArgument(args, "--parent-cgroup")
		if !ok {
			return identity, false, errors.New("root jailer cgroup ownership is unknown")
		}
		var owned bool
		identity.Plan, identity.IsBuilder, owned = nativeParentPlan(parent)
		if !owned {
			return identity, false, errors.New("root jailer cgroup is not managed")
		}
	} else {
		cgroup, err := read(filepath.Join(dir, "cgroup"))
		if err != nil {
			return identity, false, err
		}
		var owned bool
		identity.Plan, identity.IsBuilder, owned = nativeProcessCgroup(string(cgroup), instance)
		if !owned {
			return identity, false, errors.New("native process cgroup ownership is unknown")
		}
	}
	if identity.UID < JailUIDBase || identity.UID > JailUIDMax {
		return identity, false, errors.New("native process UID is outside the jail range")
	}
	stat, err := read(filepath.Join(dir, "stat"))
	if err != nil {
		return identity, false, err
	}
	identity.StartTime, err = nativeProcessStartTime(string(stat), pid)
	return identity, err == nil, err
}

func nativeExecutableName(name, binary string) bool {
	return name == binary || strings.HasPrefix(name, binary+"-v")
}

func nativeCommandInstance(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	name := filepath.Base(args[0])
	if (name == "vmmd" || name == "vmmd-jail-helper") && len(args) > 1 && args[1] == "--launch-jailer" {
		if len(args) < 5 || args[2] != "3" || args[3] != "--" || !filepath.IsAbs(args[4]) || !nativeExecutableName(filepath.Base(args[4]), "jailer") {
			return "", true, errors.New("native launch helper command is ambiguous")
		}
		args, name = args[4:], filepath.Base(args[4])
	}
	jailer := nativeExecutableName(name, "jailer")
	if !jailer && !nativeExecutableName(name, "firecracker") {
		return "", false, nil
	}
	instance, ok := nativeCommandArgument(args, "--id")
	if !ok {
		for _, arg := range args {
			if arg == "--id" {
				return "", jailer, errors.New("native process instance argument is ambiguous")
			}
		}
		// Version/utility invocations have no VM identity.
		return "", jailer, nil
	}
	if !validNativeInstanceName(instance) {
		return "", jailer, errors.New("native process instance name is invalid")
	}
	return instance, jailer, nil
}

func nativeCommandArgument(args []string, flag string) (string, bool) {
	value, count := "", 0
	for i := 1; i < len(args); i++ {
		if args[i] == flag {
			if i+1 == len(args) || args[i+1] == "" {
				return "", false
			}
			value, count = args[i+1], count+1
		}
	}
	return value, count == 1
}

func validNativeInstanceName(instance string) bool {
	if instance == "" {
		return false
	}
	for _, c := range instance {
		if c != '-' && c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func nativeStatusUID(status string) (int, error) {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Uid:" {
			continue
		}
		if len(fields) != 5 || fields[1] != fields[2] || fields[1] != fields[3] || fields[1] != fields[4] {
			return 0, errors.New("native process UID tuple is ambiguous")
		}
		uid, err := strconv.Atoi(fields[1])
		if err != nil || uid < 0 {
			return 0, errors.New("native process UID is invalid")
		}
		return uid, nil
	}
	return 0, errors.New("native process UID is missing")
}

func nativeProcessStartTime(stat string, pid int) (uint64, error) {
	left, right := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if left <= 0 || right <= left {
		return 0, errors.New("native process stat is malformed")
	}
	observed, err := strconv.Atoi(strings.TrimSpace(stat[:left]))
	fields := strings.Fields(stat[right+1:])
	if err != nil || observed != pid || len(fields) < 20 {
		return 0, errors.New("native process stat incarnation is incomplete")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return 0, errors.New("native process start time is invalid")
	}
	return start, nil
}

func nativeParentPlan(parent string) (api.Plan, bool, bool) {
	parent = strings.TrimPrefix(parent, "/")
	if parent == BuilderCgroupParent {
		return "", true, true
	}
	if parent == defaultParentCgroup {
		return "", false, true
	}
	for _, plan := range api.Plans {
		if parent == ParentCgroupFor(plan) || parent == LegacyParentCgroupFor(plan) {
			return plan, false, true
		}
	}
	return "", false, false
}

func nativeProcessCgroup(cgroup, instance string) (api.Plan, bool, bool) {
	for _, line := range strings.Split(cgroup, "\n") {
		path, ok := strings.CutPrefix(line, "0::/")
		if !ok || filepath.Base(path) != instance {
			continue
		}
		return nativeParentPlan(filepath.Dir(path))
	}
	return "", false, false
}
