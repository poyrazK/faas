package api

import "time"

const (
	MaxPlatformTenantHostnameSuffixes = 32
	MaxPlatformTenantDelegatedHosts   = 100
)

type CreatePlatformTenantRequest struct {
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
}

type SetPlatformTenantHostnamePolicyRequest struct {
	AllowedSuffixes []string `json:"allowed_suffixes"`
	MaxHostnames    *int     `json:"max_hostnames"`
}

type PlatformTenantHostnamePolicyResponse struct {
	TenantID        string     `json:"tenant_id"`
	Enabled         bool       `json:"enabled"`
	AllowedSuffixes []string   `json:"allowed_suffixes"`
	MaxHostnames    int        `json:"max_hostnames"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type CreatePlatformTenantSelfHostnameRequest struct {
	SurfaceID string `json:"surface_id"`
	Hostname  string `json:"hostname"`
}

type PlatformTenantSelfHostnameResponse struct {
	SurfaceID      string `json:"surface_id"`
	Hostname       string `json:"hostname"`
	Action         string `json:"action"`
	Verified       bool   `json:"verified"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	TXTRecord      string `json:"txt_record"`
	ChallengeToken string `json:"challenge_token,omitempty"`
}

// ApplyPlatformTenantRequest adds missing consumers and links existing surfaces
// without removing resources omitted from the bundle. Certificate issuance
// remains asynchronous and consumer keys are separate operations.
type ApplyPlatformTenantRequest struct {
	ExternalRef string                               `json:"external_ref"`
	Name        string                               `json:"name"`
	DryRun      bool                                 `json:"dry_run,omitempty"`
	Consumers   []ApplyPlatformTenantConsumerRequest `json:"consumers,omitempty"`
	SurfaceIDs  []string                             `json:"surface_ids,omitempty"`
	Surfaces    []ApplyPlatformTenantSurfaceRequest  `json:"surfaces,omitempty"`
}

type ApplyPlatformTenantSurfaceRequest struct {
	AppID     string   `json:"app_id"`
	Name      string   `json:"name"`
	CertKind  string   `json:"cert_kind,omitempty"`
	Hostnames []string `json:"hostnames"`
}

// PlanPlatformTenantReconciliationRequest describes the complete desired
// resource bundle for an existing platform tenant. Planning is always
// read-only; resources omitted from this request are reported, never changed.
type PlanPlatformTenantReconciliationRequest struct {
	Consumers  []ApplyPlatformTenantConsumerRequest `json:"consumers,omitempty"`
	SurfaceIDs []string                             `json:"surface_ids,omitempty"`
	Surfaces   []ApplyPlatformTenantSurfaceRequest  `json:"surfaces,omitempty"`
}

// ApplyPlatformTenantReconciliationRequest applies a previously reviewed plan
// only when ExpectedPlanHash still identifies the current tenant state.
type ApplyPlatformTenantReconciliationRequest struct {
	Consumers        []ApplyPlatformTenantConsumerRequest `json:"consumers,omitempty"`
	SurfaceIDs       []string                             `json:"surface_ids,omitempty"`
	Surfaces         []ApplyPlatformTenantSurfaceRequest  `json:"surfaces,omitempty"`
	ExpectedPlanHash string                               `json:"expected_plan_hash"`
}

// PlatformTenantReconciliationPlanChange is one proposed or retained resource
// in a read-only platform-tenant reconciliation plan. ManagedByPlatformTenant
// describes existing resources; it is nil for resources that would be created.
type PlatformTenantReconciliationPlanChange struct {
	ResourceType            string `json:"resource_type"`
	Action                  string `json:"action"`
	ID                      string `json:"id,omitempty"`
	AppID                   string `json:"app_id,omitempty"`
	ExternalRef             string `json:"external_ref,omitempty"`
	Name                    string `json:"name,omitempty"`
	SurfaceID               string `json:"surface_id,omitempty"`
	Hostname                string `json:"hostname,omitempty"`
	ManagedByPlatformTenant *bool  `json:"managed_by_platform_tenant,omitempty"`
}

