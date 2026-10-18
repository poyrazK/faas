package api

// OrgQueueUsageResponse reports company queues, including retained namespaces.
// ReservedAsync includes all async reservations sharing the company's cap.
type OrgQueueUsageResponse struct {
	OrganizationID      string `json:"organization_id"`
	Plan                Plan   `json:"plan"`
	Bindings            int64  `json:"bindings"`
	Pending             int64  `json:"pending"`
	Processing          int64  `json:"processing"`
	DeadLetter          int64  `json:"dead_letter"`
	ReservedAsync       int64  `json:"reserved_async"`
	MaxAsyncInvocations int    `json:"max_async_invocations"`
	MaxTriggers         int    `json:"max_triggers"`
}
