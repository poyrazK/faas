package api

import "time"

type CreatePlatformTenantRequest struct {
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
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
	ConsumerID    string    `json:"consumer_id"`
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