// PlatformTenantReconciliationPlanResponse reports the complete dry-run
// result. remove_candidate entries are advisory only and are never applied.
type PlatformTenantReconciliationPlanResponse struct {
	TenantID string                                   `json:"tenant_id"`
	PlanHash string                                   `json:"plan_hash"`
	Changes  []PlatformTenantReconciliationPlanChange `json:"changes"`
}

// PlatformTenantReconciliationApplyResponse reports the exact changes applied
// from a confirmed plan. Consumers and surfaces are detached, not deleted;
// omitted managed hostnames are removed from their surface.
type PlatformTenantReconciliationApplyResponse struct {
	TenantID  string                                   `json:"tenant_id"`
	ReceiptID string                                   `json:"receipt_id"`
	PlanHash  string                                   `json:"plan_hash"`
	AppliedAt time.Time                                `json:"applied_at"`
	Applied   bool                                     `json:"applied"`
	Changes   []PlatformTenantReconciliationPlanChange `json:"changes"`
}

// PlatformTenantReconciliationReceiptSummary is a compact immutable record in
// a tenant's reconciliation history. Fetch the receipt by ID for its changes.
type PlatformTenantReconciliationReceiptSummary struct {
	ReceiptID   string    `json:"receipt_id"`
	PlanHash    string    `json:"plan_hash"`
	AppliedAt   time.Time `json:"applied_at"`
	ChangeCount int       `json:"change_count"`
}

type PlatformTenantReconciliationReceiptListResponse struct {
	Receipts      []PlatformTenantReconciliationReceiptSummary `json:"receipts"`
	NextPageToken string                                       `json:"next_page_token,omitempty"`
}

type ListPlatformTenantReconciliationReceiptsOptions struct {
	PageSize  int
	PageToken string
}

// PlatformTenantReconciliationReceiptResponse is the durable, secret-free
// result of one successful confirmed apply.
type PlatformTenantReconciliationReceiptResponse struct {
	TenantID  string                                   `json:"tenant_id"`
	ReceiptID string                                   `json:"receipt_id"`
	PlanHash  string                                   `json:"plan_hash"`
	AppliedAt time.Time                                `json:"applied_at"`
	Changes   []PlatformTenantReconciliationPlanChange `json:"changes"`
}

