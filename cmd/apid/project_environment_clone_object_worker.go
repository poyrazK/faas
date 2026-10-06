package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type capturedProjectEnvironmentObjectPlan struct {
	appID, sourceScope string
	source             state.ProjectEnvironmentCloneObjectBucket
}

type cloneObjectWorkerStore interface {
	cloneObjectSnapshotWorkerStore
	state.ProjectEnvironmentCloneObjectBucketStore
	state.ProjectEnvironmentCloneOperationStore
	state.ObjectBucketStore
}

func renewCloneObjectWorkerLease(ctx context.Context, store state.ProjectEnvironmentCloneWorkerLeaseStore, lease state.ProjectEnvironmentCloneLease, phase string) (state.ProjectEnvironmentCloneLease, error) {
	renewed, err := store.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return lease, err
	}
	if renewed.Operation.Status != phase {
		return lease, state.ErrConflict
	}
	return renewed, nil
}

func (s *server) capturedProjectEnvironmentObjectPlans(ctx context.Context, op state.ProjectEnvironmentCloneOperation) ([]capturedProjectEnvironmentObjectPlan, error) {
	workloads, ok := s.store.(state.ProjectEnvironmentCloneWorkloadStore)
	bindings, bindingsOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !bindingsOK {
		return nil, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	views, err := workloads.ProjectEnvironmentCloneWorkloads(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, err
	}
	catalogue, err := bindings.ProjectEnvironmentCloneBindings(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, err
	}
	return buildCapturedProjectEnvironmentObjectPlans(op, views, catalogue)
}

func buildCapturedProjectEnvironmentObjectPlans(op state.ProjectEnvironmentCloneOperation, views []state.ProjectEnvironmentCloneWorkload, catalogue []state.ProjectEnvironmentCloneBindings) ([]capturedProjectEnvironmentObjectPlan, error) {
	if op.ID == "" || op.AccountID == "" || op.ProjectID == "" || len(views) == 0 || len(views) != len(catalogue) {
		return nil, state.ErrProjectEnvironmentCloneBindingCapture
	}
	byApp := make(map[string]state.ProjectEnvironmentCloneWorkload, len(views))
	for _, view := range views {
		if view.OperationID != op.ID || view.AppID == "" || view.SourceBindingsHash == "" || byApp[view.AppID].AppID != "" {
			return nil, state.ErrProjectEnvironmentCloneBindingCapture
		}
		byApp[view.AppID] = view
	}
	seen := map[string]bool{}
	plans := []capturedProjectEnvironmentObjectPlan{}
	for _, definitions := range catalogue {
		view, ok := byApp[definitions.AppID]
		if !ok || view.SourceScope != definitions.SourceScope || view.SourceBindingsHash != definitions.Hash {
			return nil, state.ErrProjectEnvironmentCloneBindingCapture
		}
		delete(byApp, definitions.AppID)
		for _, bucket := range definitions.Buckets {
			if bucket.ID == "" || seen[bucket.ID] || bucket.Name == "" || bucket.Region == "" || bucket.BackendID == "" || bucket.BackendFingerprint == "" || bucket.PhysicalName == "" {
				return nil, state.ErrProjectEnvironmentCloneBindingCapture
			}
			seen[bucket.ID] = true
			plans = append(plans, capturedProjectEnvironmentObjectPlan{appID: view.AppID, sourceScope: view.SourceScope, source: bucket})
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].source.ID < plans[j].source.ID })
	return plans, nil
}

// The capture coordinator persists this common point before listing versions.
// SourceVersion is filled with the committed version-manifest hash during the
// capturing phase, then becomes immutable when the operation enters copying.
func capturedProjectEnvironmentObjectResources(plans []capturedProjectEnvironmentObjectPlan, point time.Time) ([]state.ProjectEnvironmentCloneResource, error) {
	if point.IsZero() || point.Nanosecond()%1000 != 0 {
		return nil, state.ErrConflict
	}
	resources := make([]state.ProjectEnvironmentCloneResource, 0, len(plans))
	for _, plan := range plans {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "object_storage", Name: plan.source.ID, SourceID: plan.source.ID,
			CapturePoint: point.UTC().Format(time.RFC3339Nano), Status: "planned"})
	}
	return resources, nil
}

