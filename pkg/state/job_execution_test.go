// adr: 594
package state

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type jobExecutionAuditStore interface {
	Store
	JobExecutionInstances(context.Context, string) ([]JobExecutionInstance, error)
}

func TestJobExecutionInstancesRetainsTerminalPlacement(t *testing.T) {
	exerciseJobExecutionAudit(t, NewMemStore())
}

func exerciseJobExecutionAudit(t *testing.T, store jobExecutionAuditStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "job-audit@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "audit-job", "batch", "example/job:v1", []string{"/bin/true"}, 128, 120, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.JobCreate(ctx, account.ID, "other-job", "batch", "example/job:v1", []string{"/bin/true"}, 128, 120, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminalID, liveID := uuid.NewString(), uuid.NewString()
	if _, err := store.CreateJobInstance(ctx, terminalID, job.ID, "", 0, "cold_booting", 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(ctx, terminalID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateJobInstance(ctx, liveID, job.ID, "", 0, "cold_booting", 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateJobInstance(ctx, uuid.NewString(), other.ID, "", 0, "cold_booting", 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	instances, err := store.JobExecutionInstances(ctx, job.ID)
	if err != nil || len(instances) != 2 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	if instances[0].ID >= instances[1].ID {
		t.Fatal("placement audit order is not stable")
	}
	seen := make(map[string]string)
	for _, instance := range instances {
		if instance.JobID != job.ID || instance.NodeID != node.ID || instance.Kind != "job_task" || instance.RAMMB != 128 {
			t.Fatalf("placement audit omitted durable ownership: %+v", instance)
		}
		seen[instance.ID] = instance.State
	}
	if seen[terminalID] != "stopped" || seen[liveID] != "cold_booting" {
		t.Fatalf("lost historical placement: %+v", seen)
	}
	empty, err := store.JobExecutionInstances(ctx, uuid.NewString())
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown job=%+v err=%v", empty, err)
	}
}