type ApplyPlatformTenantConsumerRequest struct {
	AppID       string `json:"app_id"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
}

type ApplyPlatformTenantConsumerResponse struct {
	ID                      string `json:"id,omitempty"`
	AppID                   string `json:"app_id"`
	ExternalRef             string `json:"external_ref"`
	Name                    string `json:"name"`
	Status                  string `json:"status"`
	Action                  string `json:"action"`
	ManagedByPlatformTenant bool   `json:"managed_by_platform_tenant,omitempty"`
}

type ApplyPlatformTenantSurfaceResponse struct {
	ID                      string                                `json:"id,omitempty"`
	AppID                   string                                `json:"app_id"`
	Name                    string                                `json:"name"`
	Status                  string                                `json:"status"`
	CertState               string                                `json:"cert_state"`
	Action                  string                                `json:"action"`
	ManagedByPlatformTenant bool                                  `json:"managed_by_platform_tenant,omitempty"`
	Hostnames               []ApplyPlatformTenantHostnameResponse `json:"hostnames,omitempty"`
}

type ApplyPlatformTenantHostnameResponse struct {
	TenantHostnameResponse
	Action string `json:"action"`
}

type PlatformTenantActivationSurfaceResponse struct {
	ID            string                   `json:"id"`
	AppID         string                   `json:"app_id"`
	Name          string                   `json:"name"`
	Status        string                   `json:"status"`
	CertState     string                   `json:"cert_state"`
	CertNotAfter  string                   `json:"cert_not_after,omitempty"`
	CertLastError string                   `json:"cert_last_error,omitempty"`
	Ready         bool                     `json:"ready"`
	Hostnames     []TenantHostnameResponse `json:"hostnames"`
}

type PlatformTenantActivationResponse struct {
	TenantID string                                    `json:"tenant_id"`
	Status   string                                    `json:"status"`
	Enabled  bool                                      `json:"enabled"`
	Ready    bool                                      `json:"ready"`
	Surfaces []PlatformTenantActivationSurfaceResponse `json:"surfaces"`
}

// PlatformTenantSelfActivationResponse is the downstream tenant's redacted
// activation snapshot. It intentionally omits app and deployment IDs, DNS
// challenge tokens, source metadata, logs, and raw DNS/certificate/deployment
// errors from the account-owner response.
type PlatformTenantSelfActivationResponse struct {
	Status   string                                        `json:"status"`
	Enabled  bool                                          `json:"enabled"`
	Ready    bool                                          `json:"ready"`
	Surfaces []PlatformTenantSelfActivationSurfaceResponse `json:"surfaces"`
}

type PlatformTenantSelfActivationSurfaceResponse struct {
	ID               string                                         `json:"id"`
	Name             string                                         `json:"name"`
	Status           string                                         `json:"status"`
	CertState        string                                         `json:"cert_state"`
	CertNotAfter     string                                         `json:"cert_not_after,omitempty"`
	Ready            bool                                           `json:"ready"`
	LatestDeployment *PlatformTenantSelfDeploymentResponse          `json:"latest_deployment,omitempty"`
	Hostnames        []PlatformTenantSelfActivationHostnameResponse `json:"hostnames"`
}

// PlatformTenantSelfDeploymentResponse is a deliberately small projection of
// the latest deployment attempt for a linked surface. It does not claim that
// the attempt is the currently serving deployment and omits deployment IDs,
// app IDs, source metadata, logs, and errors.
type PlatformTenantSelfDeploymentResponse struct {
	Status    string `json:"status"`
	Revision  int    `json:"revision,omitempty"`
	StartedAt string `json:"started_at"`
}

type PlatformTenantSelfActivationHostnameResponse struct {
	Hostname   string `json:"hostname"`
	Verified   bool   `json:"verified"`
	VerifiedAt string `json:"verified_at,omitempty"`
}

type ApplyPlatformTenantResponse struct {
	TenantID    string                                `json:"tenant_id,omitempty"`
	ExternalRef string                                `json:"external_ref"`
	Name        string                                `json:"name"`
	Status      string                                `json:"status"`
	Action      string                                `json:"action"`
	DryRun      bool                                  `json:"dry_run"`
	Consumers   []ApplyPlatformTenantConsumerResponse `json:"consumers"`
	Surfaces    []ApplyPlatformTenantSurfaceResponse  `json:"surfaces"`
}

type SetPlatformTenantStatusRequest struct {
	Status string `json:"status"`
}

type SetPlatformTenantRequestBudgetRequest struct {
	MaxRequestsPerMinute *int64 `json:"max_requests_per_minute"`
	MaxRequestsPerDay    *int64 `json:"max_requests_per_day"`
}

type PlatformTenantRequestBudgetResponse struct {
	TenantID             string     `json:"tenant_id"`
	Configured           bool       `json:"configured"`
	MaxRequestsPerMinute int64      `json:"max_requests_per_minute"`
	MaxRequestsPerDay    int64      `json:"max_requests_per_day"`
	MinuteUsed           int64      `json:"minute_used"`
	DayUsed              int64      `json:"day_used"`
	MinuteResetsAt       time.Time  `json:"minute_resets_at"`
	DayResetsAt          time.Time  `json:"day_resets_at"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty"`
}

// CreatePlatformTenantRateCardRequest appends an immutable tenant-wide
// customer price. If omitted, effective_from defaults to the next UTC minute.
type CreatePlatformTenantRateCardRequest struct {
	Currency               string     `json:"currency"`
	PriceMillicentsPerUnit int64      `json:"price_millicents_per_unit"`
	EffectiveFrom          *time.Time `json:"effective_from,omitempty"`
}

// PlatformTenantRateCardResponse is one immutable tenant-wide request price.
type PlatformTenantRateCardResponse struct {
	ID                     string    `json:"id"`
	TenantID               string    `json:"tenant_id"`
	Currency               string    `json:"currency"`
	Unit                   string    `json:"unit"`
	PriceMillicentsPerUnit int64     `json:"price_millicents_per_unit"`
	EffectiveFrom          time.Time `json:"effective_from"`
	CreatedAt              time.Time `json:"created_at"`
}

