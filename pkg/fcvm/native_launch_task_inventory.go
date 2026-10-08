package fcvm

// adr: 776
import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Launch tasks include root jailers and vmmd's pre-exec launch helper. TargetUID
// is the intended jail UID; ObservedUID is the independently read kernel UID.
// These observations cannot authorize a producer, retirement or capacity release.
type NativeRuntimeLaunchTask struct {
	PID, TargetUID, ObservedUID int
	Instance                    string
	StartTime                   uint64
	Jailer                      bool
	Plan                        api.Plan
	IsBuilder                   bool
}

// Observe twice so a root helper's exec, exit, PID reuse or changing launch
// scope cannot be accepted as one stable sample. Each fresh inventory repeats
// this scan around its synchronous consumer, independently of journal owners.
func (p nativeProcessProbe) launchTasks(ctx context.Context) ([]NativeRuntimeLaunchTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.Stat(p.root)
	if err != nil {
		return nil, err
	}
	first, err := p.processes(ctx)
	if err != nil {
		return nil, err
	}
	second, err := p.processes(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(first, func(a, b nativeProcessIdentity) int { return cmp.Compare(a.PID, b.PID) })
	slices.SortFunc(second, func(a, b nativeProcessIdentity) int { return cmp.Compare(a.PID, b.PID) })
	if !slices.Equal(first, second) {
		return nil, errors.New("native inventory: launch task set changed during scan")
	}
	currentRoot, err := os.Stat(p.root)
	if err != nil || !os.SameFile(root, currentRoot) {
		return nil, errors.Join(err, errors.New("native inventory: process root changed between scans"))
	}
	tasks := make([]NativeRuntimeLaunchTask, len(first))
	for i, process := range first {
		tasks[i] = NativeRuntimeLaunchTask{PID: process.PID, TargetUID: process.UID, ObservedUID: process.ObservedUID,
			Instance: process.Instance, StartTime: process.StartTime, Jailer: process.Jailer, Plan: process.Plan, IsBuilder: process.IsBuilder}
	}
	return tasks, ctx.Err()
}

func unresolvedNativeLaunchTasks(tasks []NativeRuntimeLaunchTask, owners []NativeRuntimeObservation) []string {
	var unresolved []string
	for _, task := range tasks {
		matched := false
		for _, owner := range owners {
			if !task.Jailer && task.PID == owner.PID && task.StartTime == owner.StartTime && task.Instance == owner.Instance &&
				task.TargetUID == owner.Lease.UID && task.ObservedUID == owner.Lease.UID && task.Plan == owner.Lease.Plan && task.IsBuilder == owner.Lease.IsBuilder &&
				owner.Authorized && !owner.ExitConfirmed && !owner.ResourcesRemoved {
				matched = true
				break
			}
		}
		if !matched {
			unresolved = append(unresolved, fmt.Sprintf("native launch task %d has no original native VM owner", task.PID))
		}
	}
	return unresolved
}
