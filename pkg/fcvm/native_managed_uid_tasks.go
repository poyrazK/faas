package fcvm

// adr: 769
import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type NativeRuntimeTask struct {
	PID, UID  int
	StartTime uint64
}

// Scan every jail UID independently of argv. An unrecognized executable using
// a managed UID cannot disappear behind the Firecracker/jailer name filter.
func (p nativeProcessProbe) managedUIDTasks(ctx context.Context) ([]NativeRuntimeTask, error) {
	read := p.readFile
	if read == nil {
		read = os.ReadFile
	}
	root, err := os.Stat(p.root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, err
	}
	var tasks []NativeRuntimeTask
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		if pid > math.MaxInt32 || strconv.Itoa(pid) != entry.Name() {
			return nil, errors.New("native inventory: noncanonical process directory")
		}
		dir := filepath.Join(p.root, entry.Name())
		status, err := read(filepath.Join(dir, "status"))
		if errors.Is(err, os.ErrNotExist) {
			if _, gone := os.Stat(dir); errors.Is(gone, os.ErrNotExist) {
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		uid, managed, err := nativeManagedStatusUID(string(status))
		if err != nil {
			return nil, err
		}
		if !managed {
			continue
		}
		stat, err := read(filepath.Join(dir, "stat"))
		if err != nil {
			return nil, err // A disappearing managed task makes this sample unknown.
		}
		start, err := nativeProcessStartTime(string(stat), pid)
		if err != nil {
			return nil, err
		}
		current, err := read(filepath.Join(dir, "status"))
		if err != nil || string(current) != string(status) {
			// Only UID needs equality; process statistics in status naturally vary.
			currentUID, currentManaged, parseErr := nativeManagedStatusUID(string(current))
			if err != nil || parseErr != nil || !currentManaged || currentUID != uid {
				return nil, errors.Join(err, parseErr, errors.New("native inventory: managed task UID changed"))
			}
		}
		currentStat, err := read(filepath.Join(dir, "stat"))
		if err != nil {
			return nil, err
		}
		currentStart, err := nativeProcessStartTime(string(currentStat), pid)
		if err != nil || currentStart != start {
			return nil, errors.Join(err, errors.New("native inventory: managed task incarnation changed"))
		}
		tasks = append(tasks, NativeRuntimeTask{PID: pid, UID: uid, StartTime: start})
	}
	currentRoot, err := os.Stat(p.root)
	if err != nil || !os.SameFile(root, currentRoot) {
		return nil, errors.Join(err, errors.New("native inventory: process root changed during scan"))
	}
	slices.SortFunc(tasks, func(a, b NativeRuntimeTask) int { return a.PID - b.PID })
	return tasks, ctx.Err()
}

// Ordinary setuid host processes may have mixed UID tuples. Inspect every UID
// field first; only a tuple touching the managed range needs native equality.
func nativeManagedStatusUID(status string) (int, bool, error) {
	var tuple []string
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Uid:" {
			continue
		}
		if tuple != nil || len(fields) != 5 {
			return 0, false, errors.New("native inventory: ambiguous process UID tuple")
		}
		tuple = fields[1:]
	}
	if tuple == nil {
		return 0, false, errors.New("native inventory: missing process UID tuple")
	}
	managed := false
	for _, field := range tuple {
		uid, err := strconv.ParseUint(field, 10, 32)
		if err != nil || strings.Trim(field, "0123456789") != "" {
			return 0, false, errors.New("native inventory: invalid process UID tuple")
		}
		managed = managed || uid >= JailUIDBase && uid <= JailUIDMax
	}
	if !managed {
		return 0, false, nil
	}
	uid, err := nativeStatusUID(status)
	return uid, true, err
}

func (p nativeProcessProbe) requireManagedUIDProvenance(ctx context.Context) error {
	tasks, err := p.managedUIDTasks(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		process, found, err := p.process(task.PID)
		if err != nil || !found || process.StartTime != task.StartTime || process.ObservedUID != task.UID {
			return errors.Join(err, fmt.Errorf("native recovery: managed UID task %d has no stable native executable provenance", task.PID))
		}
	}
	return ctx.Err()
}
