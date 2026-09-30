package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectEnvironmentCloneWorkerLeaseDuration = 5 * time.Minute

type capturedProjectEnvironmentDatabasePlan struct {
	source     managedpostgres.Database
	name, hash string
}

// Plans come exclusively from the authenticated private capture. Database
// definitions are deduplicated across workloads; a changed source desired spec
// or binding cannot alter a retry. No provider IO occurs during planning.
func (s *server) capturedProjectEnvironmentDatabasePlans(ctx context.Context, op state.ProjectEnvironmentCloneOperation) ([]capturedProjectEnvironmentDatabasePlan, error) {
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
	return buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
}

func buildCapturedProjectEnvironmentDatabasePlans(op state.ProjectEnvironmentCloneOperation, views []state.ProjectEnvironmentCloneWorkload, catalogue []state.ProjectEnvironmentCloneBindings) ([]capturedProjectEnvironmentDatabasePlan, error) {
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
	byDatabase := map[string]capturedProjectEnvironmentDatabasePlan{}
	for _, definitions := range catalogue {
		view, ok := byApp[definitions.AppID]
		if !ok || view.SourceScope != definitions.SourceScope || view.SourceBindingsHash != definitions.Hash {
			return nil, state.ErrProjectEnvironmentCloneBindingCapture
		}
		delete(byApp, definitions.AppID)
		for _, binding := range definitions.Postgres {
			source := capturedProjectEnvironmentDatabase(op.AccountID, binding)
			if source.ID == "" || source.Name == "" || source.ProviderResourceID == "" || source.BackendID == "" || source.BackendFingerprint == "" || source.Spec.Validate() != nil || source.Spec.RestoreWindowSeconds <= 0 {
				return nil, state.ErrProjectEnvironmentCloneBindingCapture
			}
			hash, err := capturedProjectEnvironmentDatabaseHash(source)
			if err != nil {
				return nil, err
			}
			if previous, exists := byDatabase[source.ID]; exists && previous.hash != hash {
				return nil, fmt.Errorf("captured database definitions differ across workloads: %w", state.ErrProjectEnvironmentCloneBindingCapture)
			}
			sum := sha256.Sum256([]byte(strings.Join([]string{op.ProjectID, op.ID, op.TargetEnvironment, source.ID}, "\x00")))
			byDatabase[source.ID] = capturedProjectEnvironmentDatabasePlan{source: source, hash: hash, name: "env-" + op.TargetEnvironment + "-" + hex.EncodeToString(sum[:6])}
		}
	}
	plans := make([]capturedProjectEnvironmentDatabasePlan, 0, len(byDatabase))
	for _, plan := range byDatabase {
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].source.ID < plans[j].source.ID })
	return plans, nil
}

func capturedProjectEnvironmentDatabase(accountID string, binding state.ProjectEnvironmentClonePostgresBinding) managedpostgres.Database {
	return managedpostgres.Database{
		ID: binding.DatabaseID, AccountID: accountID, Name: binding.DatabaseName, State: managedpostgres.StateReady,
		BackendID: binding.BackendID, BackendFingerprint: binding.BackendFingerprint, ProviderResourceID: binding.ProviderResourceID,
		Spec: managedpostgres.Spec{Region: binding.Region, PostgresMajor: binding.PostgresMajor, Class: managedpostgres.ServiceClass(binding.ServiceClass),
			Availability: managedpostgres.Availability(binding.Availability), ScaleToZero: binding.ScaleToZero,
			StorageLimitBytes: binding.StorageLimitBytes, RestoreWindowSeconds: binding.RestoreWindowSeconds},
	}
}

func capturedProjectEnvironmentDatabaseHash(source managedpostgres.Database) (string, error) {
	raw, err := json.Marshal(struct {
		ID, Name   string
		Definition managedpostgres.RestoreSourceDefinition
	}{source.ID, source.Name, managedpostgres.RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID,
		BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID}})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// The coordinated capture owner supplies this point before copying. Never
// select time.Now during a retry or adopt a point from a mutable live database.
func capturedProjectEnvironmentDatabaseResources(plans []capturedProjectEnvironmentDatabasePlan, point time.Time) ([]state.ProjectEnvironmentCloneResource, error) {
	if point.IsZero() || point.Nanosecond()%1000 != 0 {
		return nil, state.ErrConflict
	}
	resources := make([]state.ProjectEnvironmentCloneResource, 0, len(plans))
	for _, plan := range plans {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "managed_postgres", Name: plan.source.ID, SourceID: plan.source.ID,
			SourceVersion: plan.hash, CapturePoint: point.UTC().Format(time.RFC3339Nano), Status: "captured"})
	}
	return resources, nil
}

