package sched

import (
	"context"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardNativePromotionVMM interface {
	RuntimeAdmissionIdentity(context.Context, string) (runtimeadmission.Identity, error)
	PromoteAdmittedRuntime(context.Context, string, *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error)
}

// app is the caller's current read of warm's application; the promotion
// guard rechecks ownership and inputs when the receipt publishes.
func (e *Engine) resumeWarmWithStandards(ctx context.Context, app state.App, warm state.Instance, imageHealthcheck bool) (*runtimeadmission.Receipt, error) {
	capture, err := e.capturedStandardRuntimeForApp(ctx, app, warm.ID)
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	if !capture.Managed {
		if imageHealthcheck {
			checked, ok := e.vmm.(interface {
				ResumeWarmInstanceWithImageHealthcheck(context.Context, string, string) error
			})
			if !ok {
				return nil, runtimeadmission.ErrUnavailable
			}
			return nil, checked.ResumeWarmInstanceWithImageHealthcheck(ctx, e.nodeForRoute(warm.NodeID), warm.ID)
		}
		resumer, ok := e.vmm.(WarmResumeVMM)
		if !ok {
			return nil, runtimeadmission.ErrUnavailable
		}
		return nil, resumer.ResumeWarmInstance(ctx, e.nodeForRoute(warm.NodeID), warm.ID)
	}
	native, ok := e.vmm.(standardNativePromotionVMM)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	store, ok := e.store.(state.InstanceApplicationStandardPromotionStore)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	parent, err := store.GetInstanceApplicationStandardWarmParent(ctx, warm.ID)
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	identity, err := native.RuntimeAdmissionIdentity(ctx, warm.NodeID)
	if err != nil {
		return nil, err
	}
	if identity.Validate() != nil || identity.NodeID != warm.NodeID || capture.NativeInputHash != parent.Binding.CapturedInputHash {
		return nil, runtimeadmission.ErrStale
	}
	// A parent issued by an earlier vmmd is one the warm-parent lock accepted
	// through the instance's re-binds (ADR-595 extension): promote it under
	// the current process, holding the issuer.
	held := ""
	if identity.Incarnation != parent.Binding.Incarnation {
		held = parent.Binding.Incarnation
	}
	if parent.Binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion && (identity.ProtocolVersion < runtimeadmission.ArtifactProtocolVersion || identity.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion) {
		return nil, runtimeadmission.ErrUnavailable
	}
	p := runtimeadmission.Promotion{Binding: parent.Binding, Parent: parent}.Held(held)
	now := time.Now().UTC()
	p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
	p.Binding.Incarnation = identity.Incarnation
	p.Binding.PayloadHash, err = runtimeadmission.HashPromotionPayload(p.ToProto())
	if err != nil {
		return nil, err
	}
	p, err = store.IssueInstanceApplicationStandardPromotion(ctx, p)
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	p = p.Held(held)
	r, err := native.PromoteAdmittedRuntime(ctx, warm.NodeID, p.ToProto())
	if err != nil {
		return nil, err
	}
	if p.CheckReceipt(r, time.Now()) != nil {
		return nil, runtimeadmission.ErrInvalid
	}
	return &r, nil
}
