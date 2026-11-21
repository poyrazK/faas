package sched

import (
	"context"
	"errors"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func (c *VMMClient) CaptureExecutionTemplate(ctx context.Context, spec ExecutionTemplateSpec) (ExecutionTemplateCapture, error) {
	resp, err := c.cli.CaptureExecutionTemplate(ctx, &vmmdpb.CaptureExecutionTemplateRequest{AccountId: spec.AccountID, Runtime: string(spec.Identity.Runtime), BaseKey: spec.Artifacts.BaseKey, LayerKey: spec.Artifacts.LayerKey, MemoryMb: int32(spec.Identity.MemoryMB), EphemeralDiskMb: int32(spec.Identity.EphemeralDiskMB), KernelDigest: spec.Identity.KernelDigest, BaseDigest: spec.Identity.BaseImageDigest})
	if err != nil {
		return ExecutionTemplateCapture{}, liftErr(err)
	}
	s := resp.GetSnapshot()
	if !executionTemplateStorageKey(s.GetStorageKey()) || s.GetVmstateStorageKey() != runtimeSnapshotVMStateStorageKey(s.GetStorageKey()) {
		return ExecutionTemplateCapture{}, ErrRuntimeSnapshotInvalid
	}
	return ExecutionTemplateCapture{NodeID: spec.NodeID, Snapshot: SnapshotRef{StorageKey: s.GetStorageKey(), VMStateStorageKey: s.GetVmstateStorageKey(), FCVersion: s.GetFcVersion(), MemBytes: s.GetMemBytes(), Networkless: s.GetNetworkless()}, VMStateBytes: resp.GetVmstateBytes()}, nil
}

func (c *VMMClient) RetireExecutionTemplate(ctx context.Context, capture ExecutionTemplateCapture) error {
	if !executionTemplateStorageKey(capture.Snapshot.StorageKey) || capture.Snapshot.VMStateStorageKey != runtimeSnapshotVMStateStorageKey(capture.Snapshot.StorageKey) {
		return ErrRuntimeSnapshotInvalid
	}
	var result error
	for _, name := range []string{"mem", "vmstate", "drive", "backing"} {
		key := strings.TrimSuffix(capture.Snapshot.StorageKey, "mem") + name
		_, err := c.cli.RetireNativeRuntimeSnapshotObject(ctx, &vmmdpb.RetireNativeRuntimeSnapshotObjectRequest{StorageKey: key})
		result = errors.Join(result, liftErr(err))
	}
	return result
}

func (r *VMMRouter) CaptureExecutionTemplate(ctx context.Context, spec ExecutionTemplateSpec) (ExecutionTemplateCapture, error) {
	client, err := r.resolveFor(ctx, spec.NodeID)
	if err != nil {
		return ExecutionTemplateCapture{}, err
	}
	c, ok := client.(ExecutionTemplateCapturer)
	if !ok {
		return ExecutionTemplateCapture{}, ErrRuntimeSnapshotUnwired
	}
	return c.CaptureExecutionTemplate(ctx, spec)
}

func (r *VMMRouter) RetireExecutionTemplate(ctx context.Context, capture ExecutionTemplateCapture) error {
	client, err := r.resolveFor(ctx, capture.NodeID)
	if err != nil {
		return err
	}
	c, ok := client.(ExecutionTemplateCapturer)
	if !ok {
		return errors.New("sched: execution template retirement unavailable")
	}
	return c.RetireExecutionTemplate(ctx, capture)
}
