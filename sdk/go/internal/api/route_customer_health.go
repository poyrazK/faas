package api

// RouteCustomerHealthAttribution counts observations for the selected identity
// dimension. Unattributed requests did not record that identity; unresolved
// requests recorded an identity that no longer resolves in the owning scope.
type RouteCustomerHealthAttribution struct {
	IdentifiedRequests         int64 `json:"identified_requests"`
	UnattributedRequests       int64 `json:"unattributed_requests"`
	UnresolvedIdentityRequests int64 `json:"unresolved_identity_requests"`
	OtherCustomerRequests      int64 `json:"other_customer_requests"`
}

type RouteCustomerHealthCohort struct {
	CustomerID string             `json:"customer_id,omitempty"`
	Health     RouteHealthFinding `json:"health"`
}

type RouteCustomerHealthRoute struct {
	Method             string                         `json:"method"`
	Path               string                         `json:"path"`
	ObservedCustomers  int64                          `json:"observed_customers"`
	CustomersTruncated bool                           `json:"customers_truncated"`
	Candidate          RouteCustomerHealthAttribution `json:"candidate"`
	Stable             RouteCustomerHealthAttribution `json:"stable"`
	Customers          []RouteCustomerHealthCohort    `json:"customers"`
}

// RouteCustomerHealthReport is advisory live evidence. It never participates in
// rollout decisions, saved decision history, recovery, audits, or webhooks.
type RouteCustomerHealthReport struct {
	GroupBy         string                     `json:"group_by"`
	DetailsIncluded bool                       `json:"details_included"`
	Coverage        string                     `json:"coverage"`
	Status          string                     `json:"status"`
	Reason          string                     `json:"reason"`
	CustomersLimit  int                        `json:"customers_limit"`
	Routes          []RouteCustomerHealthRoute `json:"routes"`
}
