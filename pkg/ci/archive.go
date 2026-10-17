package ci

import (
	"context"
	"errors"
	"time"
)

var (
	ErrExecutionArchived = errors.New("CI execution detail has been archived")
	ErrArchiveUnsafe     = errors.New("CI execution cleanup, payload retirement or Check settlement is incomplete")
)

type ExecutionArchive struct {
	Rerun            *ExecutionRerun       `json:"rerun,omitempty"`
	ExecutionID      string                `json:"execution_id"`
	RevisionID       string                `json:"revision_id"`
	Pipeline         string                `json:"pipeline"`
	CommitSHA        string                `json:"commit_sha"`
	Status           string                `json:"status"`
	DefinitionDigest string                `json:"definition_digest"`
	ProfileID        string                `json:"profile_id,omitempty"`
	Automation       *AutomationProvenance `json:"automation,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	FinishedAt       time.Time             `json:"finished_at"`
	ArchivedAt       time.Time             `json:"archived_at"`
	Jobs             []ArchivedJob         `json:"jobs"`
	Check            *ArchivedCheck        `json:"check,omitempty"`
}

type ArchivedJob struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	AttemptID   string            `json:"attempt_id,omitempty"`
	TestReports *TestReportTotals `json:"test_reports,omitempty"`
}

type ArchivedCheck struct {
	Status           string       `json:"status"`
	CheckID          int64        `json:"check_id,omitempty"`
	DeliveredVersion int64        `json:"delivered_version"`
	LastError        string       `json:"last_error,omitempty"`
	RetryGeneration  int32        `json:"retry_generation"`
	Retries          []CheckRetry `json:"retries"`
}

type ArchiveStore interface {
	ArchiveExecution(context.Context, string, string) (ExecutionArchive, bool, error)
	ExecutionArchive(context.Context, string, string) (ExecutionArchive, error)
	ExecutionArchives(context.Context, string) ([]ExecutionArchive, error)
}
