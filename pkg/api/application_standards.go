package api

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"time"
)

func ApplicationStandardResolverLimits() appstandards.Limits {
	return appstandards.Limits{DefinitionBytes: ApplicationStandardMaxDefinitionBytes, SetEntries: ApplicationStandardMaxSetEntries, Layers: ApplicationStandardMaxLayers}
}

type CreateApplicationStandardVersionRequest struct {
	Description     string          `json:"description"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

func (request *CreateApplicationStandardVersionRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Description     string          `json:"description"`
		ExpectedVersion *int64          `json:"expected_version"`
		Definition      json.RawMessage `json:"definition"`
	}
	if err := appstandards.DecodeStrict(raw, &wire); err != nil {
		return err
	}
	if wire.ExpectedVersion == nil || len(wire.Definition) == 0 {
		return fmt.Errorf("expected_version and definition are required")
	}
	request.Description, request.ExpectedVersion, request.Definition = wire.Description, *wire.ExpectedVersion, wire.Definition
	return nil
}

type ApplicationStandardVersion struct {
	StandardID     string                  `json:"standard_id"`
	OrgID          string                  `json:"org_id"`
	Slug           string                  `json:"slug"`
	Version        int64                   `json:"version"`
	Definition     appstandards.Definition `json:"definition"`
	DefinitionHash string                  `json:"definition_hash"`
	Description    string                  `json:"description"`
	CreatedBy      string                  `json:"created_by"`
	CreatedAt      time.Time               `json:"created_at"`
}

type ApplicationStandardList struct {
	Standards     []ApplicationStandardVersion `json:"standards"`
	NextPageAfter string                       `json:"next_page_after,omitempty"`
}

// Enrollment separates durable desired intent from the last installed
// projection. Persisted settings alone do not establish consumer observation.
type ApplicationStandardEnrollment struct {
	AppID                     string                  `json:"app_id"`
	OrgID                     string                  `json:"org_id"`
	ProjectID                 string                  `json:"project_id,omitempty"`
	LocalSettings             appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations []string                `json:"additional_log_destinations"`
	Adoptions                 []appstandards.Adoption `json:"adoptions"`
	MaterializedFields        []appstandards.Field    `json:"materialized_fields"`
	InstalledEffective        *appstandards.Effective `json:"installed_effective,omitempty"`
	InstalledEffectiveHash    string                  `json:"installed_effective_hash,omitempty"`
	DesiredRevision           int64                   `json:"desired_revision"`
	PersistedRevision         int64                   `json:"persisted_revision"`
	ObservedRevision          int64                   `json:"observed_revision"`
	State                     string                  `json:"state"`
	ErrorCode                 string                  `json:"error_code,omitempty"`
	UpdatedAt                 time.Time               `json:"updated_at"`
}
