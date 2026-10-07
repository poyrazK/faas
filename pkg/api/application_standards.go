package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
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
	AppID                       string                  `json:"app_id"`
	OrgID                       string                  `json:"org_id"`
	ProjectID                   string                  `json:"project_id,omitempty"`
	LocalSettings               appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations   []string                `json:"additional_log_destinations"`
	Adoptions                   []appstandards.Adoption `json:"adoptions"`
	MaterializedFields          []appstandards.Field    `json:"materialized_fields"`
	InstalledEffective          *appstandards.Effective `json:"installed_effective,omitempty"`
	InstalledExceptionExpiresAt *time.Time              `json:"installed_exception_expires_at,omitempty"`
	InstalledEffectiveHash      string                  `json:"installed_effective_hash,omitempty"`
	DesiredRevision             int64                   `json:"desired_revision"`
	PersistedRevision           int64                   `json:"persisted_revision"`
	ObservedRevision            int64                   `json:"observed_revision"`
	State                       string                  `json:"state"`
	ErrorCode                   string                  `json:"error_code,omitempty"`
	UpdatedAt                   time.Time               `json:"updated_at"`
}

// A review stores a preview. It never activates an assignment or changes apps.
type ApplicationStandardReviewRequest struct {
	AssignmentID     string `json:"assignment_id,omitempty"`
	Scope            string `json:"scope"`
	ScopeID          string `json:"scope_id"`
	StandardID       string `json:"standard_id"`
	AdmissionVersion int64  `json:"admission_version"`
	ExpectedRevision int64  `json:"expected_revision"`
	Active           bool   `json:"active"`
	BatchSize        int    `json:"batch_size"`
}

func (r *ApplicationStandardReviewRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		AssignmentID     string `json:"assignment_id,omitempty"`
		Scope            string `json:"scope"`
		ScopeID          string `json:"scope_id"`
		StandardID       string `json:"standard_id"`
		AdmissionVersion *int64 `json:"admission_version"`
		ExpectedRevision *int64 `json:"expected_revision"`
		Active           *bool  `json:"active"`
		BatchSize        *int   `json:"batch_size"`
	}
	if err := appstandards.DecodeStrict(raw, &wire); err != nil {
		return err
	}
	if wire.AdmissionVersion == nil || wire.ExpectedRevision == nil || wire.Active == nil || wire.BatchSize == nil {
		return fmt.Errorf("admission_version, expected_revision, active and batch_size are required")
	}
	*r = ApplicationStandardReviewRequest{wire.AssignmentID, wire.Scope, wire.ScopeID, wire.StandardID, *wire.AdmissionVersion, *wire.ExpectedRevision, *wire.Active, *wire.BatchSize}
	return r.Validate()
}

func (r ApplicationStandardReviewRequest) Validate() error {
	for _, raw := range []string{r.ScopeID, r.StandardID} {
		if id, err := uuid.Parse(raw); err != nil || id == uuid.Nil {
			return fmt.Errorf("scope_id and standard_id must be nonzero UUIDs")
		}
	}
	if r.AssignmentID != "" {
		if id, err := uuid.Parse(r.AssignmentID); err != nil || id == uuid.Nil {
			return fmt.Errorf("assignment_id must be a nonzero UUID")
		}
	}
	if !slices.Contains([]string{"organization", "project", "application"}, r.Scope) || r.AdmissionVersion < 1 || r.AdmissionVersion > ApplicationStandardMaxVersion || r.ExpectedRevision < 0 || r.ExpectedRevision >= ApplicationStandardMaxVersion || r.BatchSize < 1 || r.BatchSize > ApplicationStandardMaxRolloutBatch {
		return fmt.Errorf("invalid review scope, version, revision or batch size")
	}
	if (r.AssignmentID == "" && r.ExpectedRevision != 0) || (r.ExpectedRevision == 0 && !r.Active) {
		return fmt.Errorf("a new assignment requires active=true and expected_revision=0")
	}
	return nil
}

