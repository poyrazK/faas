package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Database reservations are private clone intent, committed before provider IO.
// The store derives configuration and lineage from the authenticated capture;
// callers supply only the source identity and their current worker authority.
type ProjectEnvironmentCloneDatabaseStore interface {
	ReserveProjectEnvironmentCloneDatabase(context.Context, ProjectEnvironmentCloneLease, string, int) (ProjectEnvironmentCloneDatabaseTarget, bool, error)
	ProjectEnvironmentCloneDatabaseForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneDatabaseTarget, error)
}

type ProjectEnvironmentCloneDatabaseTarget struct {
	ID    string
	State string
}

func ProjectEnvironmentCloneDatabaseName(op ProjectEnvironmentCloneOperation, sourceID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{op.ProjectID, op.ID, op.TargetEnvironment, sourceID}, "\x00")))
	return "env-" + op.TargetEnvironment + "-" + hex.EncodeToString(sum[:6])
}

// Keep this encoding identical to the database phase's existing definition
// receipts. Credential identity/access are binding inputs, not database inputs.
func ProjectEnvironmentCloneDatabaseSourceHash(b ProjectEnvironmentClonePostgresBinding) (string, error) {
	definition := struct {
		Spec struct {
			Region               string
			PostgresMajor        int
			Class                string
			Availability         string
			ScaleToZero          bool
			StorageLimitBytes    int64
			RestoreWindowSeconds int64
		}
		BackendID, BackendFingerprint, ProviderResourceID string
	}{}
	definition.Spec.Region, definition.Spec.PostgresMajor = b.Region, b.PostgresMajor
	definition.Spec.Class, definition.Spec.Availability, definition.Spec.ScaleToZero = b.ServiceClass, b.Availability, b.ScaleToZero
	definition.Spec.StorageLimitBytes, definition.Spec.RestoreWindowSeconds = b.StorageLimitBytes, b.RestoreWindowSeconds
	definition.BackendID, definition.BackendFingerprint, definition.ProviderResourceID = b.BackendID, b.BackendFingerprint, b.ProviderResourceID
	raw, err := json.Marshal(struct {
		ID, Name   string
		Definition any
	}{b.DatabaseID, b.DatabaseName, definition})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func capturedCloneDatabaseReservation(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, sourceID string) (ProjectEnvironmentClonePostgresBinding, ProjectEnvironmentCloneResource, time.Time, error) {
	sources := map[string]ProjectEnvironmentClonePostgresBinding{}
	for _, view := range views {
		for _, source := range view.Postgres {
			if !validClonePostgresBinding(source) {
				return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrProjectEnvironmentCloneBindingCapture
			}
			if previous, ok := sources[source.DatabaseID]; ok && !sameClonePostgresDatabase(previous, source) {
				return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrProjectEnvironmentCloneBindingCapture
			}
			sources[source.DatabaseID] = source
		}
	}
	resources := map[string]ProjectEnvironmentCloneResource{}
	var point time.Time
	for _, resource := range op.Resources {
		if resource.Kind != "managed_postgres" && resource.Kind != "postgres" {
			continue
		}
		source, ok := sources[resource.SourceID]
		hash, err := ProjectEnvironmentCloneDatabaseSourceHash(source)
		at, parseErr := time.Parse(time.RFC3339Nano, resource.CapturePoint)
		_, duplicate := resources[resource.SourceID]
		if !ok || duplicate || err != nil || parseErr != nil || resource.Kind != "managed_postgres" || resource.Name != source.DatabaseID ||
			resource.SourceVersion != hash || at.IsZero() || at.Nanosecond()%1000 != 0 || resource.CapturePoint != at.UTC().Format(time.RFC3339Nano) ||
			!point.IsZero() && !point.Equal(at) || resource.TargetID == source.DatabaseID || resource.Status == "ready" && resource.TargetID == "" {
			return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrConflict
		}
		switch resource.Status {
		case "captured", "copying", "verifying", "ready":
		default:
			return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrConflict
		}
		point, resources[resource.SourceID] = at, resource
	}
	if len(resources) != len(sources) || !point.Before(time.Now().UTC()) {
		return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrConflict
	}
	source, ok := sources[sourceID]
	if !ok {
		return ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrNotFound
	}
	return source, resources[sourceID], point, nil
}

var _ ProjectEnvironmentCloneDatabaseStore = (*PgStore)(nil)
