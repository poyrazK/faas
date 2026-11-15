package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Called after local fsync and before the adapter proceeds to its next effect.
// A missing native acknowledgement stops the adapter; it never repeats a POST.
func publishMCPTaskReleaseReceipt(ctx context.Context, s *mcpNativeReleaseState) error {
	c, err := authedClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record, err := c.GetTaskToolReleaseIntent(ctx, s.TaskIntentID)
	if err != nil {
		return err
	}
	if record.Execution == nil || record.Execution.OperationID != s.TaskOperationID {
		return errors.New("Task operation identity changed")
	}
	p, dir, err := loadMCPTaskExecutionProfile(record)
	if err != nil {
		return err
	}
	var token string
	if err = readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &token); err != nil {
		return err
	}
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil {
		return err
	}
	checkpoint := &api.TaskToolReleaseReceipt{Revision: s.Revision, ReleaseID: s.ReleaseID, Stage: s.Stage,
		ServingCaptured: s.ServingCaptured, ServingDeploymentID: s.ServingDeployment, WebDeploymentID: s.WebDeployment, WorkerDeploymentID: s.WorkerDeployment,
		PendingSubmission: s.PendingSubmission, PreviousDeployments: s.PreviousDeployments, WorkerIDs: append([]string{}, s.WorkerIDs...), Promoted: s.Promoted, Parked: s.Parked}
	slices.Sort(checkpoint.WorkerIDs)
	out, err := c.CheckTaskToolReleaseIntent(ctx, record.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: review.Fingerprint,
		Execution: &api.TaskToolReleaseExecutionRequest{Action: "guard", OperationID: p.OperationID, BindingsSHA256: p.digest(), ClaimToken: token, Stage: s.Stage, Receipt: checkpoint}})
	if err != nil {
		return err
	}
	if out.Status != "approved" || out.Execution == nil || out.Execution.State != "running" || out.Execution.Receipt == nil || !reflect.DeepEqual(*out.Execution.Receipt, *checkpoint) {
		return errors.New("native Task checkpoint was not acknowledged")
	}
	return nil
}
