package fcvm

// adr: 652
import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// These are retained original helper leaders, even after exec or cgroup escape.
// They are observations, never producer authority or proof of all descendants.
type NativeRuntimeHelperTask struct {
	Instance, OwnerGeneration, HelperGeneration string
	PID, ObservedUID                            int
	StartTime                                   uint64
	ExpectedCgroup, ObservedCgroup              string
	RetirementClaimed                           bool
}

func (p nativeProcessProbe) helperTasks(ctx context.Context, records []nativeHostHelperRecord) ([]NativeRuntimeHelperTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.Stat(p.root)
	if err != nil {
		return nil, err
	}
	first, err := p.readHelperTasks(ctx, records)
	if err != nil {
		return nil, err
	}
	second, err := p.readHelperTasks(ctx, records)
	if err != nil || !slices.Equal(first, second) {
		return nil, errors.Join(err, errors.New("native inventory: original helper task set changed"))
	}
	current, err := os.Stat(p.root)
	if err != nil || !os.SameFile(root, current) {
		return nil, errors.Join(err, errors.New("native inventory: helper process root changed"))
	}
	return first, ctx.Err()
}

func (p nativeProcessProbe) readHelperTasks(ctx context.Context, records []nativeHostHelperRecord) ([]NativeRuntimeHelperTask, error) {
	var result []NativeRuntimeHelperTask
	seen := make(map[int]bool)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !record.Launch.Authorized {
			continue
		}
		task, found, err := p.readHelperTask(record)
		if errors.Is(err, os.ErrNotExist) {
			if _, gone := os.Stat(filepath.Join(p.root, strconv.Itoa(record.Launch.PID))); errors.Is(gone, os.ErrNotExist) {
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		if found {
			if seen[task.PID] {
				return nil, errors.New("native inventory: duplicate original helper task ownership")
			}
			seen[task.PID] = true
			result = append(result, task)
		}
	}
	slices.SortFunc(result, func(a, b NativeRuntimeHelperTask) int { return cmp.Compare(a.PID, b.PID) })
	return result, ctx.Err()
}

func (p nativeProcessProbe) readHelperTask(record nativeHostHelperRecord) (NativeRuntimeHelperTask, bool, error) {
	read := p.readFile
	if read == nil {
		read = os.ReadFile
	}
	dir := filepath.Join(p.root, strconv.Itoa(record.Launch.PID))
	stat, err := read(filepath.Join(dir, "stat"))
	if err != nil {
		return NativeRuntimeHelperTask{}, false, err
	}
	start, err := nativeProcessStartTime(string(stat), record.Launch.PID)
	if err != nil || start != record.Launch.StartTime {
		return NativeRuntimeHelperTask{}, false, err // A reused PID is not the original helper.
	}
	uid, group, err := nativeHelperTaskScope(read, dir)
	if err != nil {
		return NativeRuntimeHelperTask{}, false, err
	}
	after, err := read(filepath.Join(dir, "stat"))
	if err != nil {
		return NativeRuntimeHelperTask{}, false, err
	}
	current, err := nativeProcessStartTime(string(after), record.Launch.PID)
	if err != nil || current != start {
		return NativeRuntimeHelperTask{}, false, errors.Join(err, errors.New("native inventory: helper incarnation changed"))
	}
	currentUID, currentGroup, err := nativeHelperTaskScope(read, dir)
	if err != nil || uid != currentUID || group != currentGroup {
		return NativeRuntimeHelperTask{}, false, errors.Join(err, errors.New("native inventory: helper scope changed"))
	}
	return NativeRuntimeHelperTask{Instance: record.Launch.Lease.Instance, OwnerGeneration: record.OwnerGeneration, HelperGeneration: record.Launch.Generation,
		PID: record.Launch.PID, StartTime: start, ObservedUID: uid, ExpectedCgroup: record.Group.Path, ObservedCgroup: group,
		RetirementClaimed: record.Launch.ExitConfirmed || record.Launch.ResourcesRemoved}, true, nil
}

func nativeHelperTaskScope(read func(string) ([]byte, error), dir string) (int, string, error) {
	status, err := read(filepath.Join(dir, "status"))
	if err != nil {
		return 0, "", err
	}
	// Reuse the independent tuple parser's duplicate/range checks for all UIDs.
	if _, _, err := nativeManagedStatusUID(string(status)); err != nil {
		return 0, "", err
	}
	uid, err := nativeStatusUID(string(status))
	if err != nil {
		return 0, "", err
	}
	raw, err := read(filepath.Join(dir, "cgroup"))
	if err != nil {
		return 0, "", err
	}
	group, err := nativeHostHelperParent(string(raw))
	return uid, group, err
}

func unresolvedNativeHelperTasks(tasks []NativeRuntimeHelperTask) []string {
	var unresolved []string
	for _, task := range tasks {
		reason := "original helper producer has not retired"
		if task.RetirementClaimed {
			reason = "acknowledged helper retains its original kernel task"
		} else if task.ObservedCgroup != task.ExpectedCgroup || task.ObservedUID != 0 {
			reason = "original helper escaped its retained root/cgroup scope"
		}
		unresolved = append(unresolved, fmt.Sprintf("native helper task %d: %s", task.PID, reason))
	}
	return unresolved
}

// Caller retains the original owner lock through the resource publication.
// A vanished cgroup alone cannot acknowledge an escaped original leader.
func (j *nativeHostHelperJournal) requireOriginalTasksAbsent(ctx context.Context, owner nativeLaunchRecord) error {
	frames, err := j.records(owner)
	if err != nil {
		return err
	}
	authorized := slices.ContainsFunc(frames, func(frame nativeHostHelperRecord) bool { return frame.Launch.Authorized })
	if !authorized {
		return ctx.Err()
	}
	if j.owner.helperProbe == nil {
		return errors.New("native helper: independent original task observation is unavailable")
	}
	tasks, err := j.owner.helperProbe.helperTasks(ctx, frames)
	if err != nil || len(tasks) != 0 {
		return errors.Join(err, errors.New("native helper: original task absence remains unproven"))
	}
	return ctx.Err()
}
