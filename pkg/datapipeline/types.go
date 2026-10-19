// Package datapipeline owns the managed Postgres mirror lifecycle. Customer
// intent is written by apid; isolated apid workers reconcile native replication.
package datapipeline

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrInvalid       = errors.New("invalid pipeline request")
	ErrNotFound      = errors.New("pipeline resource not found")
	ErrConflict      = errors.New("pipeline resource changed or is in use")
	ErrUnavailable   = errors.New("data pipelines are unavailable")
	ErrQuota         = errors.New("data pipeline limit reached")
	ErrCheckRequired = errors.New("a fresh passing connection check is required for every selected table")
	identifier       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

type Connection struct {
	api.DataConnection
	AccountID  string
	Ciphertext []byte
	Identity   string
}
type Pipeline struct {
	ReaderChange         *ReaderChange
	VerificationProgress *VerificationProgress
	SchemaChange         *SchemaChange
	SchemaApproval       string
	api.DataPipeline
	AccountID       string
	LeaseToken      string
	LeaseUntil      time.Time
	VerifyRequested bool
	Initialized     bool
	SourceRevision  int64
}
type Observation struct {
	ReaderOnly                  bool
	ReaderChange                *ReaderChange
	VerificationProgress        *VerificationProgress
	SchemaChange                *SchemaChange
	Initialized                 bool
	State, Reason, Verification string
	TablesReady                 int
	RetainedWALBytes            *int64
	AppliedThroughAt            time.Time
	VerifiedAt                  time.Time
}

func ValidateCreate(r api.CreateDataPipelineRequest) error {
	if !api.ValidAppSlug(r.Name) || !validID(r.SourceConnectionID) || !validID(r.DatabaseID) || len(r.Tables) == 0 || len(r.Tables) > api.DataPipelineTablesMax || len(r.ReadSubjects) == 0 || len(r.ReadSubjects) > api.DataPipelineSubjectsMax {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, table := range r.Tables {
		if !identifier.MatchString(table) || strings.HasPrefix(table, "gregale_") || seen[table] {
			return ErrInvalid
		}
		seen[table] = true
	}
	seen = map[string]bool{}
	for _, subject := range r.ReadSubjects {
		if subject == "" || len(subject) > api.DataPipelineSubjectMaxBytes || strings.ContainsAny(subject, "\x00\r\n") || seen[subject] {
			return ErrInvalid
		}
		seen[subject] = true
	}
	return nil
}
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }

type Store interface {
	CreateConnection(context.Context, Connection) (Connection, error)
	Connections(context.Context, string) ([]Connection, error)
	Connection(context.Context, string, string) (Connection, error)
	RotateConnection(context.Context, Connection, int64) (Connection, error)
	DeleteConnection(context.Context, string, string) error
	Create(context.Context, Pipeline) (Pipeline, error)
	List(context.Context, string) ([]Pipeline, error)
	Get(context.Context, string, string) (Pipeline, error)
	Control(context.Context, string, string, int64, string) (Pipeline, error)
	Claim(context.Context, string) (Pipeline, error)
	Finish(context.Context, Pipeline, Observation) error
	TargetInUse(context.Context, string, string) (bool, error)
	RequestCheck(context.Context, string, string, int64, string) (ConnectionCheck, error)
	Check(context.Context, string, string) (ConnectionCheck, error)
	ClaimCheck(context.Context, string) (ConnectionCheck, error)
	FinishCheck(context.Context, ConnectionCheck, api.DataConnectionCheck) error
}

type ConnectionCheck struct {
	api.DataConnectionCheck
	AccountID  string
	LeaseToken string
}

func normalize(r api.CreateDataPipelineRequest) api.CreateDataPipelineRequest {
	r.Tables = slices.Clone(r.Tables)
	r.ReadSubjects = slices.Clone(r.ReadSubjects)
	slices.Sort(r.Tables)
	slices.Sort(r.ReadSubjects)
	return r
}

// QuotaError carries safe admission evidence without connection metadata.
type QuotaError struct{ Limit, Observed int64 }

func (e *QuotaError) Error() string { return ErrQuota.Error() }
func (e *QuotaError) Unwrap() error { return ErrQuota }
