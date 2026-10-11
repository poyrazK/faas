package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Planned worst-case credits, never actual CPU/SQL usage or a billing receipt.
// Every allocation remains charged, even when its native dispatch is uncertain.
type ProjectEnvironmentClonePostgresVerificationReadBudget struct {
	Scope                                                         copyinventory.Scope
	DatabaseOID                                                   uint32
	OriginalVerificationID                                        string
	ReadBytes, SortMemoryBytes, SortDiskBytes, AllocatedReadBytes int64
	CreatedAt                                                     time.Time
	Allocations                                                   []ProjectEnvironmentClonePostgresVerificationReadAllocation
}
type ProjectEnvironmentClonePostgresVerificationReadAllocation struct {
	Attempt                                   int32
	VerificationID                            string
	ReadBytes, SortMemoryBytes, SortDiskBytes int64
	CreatedAt                                 time.Time
}

func (ProjectEnvironmentClonePostgresVerificationReadBudget) String() string {
	return "private PostgreSQL verification read budget"
}
func (b ProjectEnvironmentClonePostgresVerificationReadBudget) GoString() string { return b.String() }
func (ProjectEnvironmentClonePostgresVerificationReadBudget) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{})
}

type ProjectEnvironmentClonePostgresVerificationReadBudgetRequest struct {
	Scope                                     copyinventory.Scope
	DatabaseOID                               uint32
	OriginalVerificationID                    string
	ReadBytes, SortMemoryBytes, SortDiskBytes int64
}

// Temporary structural account holds. Qualified retirement, production work
// placement, actual usage accounting and billing remain separate requirements.
type ProjectEnvironmentClonePostgresVerificationReadBudgetLimits struct {
	Count int
	Bytes int64
}
type ProjectEnvironmentClonePostgresVerificationReadRequest struct {
	SourceDatabaseID                          string
	DatabaseOID                               uint32
	VerificationID                            string
	Attempt                                   int32
	ReadBytes, SortMemoryBytes, SortDiskBytes int64
}
type ProjectEnvironmentClonePostgresVerificationReadBudgetStore interface {
	ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresVerificationReadBudget, error)
	ReserveProjectEnvironmentClonePostgresVerificationReadBudget(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, ProjectEnvironmentClonePostgresVerificationReadBudgetLimits) (ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error)
	AllocateProjectEnvironmentClonePostgresVerificationRead(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresVerificationReadRequest) (ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error)
}

var _ ProjectEnvironmentClonePostgresVerificationReadBudgetStore = (*PgStore)(nil)
