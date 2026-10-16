package sched

// adr: 435. Deliver the captured producer identities inside the hashed grant.

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
		hash, err := runtimeadmission.HashArtifactSources(sources)
		if err != nil {
			return nil, err
		}
		binding.ArtifactSourcesHash = hash
	}
	if len(sources) > 0 {
		if err := bindStandardArtifactSources(sources, &app); err != nil {
			return nil, err
		}
	}
	req, err := PrepareAdmittedRuntime(ctx, binding, app, snapshot, paused)
	if err != nil || len(sources) == 0 {
		return req, err
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

func bindStandardArtifactSources(sources []runtimeadmission.ArtifactSource, app *AppSpec) error {
	if app == nil {
		return runtimeadmission.ErrInvalid
	}
	keys := make(map[string]string, len(sources))
	for _, source := range sources {
		if !source.Valid() {
			return runtimeadmission.ErrInvalid
		}
		role := source.Role()
		if _, exists := keys[role]; exists {
			return runtimeadmission.ErrInvalid
		}
		keys[role] = source.StorageKey
	}
	baseKey, hasBase := keys["base"]
	mainKey, hasMain := keys["main"]
	if !hasBase || !hasMain {
		return runtimeadmission.ErrInvalid
	}
	app.BaseKey, app.LayerKey = baseKey, mainKey
	app.Sidecars = append(app.Sidecars[:0:0], app.Sidecars...)
	for i := range app.Sidecars {
		role := "sidecar:" + app.Sidecars[i].Name
		key, ok := keys[role]
		if !ok {
			return runtimeadmission.ErrInvalid
		}
		app.Sidecars[i].StorageKey = key
		delete(keys, role)
	}
	delete(keys, "base")
	delete(keys, "main")
	if len(keys) != 0 {
		return runtimeadmission.ErrInvalid
	}
	return checkStandardSourceMembership(sources, *app)
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
