package state

import (
	"context"
	"time"
)

// ProjectEnvironmentCleanupResources is the durable, non-secret description
// of provider resources that must be removed after an environment is deleted.
// It deliberately stores opaque resource identifiers only.
type ProjectEnvironmentCleanupResources struct {
	Postgres      []ProjectEnvironmentPostgresCleanupResource      `json:"postgres,omitempty"`
	ObjectStorage []ProjectEnvironmentObjectStorageCleanupResource `json:"object_storage,omitempty"`
}

func (r ProjectEnvironmentCleanupResources) Empty() bool {
	return len(r.Postgres) == 0 && len(r.ObjectStorage) == 0
}

func (r ProjectEnvironmentCleanupResources) ValidateForEnvironment(slug string) error {
	if r.Empty() || slug == "" {
		return ErrInvalidArgument
	}
	for _, resource := range r.Postgres {
		if resource.AppID == "" || resource.Scope != slug || resource.BindingID == "" || resource.DatabaseID == "" {
			return ErrInvalidArgument
		}
		if resource.DeleteDatabase && (resource.DatabaseName == "" || resource.RestoreSourceDatabaseID == "") {
			return ErrInvalidArgument
		}
	}
	for _, resource := range r.ObjectStorage {
		if resource.AppID == "" || resource.Scope != slug || resource.BucketID == "" ||
			(resource.CredentialID == "" && !resource.DeleteBucket) ||
			(resource.DeleteBucket && resource.SourceBucketID == "") {
			return ErrInvalidArgument
		}
	}
	return nil
}

type ProjectEnvironmentPostgresCleanupResource struct {
	AppID                   string `json:"app_id"`
	Scope                   string `json:"scope"`
	BindingID               string `json:"binding_id"`
	DatabaseID              string `json:"database_id"`
	DatabaseName            string `json:"database_name,omitempty"`
	RestoreSourceDatabaseID string `json:"restore_source_database_id,omitempty"`
	DeleteDatabase          bool   `json:"delete_database,omitempty"`
}

type ProjectEnvironmentObjectStorageCleanupResource struct {
	AppID          string `json:"app_id"`
	Scope          string `json:"scope"`
	BucketID       string `json:"bucket_id"`
	CredentialID   string `json:"credential_id,omitempty"`
	DeleteBucket   bool   `json:"delete_bucket,omitempty"`
	SourceBucketID string `json:"source_bucket_id,omitempty"`
}

type ProjectEnvironmentCleanupJob struct {
	ID              string
	AccountID       string
	ProjectID       string
	EnvironmentSlug string
	Resources       ProjectEnvironmentCleanupResources
	AttemptCount    int
	NextAttemptAt   time.Time
	LeaseToken      string
	LeaseUntil      time.Time
	CreatedAt       time.Time
}

// ProjectEnvironmentCleanupStore is an optional state capability implemented
// by durable stores. Deleting the environment and enqueuing its provider
// cleanup happen in one transaction; cleanup claims are leased so apid
// instances can safely resume jobs after crashes.
type ProjectEnvironmentCleanupStore interface {
	DeleteProjectEnvironmentWithCleanup(context.Context, string, string, string, ProjectEnvironmentCleanupResources, string, time.Duration) (ProjectEnvironmentCleanupJob, error)
	ClaimNextProjectEnvironmentCleanup(context.Context, string, time.Time, time.Duration) (ProjectEnvironmentCleanupJob, error)
	RetryProjectEnvironmentCleanup(context.Context, string, string, time.Time) error
	CompleteProjectEnvironmentCleanup(context.Context, string, string) error
}
