package fcvm

// adr: 787
import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneNativeRegionalSource(source *runtimequota.LaunchSource) *runtimequota.LaunchSource {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func sameNativeRegionalSource(a, b *runtimequota.LaunchSource) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func validateNativeRegionalSource(lease Lease, placement *runtimequota.Placement, resources *state.RegionalRuntimeResources, source *runtimequota.LaunchSource) error {
	if source == nil {
		return nil // Historical frames retain their original unknown source.
	}
	if source.Validate() != nil || placement == nil || resources == nil || !resources.Valid() || source.Permit != placement.Permit || source.InstanceKey != lease.Instance ||
		source.Permit.Kind == runtimequota.CapacityAppTask && source.ValidateTask() != nil {
		return runtimequotahost.ErrBinding
	}
	return nil
}

func decodeNativeRegionalSource(raw []byte) (runtimequota.LaunchSource, error) {
	var source runtimequota.LaunchSource
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&source) != nil || source.Validate() != nil || source.Permit.Kind == runtimequota.CapacityAppTask && source.ValidateTask() != nil {
		return source, runtimequotahost.ErrBinding
	}
	canonical, err := json.Marshal(source)
	if err != nil || !bytes.Equal(raw, canonical) {
		return source, runtimequotahost.ErrBinding
	}
	return source, nil
}

func nativeRegionalWakeSource(lease Lease, req WakeRequest) (runtimequota.LaunchSource, error) {
	if req.RegionalRuntime == nil {
		return runtimequota.LaunchSource{}, runtimequotahost.ErrBinding
	}
	source := runtimequota.LaunchSource{Permit: req.RegionalRuntime.Permit, InstanceKey: lease.Instance, TaskID: req.AppTaskID}
	if source.Validate() != nil || source.InstanceKey != req.Instance || source.Permit.Kind == runtimequota.CapacityAppTask &&
		(source.ValidateTask() != nil || !req.AppTaskOnly || source.Permit.RunID != req.AppTaskLeaseToken) ||
		source.Permit.Kind != runtimequota.CapacityAppTask && (req.AppTaskID != "" || req.AppTaskLeaseToken != "" || req.AppTaskOnly) {
		return runtimequota.LaunchSource{}, runtimequotahost.ErrBinding
	}
	return source, nil
}

func (v *JailerVMM) prepareNativeRegionalSource(ctx context.Context, lease Lease, p runtimequota.Placement, resources state.RegionalRuntimeResources, source runtimequota.LaunchSource) error {
	if v.nativeRecovery == nil {
		return runtimequotahost.ErrStorage
	}
	return v.prepareNativeLeaseWithRegionalSource(ctx, lease, &p, &resources, &source)
}
