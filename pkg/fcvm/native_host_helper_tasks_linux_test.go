//go:build linux

package fcvm

// adr: 780
import (
	"os"
	"testing"
)

// Actual kernel task observation only; no cgroup manipulation or KVM guest.
func TestNativeHelperTaskLinuxObservesActualOriginalPIDAcrossArbitraryArgv(t *testing.T) {
	_, owner, _ := nativeHelperJournalFixture(t)
	start, err := nativeHostHelperStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	frame := nativeHelperTaskRecord(owner, os.Getpid(), start)
	tasks, err := (nativeProcessProbe{root: "/proc"}).helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 1 || tasks[0].PID != os.Getpid() || tasks[0].ObservedUID != os.Geteuid() || tasks[0].StartTime != start {
		t.Fatal(tasks, err)
	}
	frame.Launch.StartTime++
	tasks, err = (nativeProcessProbe{root: "/proc"}).helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 0 {
		t.Fatal("unrelated incarnation adopted", tasks, err)
	}
}