type ApplicationStandardReview struct {
	ID           string                             `json:"id"`
	OrgID        string                             `json:"org_id"`
	CreatedBy    string                             `json:"created_by"`
	Request      ApplicationStandardReviewRequest   `json:"request"`
	ApprovalHash string                             `json:"approval_hash"`
	Applications []ApplicationStandardReviewedApp   `json:"applications"`
	Blockers     []ApplicationStandardReviewBlocker `json:"blockers"`
	CreatedAt    time.Time                          `json:"created_at"`
	ExpiresAt    time.Time                          `json:"expires_at"`
}

type ApplicationStandardReviewBlocker struct {
	AppID   string             `json:"app_id,omitempty"`
	Scope   string             `json:"scope,omitempty"`
	ScopeID string             `json:"scope_id,omitempty"`
	Field   appstandards.Field `json:"field,omitempty"`
	Code    string             `json:"code"`
}

// Customer-facing review data excludes private base settings and artifact proofs.
type ApplicationStandardReviewedApp struct {
	AppID                     string                  `json:"app_id"`
	Slug                      string                  `json:"slug"`
	ProjectID                 string                  `json:"project_id,omitempty"`
	DesiredRevision           int64                   `json:"desired_revision"`
	BeforeSettings            appstandards.Settings   `json:"before_settings"`
	BeforeAdoptions           []appstandards.Adoption `json:"before_adoptions"`
	AfterAdoptions            []appstandards.Adoption `json:"after_adoptions"`
	LocalSettings             appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations []string                `json:"additional_log_destinations"`
	Effective                 appstandards.Effective  `json:"effective"`
	ChangedFields             []appstandards.Field    `json:"changed_fields"`
}

type ApplicationStandardOperation struct {
	ID           string                               `json:"id"`
	OrgID        string                               `json:"org_id"`
	PlanID       string                               `json:"plan_id"`
	AssignmentID string                               `json:"assignment_id"`
	ApprovalHash string                               `json:"approval_hash"`
	ApprovedBy   string                               `json:"approved_by"`
	BatchSize    int                                  `json:"batch_size"`
	State        string                               `json:"state"`
	ErrorCode    string                               `json:"error_code,omitempty"`
	Targets      []ApplicationStandardOperationTarget `json:"targets"`
	CreatedAt    time.Time                            `json:"created_at"`
	UpdatedAt    time.Time                            `json:"updated_at"`
}

type ApplicationStandardOperationTarget struct {
	AppID           string                         `json:"app_id"`
	Position        int                            `json:"position"`
	ApprovedApp     ApplicationStandardReviewedApp `json:"approved_app"`
	State           string                         `json:"state"`
	DesiredRevision int64                          `json:"desired_revision"`
	ErrorCode       string                         `json:"error_code,omitempty"`
	UpdatedAt       time.Time                      `json:"updated_at"`
}

type ApplicationStandardException struct {
	ID         string             `json:"id"`
	OrgID      string             `json:"org_id"`
	AppID      string             `json:"app_id"`
	StandardID string             `json:"standard_id"`
	Version    int64              `json:"version"`
	Field      appstandards.Field `json:"field"`
	Value      json.RawMessage    `json:"value"`
	Reason     string             `json:"reason"`
	ExpiresAt  time.Time          `json:"expires_at"`
	ApprovedBy string             `json:"approved_by"`
	CreatedAt  time.Time          `json:"created_at"`
	RevokedBy  string             `json:"revoked_by,omitempty"`
	RevokedAt  *time.Time         `json:"revoked_at,omitempty"`
	Status     string             `json:"status"`
}

type ApplicationStandardExceptionList struct {
	Exceptions    []ApplicationStandardException `json:"exceptions"`
	NextPageAfter string                         `json:"next_page_after,omitempty"`
	AsOf          time.Time                      `json:"as_of"`
}
