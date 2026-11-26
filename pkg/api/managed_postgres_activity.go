package api

import "time"

// ManagedPostgresActivity is why a database was awake in a period
// (ADR-20261125231222): GET /v1/postgres/databases/{id}/activity. Causes
// are heuristic: the first of app_wake, cron, request, app_start,
// running_app, external that matches the start. Since is the period's start
// after clamping to the 30-day retention.
type ManagedPostgresActivity struct {
	DatabaseID   string    `json:"database_id"`
	DatabaseName string    `json:"database_name"`
	Since        time.Time `json:"since"`
	Until        time.Time `json:"until"`
	Starts       int       `json:"starts"`
	AwakeSeconds int64     `json:"awake_seconds"`
	// SuspendTimeoutSeconds is the timeout in force; omitted for an
	// always-on database or when it could not be read.
	SuspendTimeoutSeconds int64                                 `json:"suspend_timeout_seconds,omitempty"`
	Causes                []ManagedPostgresActivityCause        `json:"causes"`
	ComputeStarts         []ManagedPostgresActivityComputeStart `json:"compute_starts"`
	// Truncated is true when the period had more starts than
	// compute_starts lists; the totals and causes still count them all.
	Truncated bool                          `json:"truncated,omitempty"`
	Hints     []ManagedPostgresActivityHint `json:"hints"`
	DocsURL   string                        `json:"docs_url"`
}

// ManagedPostgresActivityCause is one cause's share of the period.
type ManagedPostgresActivityCause struct {
	Cause          string    `json:"cause"`
	AppID          string    `json:"app_id,omitempty"`
	AppSlug        string    `json:"app_slug,omitempty"`
	Detail         string    `json:"detail,omitempty"`
	Starts         int       `json:"starts"`
	AwakeSeconds   int64     `json:"awake_seconds"`
	FirstStartedAt time.Time `json:"first_started_at"`
}

// ManagedPostgresActivityComputeStart is one recorded start. SuspendedAt is
// omitted while the compute runs or its suspend has not been read yet.
type ManagedPostgresActivityComputeStart struct {
	StartedAt   time.Time  `json:"started_at"`
	SuspendedAt *time.Time `json:"suspended_at,omitempty"`
	Cause       string     `json:"cause"`
	AppID       string     `json:"app_id,omitempty"`
	AppSlug     string     `json:"app_slug,omitempty"`
	Detail      string     `json:"detail,omitempty"`
}

// ManagedPostgresActivityHint suggests what to do about a cause; Command,
// when set, is the CLI command that acts on it.
type ManagedPostgresActivityHint struct {
	Code    string `json:"code"`
	Cause   string `json:"cause"`
	AppSlug string `json:"app_slug,omitempty"`
	Message string `json:"message"`
	Command string `json:"command,omitempty"`
}

// ManagedPostgresDatabaseUsage is one database's share of the month's
// compute in the usage view, with its recorded starts and awake time.
type ManagedPostgresDatabaseUsage struct {
	DatabaseID         string `json:"database_id"`
	Name               string `json:"name"`
	ComputeUnitSeconds int64  `json:"compute_unit_seconds"`
	Starts             int    `json:"starts"`
	AwakeSeconds       int64  `json:"awake_seconds"`
}
