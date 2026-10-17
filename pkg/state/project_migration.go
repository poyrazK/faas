// adr:1575 — account-owned multi-service migration journal.
package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectMigrationRecord struct {
	AccountID           string
	Version             int64
	RequestDigest       string
	BeforeDeploymentIDs map[string]string
	LeaseUntil          time.Time
	Report              api.ProjectMigration
}
type ProjectMigrationStore interface {
	CreateProjectMigration(context.Context, ProjectMigrationRecord) (ProjectMigrationRecord, error)
	GetProjectMigration(context.Context, string, string) (ProjectMigrationRecord, error)
	ListProjectMigrations(context.Context, string, string) ([]ProjectMigrationRecord, error)
	SaveProjectMigration(context.Context, ProjectMigrationRecord, int64) (ProjectMigrationRecord, error)
}

func cloneProjectMigration(r ProjectMigrationRecord) ProjectMigrationRecord {
	raw, _ := json.Marshal(r)
	var out ProjectMigrationRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
