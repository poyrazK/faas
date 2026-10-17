package financial

import (
	"testing"

	"github.com/google/uuid"
)

// adr: 1641 — an app UUID must never alias a job partition's financial identity.
func TestFinancialComputeJobIdentitySeparatesResourceKinds(t *testing.T) {
	run, token := uuid.NewString(), uuid.NewString()
	job := ComputeRequest{AccountID: uuid.NewString(), InstanceID: uuid.NewString(), JobRunID: run, JobTaskIndex: 1, AttemptID: ComputeJobAttemptID(run, 1, token)}
	app := job
	app.JobRunID = ""
	app.JobTaskIndex = 0
	app.AppID = ComputeJobAttemptID(run, 1, "")
	j, err := BindComputeRequest(job)
	if err != nil {
		t.Fatal(err)
	}
	a, err := BindComputeRequest(app)
	if err != nil {
		t.Fatal(err)
	}
	if a.LeaseID == j.LeaseID || a.Fence == j.Fence || a.RequestID == j.RequestID {
		t.Fatal("resource kinds share monetary identity")
	}
	again, err := BindComputeRequest(job)
	if err != nil || again.LeaseID != j.LeaseID || again.Fence != j.Fence || again.RequestID != j.RequestID {
		t.Fatal("job replay minted another monetary identity")
	}
	job.JobTaskIndex++
	other, err := BindComputeRequest(job)
	if err != nil || other.LeaseID == j.LeaseID {
		t.Fatal("partitions share monetary identity")
	}
}
