package api

import (
	"encoding/json"
	"time"
)

type SetApplicationStandardLocalIntentRequest struct {
	ExpectedRevision          int64           `json:"expected_revision"`
	Settings                  json.RawMessage `json:"settings"`
	AdditionalLogDestinations []string        `json:"additional_log_destinations"`
}

type ApproveApplicationStandardExceptionRequest struct {
	ExpectedRevision int64           `json:"expected_revision"`
	StandardID       string          `json:"standard_id"`
	Version          int64           `json:"version"`
	Field            string          `json:"field"`
	Value            json.RawMessage `json:"value"`
	Reason           string          `json:"reason"`
	ExpiresAt        time.Time       `json:"expires_at"`
}

type RevokeApplicationStandardExceptionRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type CreateApplicationStandardVersionRequest struct {
	Description     string          `json:"description"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

type ApplicationStandardVersion struct {
	StandardID     string                        `json:"standard_id"`
	OrgID          string                        `json:"org_id"`
	Slug           string                        `json:"slug"`
	Version        int64                         `json:"version"`
	Definition     ApplicationStandardDefinition `json:"definition"`
	DefinitionHash string                        `json:"definition_hash"`
	Description    string                        `json:"description"`
	CreatedBy      string                        `json:"created_by"`
	CreatedAt      time.Time                     `json:"created_at"`
}

type ApplicationStandardList struct {
	Standards     []ApplicationStandardVersion `json:"standards"`
	NextPageAfter string                       `json:"next_page_after,omitempty"`
}

type ApplicationStandardEnrollment struct {
	AppID                       string                        `json:"app_id"`
	OrgID                       string                        `json:"org_id"`
	ProjectID                   string                        `json:"project_id,omitempty"`
	LocalSettings               ApplicationStandardSettings   `json:"local_settings"`
	AdditionalLogDestinations   []string                      `json:"additional_log_destinations"`
	Adoptions                   []ApplicationStandardAdoption `json:"adoptions"`
	MaterializedFields          []string                      `json:"materialized_fields"`
	InstalledEffective          *ApplicationStandardEffective `json:"installed_effective,omitempty"`
	InstalledExceptionExpiresAt *time.Time                    `json:"installed_exception_expires_at,omitempty"`
	InstalledEffectiveHash      string                        `json:"installed_effective_hash,omitempty"`
	DesiredRevision             int64                         `json:"desired_revision"`
	PersistedRevision           int64                         `json:"persisted_revision"`
	ObservedRevision            int64                         `json:"observed_revision"`
	State                       string                        `json:"state"`
	ErrorCode                   string                        `json:"error_code,omitempty"`
	UpdatedAt                   time.Time                     `json:"updated_at"`
}

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
	AppID   string `json:"app_id,omitempty"`
	Scope   string `json:"scope,omitempty"`
	ScopeID string `json:"scope_id,omitempty"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
}

type ApplicationStandardReviewedApp struct {
	AppID                     string                        `json:"app_id"`
	Slug                      string                        `json:"slug"`
	ProjectID                 string                        `json:"project_id,omitempty"`
	DesiredRevision           int64                         `json:"desired_revision"`
	BeforeSettings            ApplicationStandardSettings   `json:"before_settings"`
	BeforeAdoptions           []ApplicationStandardAdoption `json:"before_adoptions"`
	AfterAdoptions            []ApplicationStandardAdoption `json:"after_adoptions"`
	LocalSettings             ApplicationStandardSettings   `json:"local_settings"`
	AdditionalLogDestinations []string                      `json:"additional_log_destinations"`
	Effective                 ApplicationStandardEffective  `json:"effective"`
	ChangedFields             []string                      `json:"changed_fields"`
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
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	AppID      string          `json:"app_id"`
	StandardID string          `json:"standard_id"`
	Version    int64           `json:"version"`
	Field      string          `json:"field"`
	Value      json.RawMessage `json:"value"`
	Reason     string          `json:"reason"`
	ExpiresAt  time.Time       `json:"expires_at"`
	ApprovedBy string          `json:"approved_by"`
	CreatedAt  time.Time       `json:"created_at"`
	RevokedBy  string          `json:"revoked_by,omitempty"`
	RevokedAt  *time.Time      `json:"revoked_at,omitempty"`
	Status     string          `json:"status"`
}

type ApplicationStandardExceptionList struct {
	Exceptions    []ApplicationStandardException `json:"exceptions"`
	NextPageAfter string                         `json:"next_page_after,omitempty"`
	AsOf          time.Time                      `json:"as_of"`
}

type CreateApplicationStandardLogDestinationRequest struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	TargetURL  string `json:"target_url"`
	AuthHeader string `json:"auth_header,omitempty"`
}

type ApplicationStandardLogDestination struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	TargetURL     string    `json:"target_url"`
	HasAuthHeader bool      `json:"has_auth_header"`
	ConfigHash    string    `json:"config_hash"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type ApplicationStandardLogDestinationList struct {
	Destinations  []ApplicationStandardLogDestination `json:"destinations"`
	NextPageAfter string                              `json:"next_page_after,omitempty"`
}

type CreateApplicationStandardPublisherRequest struct {
	Name         string `json:"name"`
	PublicKeyDER string `json:"public_key_der"` // base64 ECDSA P-256 SubjectPublicKeyInfo
}

type ApplicationStandardPublisher struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Name         string    `json:"name"`
	PublicKeyDER string    `json:"public_key_der"`
	Fingerprint  string    `json:"fingerprint"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

type ApplicationStandardPublisherList struct {
	Publishers    []ApplicationStandardPublisher `json:"publishers"`
	NextPageAfter string                         `json:"next_page_after,omitempty"`
}

// Settings contain normalized JSON values from the supported six fields.
type ApplicationStandardSettings map[string]json.RawMessage
type ApplicationStandardDefinition map[string]ApplicationStandardRule
type ApplicationStandardRule struct {
	Mode     string          `json:"mode"`
	Value    json.RawMessage `json:"value"`
	Override string          `json:"override,omitempty"`
}
type ApplicationStandardAdoption struct {
	AssignmentID string `json:"assignment_id"`
	Version      int64  `json:"version"`
}
type ApplicationStandardSource struct {
	AssignmentID string `json:"assignment_id,omitempty"`
	ScopeID      string `json:"scope_id,omitempty"`
	StandardID   string `json:"standard_id"`
	Version      int64  `json:"version"`
	Scope        string `json:"scope"`
	Mode         string `json:"mode"`
	Override     string `json:"override"`
	ExceptionID  string `json:"exception_id,omitempty"`
}
type ApplicationStandardViolation struct {
	Field  string                    `json:"field"`
	Code   string                    `json:"code"`
	Source ApplicationStandardSource `json:"source"`
}
type ApplicationStandardEffective struct {
	Values     ApplicationStandardSettings            `json:"values"`
	Sources    map[string][]ApplicationStandardSource `json:"sources"`
	Violations []ApplicationStandardViolation         `json:"violations"`
}
