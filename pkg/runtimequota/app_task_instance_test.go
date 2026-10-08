package runtimequota

// adr: 781
import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAppTaskInstanceBindsOriginalClaim(t *testing.T) {
	task, run := uuid.NewString(), uuid.NewString()
	instance, err := AppTaskInstanceID(task, run)
	if err != nil || !validID(instance) || instance == task {
		t.Fatal("invalid attempt identity", instance, err)
	}
	again, _ := AppTaskInstanceID(task, run)
	replacement, _ := AppTaskInstanceID(task, uuid.NewString())
	other, _ := AppTaskInstanceID(uuid.NewString(), run)
	if again != instance || replacement == instance || other == instance {
		t.Fatal("attempts share physical identity")
	}
	for _, bad := range []string{"", uuid.Nil.String(), strings.ToUpper(task), strings.ReplaceAll(task, "-", "")} {
		if _, err := AppTaskInstanceID(bad, run); err == nil {
			t.Fatal("ambiguous task identity accepted", bad)
		}
		if _, err := AppTaskInstanceID(task, bad); err == nil {
			t.Fatal("ambiguous lease identity accepted", bad)
		}
	}
	g, _, _, _ := fixture(t)
	p := capacityPermitFixture(g)
	p.Kind, p.RunID, p.InstanceID = CapacityAppTask, run, instance
	source := LaunchSource{Permit: p, InstanceKey: instance, TaskID: task}
	if source.Validate() != nil || source.ValidateTask() != nil {
		t.Fatal("original claim refused")
	}
	for _, change := range []string{"task", "lease", "instance", "purpose", "missing"} {
		t.Run(change, func(t *testing.T) {
			bad := source
			switch change {
			case "task":
				bad.TaskID = uuid.NewString()
			case "lease":
				bad.Permit.RunID = uuid.NewString()
			case "instance":
				bad.Permit.InstanceID = task
				bad.InstanceKey = task
			case "purpose":
				bad.Permit.Kind = CapacityPrime
			case "missing":
				bad.TaskID = ""
			}
			if bad.ValidateTask() == nil || change != "missing" && bad.Validate() == nil {
				t.Fatal("claim changed without a new physical attempt")
			}
			if change == "missing" && bad.Validate() != nil {
				t.Fatal("historical source became unreadable")
			}
		})
	}
}