func validateCapturedProjectEnvironmentObjectResources(plans []capturedProjectEnvironmentObjectPlan, resources []state.ProjectEnvironmentCloneResource, capturing bool) (map[string]int, time.Time, error) {
	indices := map[string]int{}
	var point time.Time
	for i, resource := range resources {
		if resource.Kind != "object_storage" && resource.Kind != "managed_postgres" && resource.Kind != "postgres" {
			continue
		}
		captured, err := time.Parse(time.RFC3339Nano, resource.CapturePoint)
		if err != nil || captured.IsZero() || captured.Nanosecond()%1000 != 0 || resource.CapturePoint != captured.UTC().Format(time.RFC3339Nano) ||
			!captured.Before(time.Now().UTC()) || !point.IsZero() && !point.Equal(captured) {
			return nil, point, state.ErrConflict
		}
		point = captured
		if resource.Kind != "object_storage" {
			continue
		}
		if _, duplicate := indices[resource.SourceID]; duplicate || resource.Name != resource.SourceID || resource.SourceID == resource.TargetID {
			return nil, point, state.ErrConflict
		}
		indices[resource.SourceID] = i
		if resource.SourceVersion != "" {
			hash, err := hex.DecodeString(resource.SourceVersion)
			if err != nil || len(hash) != 32 || strings.ToLower(resource.SourceVersion) != resource.SourceVersion {
				return nil, point, state.ErrConflict
			}
		}
		if capturing {
			if resource.Status != "planned" && resource.Status != "capturing" && resource.Status != "captured" ||
				resource.Status == "captured" && (resource.SourceVersion == "" || resource.TargetID == "") {
				return nil, point, state.ErrConflict
			}
		} else if resource.SourceVersion == "" || resource.TargetID == "" || resource.Status != "captured" && resource.Status != "copying" && resource.Status != "verifying" && resource.Status != "ready" {
			return nil, point, state.ErrConflict
		}
	}
	if len(indices) != len(plans) {
		return nil, point, state.ErrConflict
	}
	for _, plan := range plans {
		if _, exists := indices[plan.source.ID]; !exists {
			return nil, point, state.ErrConflict
		}
	}
	return indices, point, nil
}

func (s *server) capturedCloneObjectProvider(plan capturedProjectEnvironmentObjectPlan) (objectstorage.Provider, error) {
	if !s.objectStorageEnabled() {
		return nil, objectstorage.ErrUnavailable
	}
	backend, err := s.objectStorage.Resolve(plan.source.BackendID, plan.source.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	if _, ok := backend.Provider.(objectstorage.VersionedObjectLister); !ok {
		return nil, objectstorage.ErrUnsupported
	}
	if _, ok := backend.Provider.(objectstorage.ObjectSnapshotCopier); !ok {
		return nil, objectstorage.ErrUnsupported
	}
	if _, ok := backend.Provider.(objectstorage.ObjectVersionRetentionObserver); !ok {
		return nil, objectstorage.ErrObjectSnapshotRetentionUnavailable
	}
	return backend.Provider, nil
}

func checkpointCloneObjectResource(ctx context.Context, store state.ProjectEnvironmentCloneOperationStore, lease state.ProjectEnvironmentCloneLease, i int, resource state.ProjectEnvironmentCloneResource) (state.ProjectEnvironmentCloneLease, error) {
	if resource == lease.Operation.Resources[i] {
		return lease, nil
	}
	resources := append([]state.ProjectEnvironmentCloneResource(nil), lease.Operation.Resources...)
	resources[i] = resource
	op := lease.Operation
	updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, op.Status, op.Revision, resources, "")
	if err != nil {
		return lease, err
	}
	lease.Operation = updated
	return lease, nil
}

func validateCapturedCloneObjectManifest(op state.ProjectEnvironmentCloneOperation, resource state.ProjectEnvironmentCloneResource, manifest state.ProjectEnvironmentCloneObjectManifest) error {
	if manifest.OperationID != op.ID || manifest.SourceBucketID != resource.SourceID || manifest.TargetBucketID != resource.TargetID ||
		manifest.CapturedAt.UTC().Format(time.RFC3339Nano) != resource.CapturePoint || resource.SourceVersion != "" && manifest.Hash != resource.SourceVersion {
		return state.ErrConflict
	}
	versions := make([]state.ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))
	for i, checkpoint := range manifest.Objects {
		versions[i] = checkpoint.Source
	}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash(versions)
	if err != nil || hash != manifest.Hash {
		return state.ErrConflict
	}
	return nil
}

