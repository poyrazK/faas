package api

import (
	"context"
	"net/url"
	"time"
)

const (
	BindingTypeService       = "service"
	BindingTypePostgres      = "postgres"
	BindingTypeObjectStorage = "object_storage"
	BindingTypeQueue         = "queue"
	BindingTypeOutbound      = "outbound"
)

// AppBindingInventory is a best-effort metadata projection, not an atomic
// snapshot or a connectivity check. Complete means every binding section
// could be read. Omitted sections always have a structured issue.
type AppBindingInventory struct {
	App                      string                    `json:"app"`
	Scope                    string                    `json:"scope,omitempty"`
	GeneratedAt              time.Time                 `json:"generated_at"`
	Complete                 bool                      `json:"complete"`
	Bindings                 []AppBindingInventoryItem `json:"bindings"`
	Issues                   []BindingInventoryIssue   `json:"issues,omitempty"`
	Warnings                 []string                  `json:"warnings,omitempty"`
	VerificationDeploymentID string                    `json:"verification_deployment_id,omitempty"`
	RequestedDeploymentID    string                    `json:"requested_deployment_id,omitempty"`
	VerificationScope        string                    `json:"verification_scope,omitempty"`
	RuntimeFreshness         *BindingRuntimeFreshness  `json:"runtime_freshness,omitempty"`
}

// AppBindingInventoryItem contains only public binding metadata. State is
// configuration/provisioning state; RuntimeStatus is last-known observation
// and VerificationStatus reports revision-fenced task-guest canary evidence.
// Credential material, credential IDs, provider origins and raw errors are
// deliberately absent from this contract.
type AppBindingInventoryItem struct {
	Type                 string                      `json:"type"`
	Name                 string                      `json:"name"`
	Binding              string                      `json:"binding"`
	Scope                string                      `json:"scope"`
	Access               string                      `json:"access"`
	State                string                      `json:"state"`
	RuntimeStatus        string                      `json:"runtime_status"`
	VerificationStatus   string                      `json:"verification_status"`
	Verification         *BindingVerification        `json:"verification,omitempty"`
	Refresh              *BindingRefresh             `json:"refresh,omitempty"`
	ApplicationAdoption  *BindingApplicationAdoption `json:"application_adoption,omitempty"`
	ObservedAt           *time.Time                  `json:"observed_at,omitempty"`
	HTTPURL              string                      `json:"http_url,omitempty"`
	HTTPSEnv             string                      `json:"https_env,omitempty"`
	HTTPSURL             string                      `json:"https_url,omitempty"`
	Transport            string                      `json:"transport,omitempty"`
	CredentialGeneration *int64                      `json:"credential_generation,omitempty"`
	RotationPending      *bool                       `json:"rotation_pending,omitempty"`
	ConsumerState        string                      `json:"consumer_state,omitempty"`
	ConsumerStateReason  string                      `json:"consumer_state_reason,omitempty"`
	ConsumerLiveness     string                      `json:"consumer_liveness,omitempty"`
	CredentialConfigured *bool                       `json:"credential_configured,omitempty"`
	OutboundProbe        *OutboundBindingProbePolicy `json:"outbound_probe,omitempty"`
	AllowedMethods       []string                    `json:"allowed_methods,omitempty"`
	AllowedPathPrefixes  []string                    `json:"allowed_path_prefixes,omitempty"`
}

// BindingVerification describes the latest admitted platform canary, never a
// resident application's acknowledgement. Check details contain stable status
// values only; task output, provider errors and credential material are absent.
type BindingVerification struct {
	Result               string                     `json:"result"`
	Reason               string                     `json:"reason,omitempty"`
	Source               string                     `json:"source"`
	DeploymentID         string                     `json:"deployment_id"`
	Scope                string                     `json:"scope"`
	CheckedAt            *time.Time                 `json:"checked_at,omitempty"`
	CredentialGeneration *int64                     `json:"credential_generation,omitempty"`
	Checks               []BindingVerificationCheck `json:"checks,omitempty"`
}

type BindingVerificationCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// BindingRuntimeFreshness compares resident instance admission timestamps to
// the app-wide configuration stamp. It is not a guest acknowledgement, proof
// of credential use, readiness, or connectivity. A missing stamp is unknown.
type BindingRuntimeFreshness struct {
	Source          string                     `json:"source"`
	ObservedAt      time.Time                  `json:"observed_at"`
	ConfigChangedAt *time.Time                 `json:"config_changed_at,omitempty"`
	Deployments     []BindingRuntimeDeployment `json:"deployments"`
}

type BindingRuntimeDeployment struct {
	DeploymentID     string                       `json:"deployment_id"`
	Scope            string                       `json:"scope"`
	DeploymentStatus string                       `json:"deployment_status"`
	Status           string                       `json:"status"`
	Serving          BindingRuntimeInstanceCounts `json:"serving"`
	Resident         BindingRuntimeInstanceCounts `json:"resident"`
	Starting         int                          `json:"starting"`
}

// Serving counts running app instances. Resident also includes starting,
// warm, snapshotting, draining and migrating instances.
// Task guests, jobs, mirrors and terminal/parked rows are excluded.
type BindingRuntimeInstanceCounts struct {
	Current int `json:"current"`
	Stale   int `json:"stale"`
	Unknown int `json:"unknown"`
}