// PlatformTenantRateCardListResponse wraps a tenant's chronological tariff history.
type PlatformTenantRateCardListResponse struct {
	RateCards []PlatformTenantRateCardResponse `json:"rate_cards"`
}

type LinkPlatformTenantConsumerRequest struct {
	ConsumerID string `json:"consumer_id"`
}

type LinkPlatformTenantSurfaceRequest struct {
	SurfaceID string `json:"surface_id"`
}

type PlatformTenantResponse struct {
	ID          string    `json:"id"`
	ExternalRef string    `json:"external_ref"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PlatformTenantListResponse struct {
	Tenants       []PlatformTenantResponse `json:"tenants"`
	NextOffset    *int                     `json:"next_offset,omitempty"`
	NextPageToken string                   `json:"next_page_token,omitempty"`
}

type PlatformTenantSurfaceResponse struct {
	ID                      string `json:"id"`
	AppID                   string `json:"app_id"`
	Name                    string `json:"name"`
	Status                  string `json:"status"`
	ManagedByPlatformTenant bool   `json:"managed_by_platform_tenant,omitempty"`
}

type PlatformTenantDetailResponse struct {
	PlatformTenantResponse
	Consumers []APIConsumerResponse           `json:"consumers"`
	Surfaces  []PlatformTenantSurfaceResponse `json:"surfaces"`
}

// CreatePlatformTenantAccessTokenRequest asks the platform owner to mint a
// tenant-bound credential with selected self-service scopes. Omitted expiration defaults
// to 90 days; callers may choose any future time up to 365 days away.
type CreatePlatformTenantAccessTokenRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// PlatformTenantAccessTokenResponse is a redacted token record. It never
// contains the plaintext bearer.
type PlatformTenantAccessTokenResponse struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreatePlatformTenantAccessTokenResponse is returned exactly once. The
// plaintext bearer is intentionally absent from list and revoke responses.
type CreatePlatformTenantAccessTokenResponse struct {
	PlatformTenantAccessTokenResponse
	Token string `json:"token"`
}

type PlatformTenantAccessTokenListResponse struct {
	Tokens []PlatformTenantAccessTokenResponse `json:"tokens"`
}

type PlatformTenantUsageBucketResponse struct {
	AppID                  string    `json:"app_id"`
	ConsumerID             string    `json:"consumer_id,omitempty"`
	SurfaceID              string    `json:"surface_id,omitempty"`
	JWTAuthorizationRuleID string    `json:"jwt_authorization_rule_id,omitempty"`
	WindowStart            time.Time `json:"window_start"`
	RequestCount           int64     `json:"request_count"`
	ErrorCount             int64     `json:"error_count"`
	BillableUnits          int64     `json:"billable_units"`
}

type PlatformTenantUsageResponse struct {
	TenantID      string                              `json:"tenant_id"`
	PeriodStart   time.Time                           `json:"period_start"`
	PeriodEnd     time.Time                           `json:"period_end"`
	RequestCount  int64                               `json:"request_count"`
	ErrorCount    int64                               `json:"error_count"`
	BillableUnits int64                               `json:"billable_units"`
	Buckets       []PlatformTenantUsageBucketResponse `json:"buckets"`
	AsOf          time.Time                           `json:"as_of"`
}

// PlatformTenantActivityItem couples a sampled request-debugger row with the
// app that served it. The request payload deliberately excludes request and
// response bodies, headers, and credentials.
type PlatformTenantActivityItem struct {
	AppID   string                    `json:"app_id"`
	Request DebugTelemetryRequestItem `json:"request"`
}

// PlatformTenantActivityResponse is a bounded page over retained debugger
// evidence, not a complete request ledger. RequestCount and ErrorCount are
// weighted by each collapsed telemetry row's Count value.
type PlatformTenantActivityResponse struct {
	TenantID                string                        `json:"tenant_id"`
	Since                   string                        `json:"since"`
	WindowStart             time.Time                     `json:"window_start"`
	WindowEnd               time.Time                     `json:"window_end"`
	PlanRetentionDays       int                           `json:"plan_retention_days"`
	RetentionClamped        bool                          `json:"retention_clamped"`
	PageTelemetryRows       int64                         `json:"page_telemetry_rows"`
	PageRepresentedRequests int64                         `json:"page_represented_requests"`
	PageErrorRequests       int64                         `json:"page_error_requests"`
	PageComplete            bool                          `json:"page_complete"`
	NextCursor              string                        `json:"next_cursor,omitempty"`
	Filters                 PlatformTenantActivityFilters `json:"filters"`
	Requests                []PlatformTenantActivityItem  `json:"requests"`
}

type PlatformTenantActivityFilters struct {
	AppID  string `json:"app_id,omitempty"`
	Status int    `json:"status,omitempty"`
}

// PlatformTenantActivityOptions filters and paginates the tenant activity
// evidence endpoint. Cursor values are opaque and should be reused verbatim.
type PlatformTenantActivityOptions struct {
	Since  string
	AppID  string
	Status int
	Cursor string
	Limit  int
}

// Tenant statement revisions are additive. Revision 1 is the initial period
// snapshot; later revisions contain only usage delivered after prior ones.
type PlatformTenantStatementLineResponse struct {
	AppID                    string    `json:"app_id"`
	ConsumerID               string    `json:"consumer_id,omitempty"`
	SurfaceID                string    `json:"surface_id,omitempty"`
	JWTAuthorizationRuleID   string    `json:"jwt_authorization_rule_id,omitempty"`
	WindowStart              time.Time `json:"window_start"`
	BillableUnits            int64     `json:"billable_units"`
	RateCardID               string    `json:"rate_card_id,omitempty"`
	PlatformTenantRateCardID string    `json:"platform_tenant_rate_card_id,omitempty"`
	Currency                 string    `json:"currency,omitempty"`
	PriceMillicentsPerUnit   int64     `json:"price_millicents_per_unit,omitempty"`
	AmountMillicents         int64     `json:"amount_millicents"`
}

type PlatformTenantStatementResponse struct {
	ID               string                                `json:"id"`
	TenantID         string                                `json:"tenant_id"`
	PeriodStart      time.Time                             `json:"period_start"`
	PeriodEnd        time.Time                             `json:"period_end"`
	Revision         int                                   `json:"revision"`
	Status           string                                `json:"status"`
	Currency         string                                `json:"currency,omitempty"`
	BillableUnits    int64                                 `json:"billable_units"`
	UnpricedUnits    int64                                 `json:"unpriced_units"`
	AmountMillicents int64                                 `json:"amount_millicents"`
	Lines            []PlatformTenantStatementLineResponse `json:"lines"`
	AsOf             time.Time                             `json:"as_of"`
	CreatedAt        time.Time                             `json:"created_at"`
	FinalizedAt      *time.Time                            `json:"finalized_at,omitempty"`
}

type PlatformTenantStatementListResponse struct {
	Statements []PlatformTenantStatementResponse `json:"statements"`
	NextOffset *int                              `json:"next_offset,omitempty"`
}

// PlatformTenantStatementSummaryResponse keeps cross-period listing bounded;
// the downstream caller fetches line items for one statement by ID.
type PlatformTenantStatementSummaryResponse struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	PeriodStart      time.Time  `json:"period_start"`
	PeriodEnd        time.Time  `json:"period_end"`
	Revision         int        `json:"revision"`
	Status           string     `json:"status"`
	Currency         string     `json:"currency,omitempty"`
	BillableUnits    int64      `json:"billable_units"`
	UnpricedUnits    int64      `json:"unpriced_units"`
	AmountMillicents int64      `json:"amount_millicents"`
	AsOf             time.Time  `json:"as_of"`
	CreatedAt        time.Time  `json:"created_at"`
	FinalizedAt      *time.Time `json:"finalized_at,omitempty"`
}

type PlatformTenantSelfStatementListResponse struct {
	Statements []PlatformTenantStatementSummaryResponse `json:"statements"`
	NextOffset *int                                     `json:"next_offset,omitempty"`
}

type PlatformTenantStatementHandoffResponse struct {
	ID                string    `json:"id"`
	StatementID       string    `json:"statement_id"`
	ExternalInvoiceID string    `json:"external_invoice_id"`
	Currency          string    `json:"currency"`
	AmountMillicents  int64     `json:"amount_millicents"`
	CreatedAt         time.Time `json:"created_at"`
}