func validateCapturedProjectEnvironmentDatabaseResources(plans []capturedProjectEnvironmentDatabasePlan, resources []state.ProjectEnvironmentCloneResource) (map[string]int, time.Time, error) {
	indices := map[string]int{}
	var point time.Time
	for i, resource := range resources {
		if resource.Kind != "managed_postgres" && resource.Kind != "postgres" {
			continue
		}
		if resource.Kind != "managed_postgres" || resource.Name != resource.SourceID {
			return nil, point, state.ErrConflict
		}
		if _, duplicate := indices[resource.SourceID]; duplicate {
			return nil, point, state.ErrConflict
		}
		captured, err := time.Parse(time.RFC3339Nano, resource.CapturePoint)
		if err != nil || captured.IsZero() || captured.Nanosecond()%1000 != 0 || resource.CapturePoint != captured.UTC().Format(time.RFC3339Nano) || !point.IsZero() && !point.Equal(captured) {
			return nil, point, state.ErrConflict
		}
		point, indices[resource.SourceID] = captured, i
		switch resource.Status {
		case "captured", "copying", "verifying", "ready":
		default:
			return nil, point, state.ErrConflict
		}
		if resource.TargetID == resource.SourceID || resource.Status == "ready" && resource.TargetID == "" {
			return nil, point, state.ErrConflict
		}
	}
	if len(indices) != len(plans) {
		return nil, point, state.ErrConflict
	}
	now := time.Now().UTC()
	for _, plan := range plans {
		i, ok := indices[plan.source.ID]
		if !ok || resources[i].SourceVersion != plan.hash || !point.Before(now) {
			return nil, point, state.ErrConflict
		}
	}
	return indices, point, nil
}

// Each target reservation is recoverable through its operation-specific name.
// The operation records the target before another copy starts. Provider calls
// are bounded by a renewed worker lease; failed workers never compensate here.
func (s *server) prepareProjectEnvironmentCloneDatabases(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	leases, ok := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	operations, operationsOK := s.store.(state.ProjectEnvironmentCloneOperationStore)
	if !ok || !operationsOK {
		return lease, false, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return lease, false, err
	}
	lease = renewed
	if lease.Operation.Status != state.CloneOperationCopying {
		return lease, false, state.ErrConflict
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	indices, point, err := validateCapturedProjectEnvironmentDatabaseResources(plans, lease.Operation.Resources)
	if err != nil {
		return lease, false, err
	}
	if len(plans) > 0 && s.managedPostgres == nil {
		return lease, false, managedpostgres.ErrUnavailable
	}
	ready := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		copyCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		i := indices[plan.source.ID]
		database, err := s.restoreCapturedProjectEnvironmentDatabase(copyCtx, plan, point, lease.Operation.Resources[i])
		cancel()
		if err != nil {
			return lease, false, fmt.Errorf("restore captured clone database: %w", err)
		}
		resources := append([]state.ProjectEnvironmentCloneResource(nil), lease.Operation.Resources...)
		if err := verifyCapturedProjectEnvironmentDatabaseTarget(plan, point, resources[i], database); err != nil {
			return lease, false, err
		}
		resources[i].TargetID, resources[i].Status = database.ID, "verifying"
		if database.State == managedpostgres.StateReady {
			resources[i].Status = "ready"
		} else {
			ready = false
		}
		if resources[i] == lease.Operation.Resources[i] {
			continue
		}
		op := lease.Operation
		updated, err := operations.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, op.Status, op.Revision, resources, "")
		if err != nil {
			return lease, false, err
		}
		lease.Operation = updated
	}
	return lease, ready, nil
}

func (s *server) restoreCapturedProjectEnvironmentDatabase(ctx context.Context, plan capturedProjectEnvironmentDatabasePlan, point time.Time, resource state.ProjectEnvironmentCloneResource) (managedpostgres.Database, error) {
	var target managedpostgres.Database
	var err error
	if resource.TargetID != "" {
		target, err = s.managedPostgres.Get(ctx, plan.source.AccountID, resource.TargetID)
	} else {
		target, err = s.managedPostgres.FindByName(ctx, plan.source.AccountID, plan.name)
	}
	if err == nil {
		if err := verifyCapturedProjectEnvironmentDatabaseTarget(plan, point, resource, target); err != nil {
			return managedpostgres.Database{}, err
		}
		if target.State == managedpostgres.StateReady {
			return target, nil
		}
	} else if !errors.Is(err, managedpostgres.ErrNotFound) || resource.TargetID != "" {
		return managedpostgres.Database{}, err
	}
	target, _, err = s.managedPostgres.RestoreWithResult(ctx, managedpostgres.RestoreDatabaseRequest{
		AccountID: plan.source.AccountID, SourceDatabaseID: plan.source.ID, Name: plan.name, PointInTime: point,
		SourceDefinition: &managedpostgres.RestoreSourceDefinition{Spec: plan.source.Spec, BackendID: plan.source.BackendID,
			BackendFingerprint: plan.source.BackendFingerprint, ProviderResourceID: plan.source.ProviderResourceID},
	})
	return target, err
}

func verifyCapturedProjectEnvironmentDatabaseTarget(plan capturedProjectEnvironmentDatabasePlan, point time.Time, resource state.ProjectEnvironmentCloneResource, target managedpostgres.Database) error {
	source := plan.source
	if target.ID == "" || target.ID == source.ID || target.AccountID != source.AccountID || target.Name != plan.name ||
		resource.TargetID != "" && resource.TargetID != target.ID || target.Spec != source.Spec || target.BackendID != source.BackendID ||
		target.BackendFingerprint != source.BackendFingerprint || target.RestoreSourceDatabaseID != source.ID ||
		target.RestoreSourceResourceID != source.ProviderResourceID || !target.RestorePointInTime.Equal(point) ||
		(target.State != managedpostgres.StateProvisioning && target.State != managedpostgres.StateReady) {
		return state.ErrConflict
	}
	if target.State == managedpostgres.StateReady && (target.ProviderResourceID == "" || target.ProviderResourceID == source.ProviderResourceID) {
		return state.ErrConflict
	}
	return nil
}