// BindingRefresh reports the durable restart handoff for a pending managed
// binding rotation. Completion does not imply runtime freshness or probe success.
type BindingRefresh struct {
	WakeID        string     `json:"wake_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	FailureReason string     `json:"failure_reason,omitempty"`
	RequestedAt   *time.Time `json:"requested_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

// BindingInventoryIssue uses stable codes and sanitized messages. Severity
// "warning" identifies an unconfigured optional feature; "error" identifies
// a failed or forbidden read. Both make Complete false.
type BindingInventoryIssue struct {
	Type     string `json:"type"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func (i AppBindingInventory) HasErrors() bool {
	for _, issue := range i.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

// GetAppBindingInventory includes all resource scopes when scope is empty.
// With a filter, app-wide service, queue and outbound bindings remain included.
func (c *Client) GetAppBindingInventory(ctx context.Context, slug, scope string) (AppBindingInventory, error) {
	return c.GetAppBindingInventoryForDeployment(ctx, slug, scope, "")
}

// GetAppBindingInventoryForDeployment selects evidence for an exact live
// deployment. Empty deploymentID preserves the manual-task selection.
func (c *Client) GetAppBindingInventoryForDeployment(ctx context.Context, slug, scope, deploymentID string) (AppBindingInventory, error) {
	var out AppBindingInventory
	path := "/v1/apps/" + url.PathEscape(slug) + "/bindings"
	query := url.Values{}
	if scope != "" {
		query.Set("scope", scope)
	}
	if deploymentID != "" {
		query.Set("deployment_id", deploymentID)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

const DefaultBindingVerificationAge = 10 * time.Minute

type BindingCheckFinding struct {
	Code         string `json:"code"`
	Type         string `json:"type,omitempty"`
	Name         string `json:"name,omitempty"`
	Binding      string `json:"binding,omitempty"`
	Scope        string `json:"scope,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Message      string `json:"message"`
}

type BindingCheckBindingResult struct {
	Type                string                      `json:"type"`
	Name                string                      `json:"name"`
	Binding             string                      `json:"binding,omitempty"`
	Scope               string                      `json:"scope"`
	Status              string                      `json:"status"`
	Reason              string                      `json:"reason,omitempty"`
	VerificationStatus  string                      `json:"verification_status,omitempty"`
	CheckedAt           *time.Time                  `json:"checked_at,omitempty"`
	RefreshStatus       string                      `json:"refresh_status,omitempty"`
	ApplicationAdoption *BindingApplicationAdoption `json:"application_adoption,omitempty"`
}

// Report.Passed means the declared policy passed at CheckedAt. Optional
// application acknowledgements are self-attestations, not independent proof
// of readiness or credential use. Coverage is complete, partial, or none.
type BindingCheckReport struct {
	ExpectedDeploymentID  string                      `json:"expected_deployment_id,omitempty"`
	App                   string                      `json:"app"`
	Scope                 string                      `json:"scope"`
	DeploymentID          string                      `json:"deployment_id"`
	CheckedAt             time.Time                   `json:"checked_at"`
	InventoryGeneratedAt  time.Time                   `json:"inventory_generated_at"`
	MaxVerificationAge    string                      `json:"max_verification_age"`
	AllowUnsupported      bool                        `json:"allow_unsupported"`
	RequireApplicationAck bool                        `json:"require_application_ack"`
	Passed                bool                        `json:"passed"`
	Coverage              string                      `json:"coverage"`
	Bindings              []BindingCheckBindingResult `json:"bindings"`
	Runtime               []BindingRuntimeDeployment  `json:"runtime"`
	Issues                []BindingInventoryIssue     `json:"issues"`
	Blockers              []BindingCheckFinding       `json:"blockers"`
	Warnings              []BindingCheckFinding       `json:"warnings"`
}

// BindingPromotionRequest requires passed binding evidence for this promotion.
// The dedicated route prevents an older server from silently ignoring the gate.
type BindingPromotionRequest struct {
	ExpectedServingDeploymentID *string `json:"expected_serving_deployment_id,omitempty"`
	MaxVerificationAge          string  `json:"max_verification_age,omitempty"`
	AllowUnsupported            bool    `json:"allow_unsupported,omitempty"`
	RequireApplicationAck       bool    `json:"require_application_ack,omitempty"`
}

type BindingPromotionResponse struct {
	Deployment      DeploymentResponse  `json:"deployment"`
	FromPercent     int                 `json:"from_percent"`
	ToPercent       int                 `json:"to_percent"`
	AlreadyPromoted bool                `json:"already_promoted"`
	BindingsCheck   *BindingCheckReport `json:"bindings_check,omitempty"`
}

// PromoteDeploymentWithBindings fails without changing traffic when bindings
// do not pass or their observations change before the atomic traffic update.
func (c *Client) PromoteDeploymentWithBindings(ctx context.Context, id string, request BindingPromotionRequest) (BindingPromotionResponse, error) {
	var out BindingPromotionResponse
	path := "/v1/deployments/" + url.PathEscape(id) + "/promote"
	if request.RequireApplicationAck {
		path += "-with-application-ack"
	}
	err := c.do(ctx, "POST", path, request, &out)
	return out, err
}
