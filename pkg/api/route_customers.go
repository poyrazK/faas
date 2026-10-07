package api

// RouteCustomerObservation groups retained requests by the identities recorded
// at request time. A consumer may appear with different historical tenants.
type RouteCustomerObservation struct {
	ConsumerID       string `json:"consumer_id,omitempty"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
	Requests         int64  `json:"requests"`
	LastObservedAt   string `json:"last_observed_at"`
}

// RouteCustomerUsage is observed exposure, not proof that a client will break.
// Consumer and tenant counts overlap and must never be added together.
type RouteCustomerUsage struct {
	Route                      string                     `json:"route"`
	Method                     string                     `json:"method"`
	Requests                   int64                      `json:"requests"`
	IdentifiedRequests         int64                      `json:"identified_requests"`
	AnonymousRequests          int64                      `json:"anonymous_requests"`
	UnresolvedIdentityRequests int64                      `json:"unresolved_identity_requests"`
	ConsumerCount              int64                      `json:"consumer_count"`
	PlatformTenantCount        int64                      `json:"platform_tenant_count"`
	LastObservedAt             string                     `json:"last_observed_at"`
	Customers                  []RouteCustomerObservation `json:"customers"`
	CustomersTruncated         bool                       `json:"customers_truncated"`
	OtherCustomerRequests      int64                      `json:"other_customer_requests"`
}

// RouteCustomerUsageResponse is scoped to one app, immutable deployment, and
// half-open retained telemetry window. Coverage is always observed_only:
// sampling, dropped events, expiry, and disabled recording have no denominator.
type RouteCustomerUsageResponse struct {
	Slug            string               `json:"slug"`
	DeploymentID    string               `json:"deployment_id"`
	From            string               `json:"from"`
	Until           string               `json:"until"`
	AsOf            string               `json:"as_of"`
	WindowClamped   bool                 `json:"window_clamped"`
	Coverage        string               `json:"coverage"`
	Routes          []RouteCustomerUsage `json:"routes"`
	RoutesLimit     int                  `json:"routes_limit"`
	RoutesTruncated bool                 `json:"routes_truncated"`
	CustomersLimit  int                  `json:"customers_limit"`
}
