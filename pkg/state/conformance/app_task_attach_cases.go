package conformance

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testAppTaskAttachSession pins ADR-958: the interactive record is created
// with its task, only running tasks under the current lease can record a
// node, and the routing view is scoped to the owning account and app.
func testAppTaskAttachSession(t *testing.T, fx *Fixture) {
	attachStore, ok := fx.Store.(state.AppTaskAttachStore)
	if !ok {
		t.Fatal("store does not implement AppTaskAttachStore")
	}
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if err := fx.Store.SetDeploymentRootfs(fx.Ctx, fx.Deployment.ID, "/local/app.ext4", "apps/conformance/attach.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	digest := sha256.Sum256([]byte("attach-token"))
	if _, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"/bin/sh"}, CreatedAt: base,
		Interactive: &state.AppTaskInteractive{TTY: true, TokenSHA256: []byte("short")},
	}); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("CreateAppTask(short digest) = %v, want ErrAppTaskInvalid", err)
	}
	if _, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		Kind: state.AppTaskKindRelease, Command: []string{"/bin/sh"}, CreatedAt: base,
		Interactive: &state.AppTaskInteractive{TokenSHA256: digest[:]},
	}); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("CreateAppTask(interactive release) = %v, want ErrAppTaskInvalid", err)
	}

	batch, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"bin/check"}, CreatedAt: base,
	})
	if err != nil {
		t.Fatalf("CreateAppTask(batch): %v", err)
	}
	if _, err := attachStore.AppTaskAttachByTask(fx.Ctx, batch.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AppTaskAttachByTask(batch) = %v, want ErrNotFound", err)
	}
	if _, err := attachStore.AppTaskAttachTarget(fx.Ctx, fx.Account.ID, fx.App.ID, batch.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AppTaskAttachTarget(batch) = %v, want ErrNotFound", err)
	}

	interactive, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"/bin/sh"}, TimeoutSeconds: 3600, CreatedAt: base.Add(time.Second),
		Interactive: &state.AppTaskInteractive{TTY: true, TokenSHA256: digest[:]},
	})
	if err != nil {
		t.Fatalf("CreateAppTask(interactive): %v", err)
	}
	record, err := attachStore.AppTaskAttachByTask(fx.Ctx, interactive.ID)
	if err != nil || record.TaskID != interactive.ID || !record.TTY || !bytes.Equal(record.TokenSHA256, digest[:]) ||
		record.NodeID != "" || record.NodeRecordedAt != nil {
		t.Fatalf("AppTaskAttachByTask = %+v, err=%v", record, err)
	}
	target, err := attachStore.AppTaskAttachTarget(fx.Ctx, fx.Account.ID, fx.App.ID, interactive.ID)
	if err != nil || target.TaskStatus != state.AppTaskQueued || !target.TTY || target.NodeID != "" {
		t.Fatalf("AppTaskAttachTarget(queued) = %+v, err=%v", target, err)
	}
	if _, err := attachStore.AppTaskAttachTarget(fx.Ctx, uuid.NewString(), fx.App.ID, interactive.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account AppTaskAttachTarget = %v, want ErrNotFound", err)
	}
	if _, err := attachStore.AppTaskAttachTarget(fx.Ctx, fx.Account.ID, fx.App.ID, "not-a-uuid"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("malformed-id AppTaskAttachTarget = %v, want ErrNotFound", err)
	}

	// Claims are oldest first: drain the batch task, then the interactive one.
	if _, err := fx.Store.ClaimNextAppTask(fx.Ctx, "schedd-a", base.Add(2*time.Second), time.Minute); err != nil {
		t.Fatalf("ClaimNextAppTask(batch): %v", err)
	}
	claimed, err := fx.Store.ClaimNextAppTask(fx.Ctx, "schedd-a", base.Add(2*time.Second), time.Minute)
	if err != nil || claimed.ID != interactive.ID {
		t.Fatalf("ClaimNextAppTask(interactive) = %+v, err=%v", claimed, err)
	}
	if err := attachStore.RecordAppTaskAttachNode(fx.Ctx, interactive.ID, *claimed.LeaseToken, "node-a", base.Add(3*time.Second)); !errors.Is(err, state.ErrAppTaskLeaseLost) {
		t.Fatalf("RecordAppTaskAttachNode(restoring) = %v, want ErrAppTaskLeaseLost", err)
	}
	if _, err := fx.Store.MarkAppTaskRunning(fx.Ctx, interactive.ID, *claimed.LeaseToken, base.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	if err := attachStore.RecordAppTaskAttachNode(fx.Ctx, interactive.ID, uuid.NewString(), "node-a", base.Add(4*time.Second)); !errors.Is(err, state.ErrAppTaskLeaseLost) {
		t.Fatalf("RecordAppTaskAttachNode(stale lease) = %v, want ErrAppTaskLeaseLost", err)
	}
	if err := attachStore.RecordAppTaskAttachNode(fx.Ctx, interactive.ID, *claimed.LeaseToken, "", base.Add(4*time.Second)); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("RecordAppTaskAttachNode(empty node) = %v, want ErrAppTaskInvalid", err)
	}
	if err := attachStore.RecordAppTaskAttachNode(fx.Ctx, interactive.ID, *claimed.LeaseToken, "node-a", base.Add(4*time.Second)); err != nil {
		t.Fatalf("RecordAppTaskAttachNode: %v", err)
	}
	target, err = attachStore.AppTaskAttachTarget(fx.Ctx, fx.Account.ID, fx.App.ID, interactive.ID)
	if err != nil || target.TaskStatus != state.AppTaskRunning || target.NodeID != "node-a" {
		t.Fatalf("AppTaskAttachTarget(running) = %+v, err=%v", target, err)
	}
	record, err = attachStore.AppTaskAttachByTask(fx.Ctx, interactive.ID)
	if err != nil || record.NodeID != "node-a" || record.NodeRecordedAt == nil || !record.NodeRecordedAt.Equal(base.Add(4*time.Second)) {
		t.Fatalf("AppTaskAttachByTask(after record) = %+v, err=%v", record, err)
	}
	exit := 0
	if _, err := fx.Store.CompleteAppTask(fx.Ctx, state.CompleteAppTaskParams{
		ID: interactive.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exit, FinishedAt: base.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("CompleteAppTask(interactive): %v", err)
	}
	target, err = attachStore.AppTaskAttachTarget(fx.Ctx, fx.Account.ID, fx.App.ID, interactive.ID)
	if err != nil || target.TaskStatus != state.AppTaskSucceeded {
		t.Fatalf("AppTaskAttachTarget(terminal) = %+v, err=%v", target, err)
	}
}
