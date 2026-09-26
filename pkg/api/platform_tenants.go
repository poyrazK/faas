package api

import "time"

type CreatePlatformTenantRequest struct {
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
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

type ApplyPlatformTenantConsumerRequest struct {
	AppID       string `json:"app_id"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
}

type ApplyPlatformTenantConsumerResponse struct {
	ID          string `json:"id,omitempty"`
	AppID       string `json:"app_id"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Action      string `json:"action"`
}

type ApplyPlatformTenantSurfaceResponse struct {
	ID        string                                `json:"id,omitempty"`
	AppID     string                                `json:"app_id"`
	Name      string                                `json:"name"`
	Status    string                                `json:"status"`
	CertState string                                `json:"cert_state"`
	Action    string                                `json:"action"`
	Hostnames []ApplyPlatformTenantHostnameResponse `json:"hostnames,omitempty"`
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
	Tenants    []PlatformTenantResponse `json:"tenants"`
	NextOffset *int                     `json:"next_offset,omitempty"`
}

type PlatformTenantSurfaceResponse struct {
	ID     string `json:"id"`
	AppID  string `json:"app_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type PlatformTenantDetailResponse struct {
	PlatformTenantResponse
	Consumers []APIConsumerResponse           `json:"consumers"`
	Surfaces  []PlatformTenantSurfaceResponse `json:"surfaces"`
}

// CreatePlatformTenantAccessTokenRequest asks the platform owner to mint a
// read-only credential for one downstream tenant. Omitted expiration defaults
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
