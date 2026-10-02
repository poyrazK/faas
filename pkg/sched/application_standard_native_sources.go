package sched

// adr: 431. Deliver the captured producer identities inside the hashed grant.

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func prepareStandardAdmittedRuntime(ctx context.Context, binding runtimeadmission.Binding, capture state.InstanceApplicationStandardAdmission, app AppSpec, snapshot *SnapshotRef, paused bool) (*vmmdpb.CreateAdmittedRuntimeRequest, error) {
	sources := make([]runtimeadmission.ArtifactSource, 0, len(capture.RuntimeArtifacts))
	for _, artifact := range capture.RuntimeArtifacts {
		source := runtimeadmission.ArtifactSource{Kind: artifact.Kind, WorkloadName: artifact.WorkloadName, StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes}
		sources = append(sources, source)
	}
	if binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion {
		if paused {
			return nil, runtimeadmission.ErrUnavailable
		}
		hash, err := runtimeadmission.HashArtifactSources(sources)
		if err != nil {
			return nil, err
		}
		binding.ArtifactSourcesHash = hash
	}
	req, err := PrepareAdmittedRuntime(ctx, binding, app, snapshot, paused)
	if err != nil || len(sources) == 0 {
		return req, err
	}
	if err := checkStandardSourceMembership(sources, app); err != nil {
		return nil, err
	}
	for _, source := range sources {
		req.ArtifactSources = append(req.ArtifactSources, source.ToProto())
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil {
		return nil, err
	}
	req.Binding.PayloadHash = hash
	return req, nil
}

func checkStandardSourceMembership(sources []runtimeadmission.ArtifactSource, app AppSpec) error {
	sidecars := map[string]string{}
	for _, workload := range app.Sidecars {
		if _, duplicate := sidecars[workload.Name]; duplicate {
			return runtimeadmission.ErrInvalid
		}
		sidecars[workload.Name] = workload.StorageKey
	}
	return runtimeadmission.CheckArtifactSources(sources, app.BaseKey, app.LayerKey, sidecars)
}