// Reservation and source version manifests are durable before copying starts.
// A crash before the resource checkpoint recovers the same operation-owned
// bucket and manifest. No source bucket configuration is read on retry.
func (s *server) captureProjectEnvironmentCloneObjects(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(cloneObjectWorkerStore)
	if !ok {
		return lease, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, err
	}
	plans, err := s.capturedProjectEnvironmentObjectPlans(ctx, lease.Operation)
	if err != nil {
		return lease, err
	}
	indices, point, err := validateCapturedProjectEnvironmentObjectResources(plans, lease.Operation.Resources, true)
	if err != nil {
		return lease, err
	}
	providers := make(map[string]objectstorage.Provider, len(plans))
	for _, plan := range plans {
		provider, err := s.capturedCloneObjectProvider(plan)
		if err != nil {
			return lease, err
		}
		providers[plan.source.ID] = provider
	}
	for _, plan := range plans {
		lease, err = renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
		if err != nil {
			return lease, err
		}
		bucket, _, err := store.ReserveProjectEnvironmentCloneObjectBucket(ctx, lease, plan.appID, plan.source.ID, s.objectStorage.MaxBucketsPerApp)
		if err != nil {
			return lease, err
		}
		i := indices[plan.source.ID]
		resource := lease.Operation.Resources[i]
		if resource.TargetID != "" && resource.TargetID != bucket.ID {
			return lease, state.ErrConflict
		}
		resource.TargetID = bucket.ID
		if resource.Status == "planned" {
			resource.Status = "capturing"
		}
		lease, err = checkpointCloneObjectResource(ctx, store, lease, i, resource)
		if err != nil {
			return lease, err
		}
		callCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		if bucket.State == "provisioning" {
			bucket, err = s.provisionBucket(callCtx, store, bucket)
		}
		if err != nil {
			cancel()
			return lease, err
		}
		manifest, err := captureProjectEnvironmentObjectStorageSnapshotForLease(callCtx, store, providers[plan.source.ID], lease,
			plan.source.ID, bucket.ID, plan.source.PhysicalName, point)
		cancel()
		if err != nil {
			return lease, err
		}
		if err := validateCapturedCloneObjectManifest(lease.Operation, resource, manifest); err != nil {
			return lease, err
		}
		resource.SourceVersion, resource.Status = manifest.Hash, "captured"
		lease, err = checkpointCloneObjectResource(ctx, store, lease, i, resource)
		if err != nil {
			return lease, err
		}
	}
	return lease, nil
}

func (s *server) prepareProjectEnvironmentCloneObjects(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	store, ok := s.store.(cloneObjectWorkerStore)
	if !ok {
		return lease, false, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCopying)
	if err != nil {
		return lease, false, err
	}
	plans, err := s.capturedProjectEnvironmentObjectPlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	indices, _, err := validateCapturedProjectEnvironmentObjectResources(plans, lease.Operation.Resources, false)
	if err != nil {
		return lease, false, err
	}
	for _, plan := range plans {
		lease, err = renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCopying)
		if err != nil {
			return lease, false, err
		}
		i := indices[plan.source.ID]
		resource := lease.Operation.Resources[i]
		bucket, err := store.ProjectEnvironmentCloneObjectBucketForLease(ctx, lease, plan.appID, plan.source.ID, resource.TargetID)
		if err != nil {
			return lease, false, err
		}
		if bucket.State != "ready" {
			return lease, false, state.ErrConflict
		}
		manifest, err := store.ProjectEnvironmentCloneObjectManifest(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID, plan.source.ID)
		if err != nil {
			return lease, false, err
		}
		if err := validateCapturedCloneObjectManifest(lease.Operation, resource, manifest); err != nil {
			return lease, false, err
		}
		provider, err := s.capturedCloneObjectProvider(plan)
		if err != nil {
			return lease, false, err
		}
		copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease,
			plan.source.ID, bucket.ID, plan.source.PhysicalName, bucket.PhysicalName)
		if err != nil {
			return lease, false, fmt.Errorf("copy captured object bucket: %w", err)
		}
		if copied != len(manifest.Objects) {
			return lease, false, state.ErrConflict
		}
		resource.Status = "ready"
		lease, err = checkpointCloneObjectResource(ctx, store, lease, i, resource)
		if err != nil {
			return lease, false, err
		}
	}
	return lease, true, nil
}
