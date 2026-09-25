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

type PlatformTenantUsageBucketResponse struct {
	AppID         string    `json:"app_id"`
	ConsumerID    string    `json:"consumer_id,omitempty"`
	SurfaceID     string    `json:"surface_id,omitempty"`
	WindowStart   time.Time `json:"window_start"`
	RequestCount  int64     `json:"request_count"`
	ErrorCount    int64     `json:"error_count"`
	BillableUnits int64     `json:"billable_units"`
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

// Tenant statement revisions are additive. Revision 1 is the initial period
// snapshot; later revisions contain only usage delivered after prior ones.
type PlatformTenantStatementLineResponse struct {
	AppID                  string    `json:"app_id"`
	ConsumerID             string    `json:"consumer_id,omitempty"`
	SurfaceID              string    `json:"surface_id,omitempty"`
	WindowStart            time.Time `json:"window_start"`
	BillableUnits          int64     `json:"billable_units"`
	RateCardID             string    `json:"rate_card_id,omitempty"`
	Currency               string    `json:"currency,omitempty"`
	PriceMillicentsPerUnit int64     `json:"price_millicents_per_unit,omitempty"`
	AmountMillicents       int64     `json:"amount_millicents"`
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
}

type PlatformTenantStatementHandoffResponse struct {
	ID                string    `json:"id"`
	StatementID       string    `json:"statement_id"`
	ExternalInvoiceID string    `json:"external_invoice_id"`
	Currency          string    `json:"currency"`
	AmountMillicents  int64     `json:"amount_millicents"`
	CreatedAt         time.Time `json:"created_at"`
}
