package api

import "time"

// ManagedPostgresPlanLimits is the customer-facing allowance for managed
// PostgreSQL. Provider adapters may impose a lower ceiling, but never raise
// these plan limits.
type ManagedPostgresPlanLimits struct {
	DatabasesMax         int
	StorageLimitBytes    int64
	RestoreWindowSeconds int64
	DevelopmentAllowed   bool
	BurstableAllowed     bool
	ProductionAllowed    bool
	// AlwaysOnAllowed is deliberately disabled for the current bundled plans.
	// Keeping this as an entitlement makes the future always-on tier explicit
	// instead of allowing a customer request to bypass the COGS guardrail.
	AlwaysOnAllowed bool
}

const managedPostgresGiB int64 = 1 << 30

var managedPostgresPlanLimits = map[Plan]ManagedPostgresPlanLimits{
	PlanFree:  {},
	PlanHobby: {DatabasesMax: 1, StorageLimitBytes: 10 * managedPostgresGiB, RestoreWindowSeconds: 7 * 24 * 60 * 60, DevelopmentAllowed: true},
	PlanPro:   {DatabasesMax: 3, StorageLimitBytes: 50 * managedPostgresGiB, RestoreWindowSeconds: 7 * 24 * 60 * 60, DevelopmentAllowed: true, BurstableAllowed: true},
	PlanScale: {DatabasesMax: 10, StorageLimitBytes: 100 * managedPostgresGiB, RestoreWindowSeconds: 7 * 24 * 60 * 60, DevelopmentAllowed: true, BurstableAllowed: true, ProductionAllowed: true},
}

// ManagedPostgresLimitsFor returns a copy of the closed-set entitlement for p.
func ManagedPostgresLimitsFor(p Plan) (ManagedPostgresPlanLimits, bool) {
	limits, ok := managedPostgresPlanLimits[p]
	return limits, ok
}

type ManagedPostgresDatabase struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Region                  string `json:"region"`
	PostgresMajor           int    `json:"postgres_major"`
	ServiceClass            string `json:"service_class"`
	Availability            string `json:"availability"`
	ScaleToZero             bool   `json:"scale_to_zero"`
	StorageLimitBytes       int64  `json:"storage_limit_bytes"`
	RestoreWindowSeconds    int64  `json:"restore_window_seconds"`
	RestoreSourceDatabaseID string `json:"restore_source_database_id,omitempty"`
	RestorePointInTime      string `json:"restore_point_in_time,omitempty"`
	State                   string `json:"state"`
	LastErrorCode           string `json:"last_error_code,omitempty"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
	DeletedAt               string `json:"deleted_at,omitempty"`
}

type ManagedPostgresDatabaseList struct {
	Items []ManagedPostgresDatabase `json:"items"`
}

// ManagedPostgresUsageResponse is the customer-safe monthly usage view. The
// meters are provider-neutral quantities used for guardrails; provider costs,
// backend IDs, and credentials are deliberately not part of this response.
// StorageByteSecondsLimit is the plan's aggregate storage entitlement across
// its database slots, expressed in the same canonical unit as usage.
type ManagedPostgresUsageResponse struct {
	PeriodStart                 time.Time  `json:"period_start"`
	ObservedAt                  *time.Time `json:"observed_at,omitempty"`
	PolicyEnabled               bool       `json:"policy_enabled"`
	Fresh                       bool       `json:"fresh"`
	GuardrailState              string     `json:"guardrail_state"`
	ReadyDatabases              int        `json:"ready_databases"`
	DatabaseLimit               int        `json:"database_limit"`
	StorageLimitBytes           int64      `json:"storage_limit_bytes"`
	ComputeUnitSeconds          int64      `json:"compute_unit_seconds"`
	StorageByteSeconds          int64      `json:"storage_byte_seconds"`
	StorageByteSecondsLimit     int64      `json:"storage_byte_seconds_limit"`
	StorageByteSecondsRemaining int64      `json:"storage_byte_seconds_remaining"`
	HistoryByteSeconds          int64      `json:"history_byte_seconds"`
	EgressBytes                 int64      `json:"egress_bytes"`
}

// ManagedPostgresUsageLineItem is an operator-only normalized ledger line.
// Costs are internal COGS/invoice inputs, never customer charges.
type ManagedPostgresUsageLineItem struct {
	Code           string `json:"code"`
	Meter          string `json:"meter"`
	Unit           string `json:"unit"`
	Quantity       int64  `json:"quantity"`
	CostMillicents int64  `json:"cost_millicents"`
}

// ManagedPostgresUsageOperatorResponse is restricted to the operator
// allowlist. It adds account identity, effective safety ceilings, and
// internal cost line items to the customer-safe usage projection.
type ManagedPostgresUsageOperatorResponse struct {
	AccountID                    string                         `json:"account_id"`
	PeriodStart                  time.Time                      `json:"period_start"`
	ObservedAt                   *time.Time                     `json:"observed_at,omitempty"`
	PolicyEnabled                bool                           `json:"policy_enabled"`
	Fresh                        bool                           `json:"fresh"`
	GuardrailState               string                         `json:"guardrail_state"`
	ReadyDatabases               int                            `json:"ready_databases"`
	DatabaseLimit                int                            `json:"database_limit"`
	StorageLimitBytes            int64                          `json:"storage_limit_bytes"`
	ComputeUnitSeconds           int64                          `json:"compute_unit_seconds"`
	StorageByteSeconds           int64                          `json:"storage_byte_seconds"`
	StorageByteSecondsLimit      int64                          `json:"storage_byte_seconds_limit"`
	StorageByteSecondsRemaining  int64                          `json:"storage_byte_seconds_remaining"`
	HistoryByteSeconds           int64                          `json:"history_byte_seconds"`
	EgressBytes                  int64                          `json:"egress_bytes"`
	CostMillicents               int64                          `json:"cost_millicents"`
	MaxMonthlyCostMillicents     int64                          `json:"max_monthly_cost_millicents"`
	MaxMonthlyComputeUnitSeconds int64                          `json:"max_monthly_compute_unit_seconds"`
	MaxMonthlyStorageByteSeconds int64                          `json:"max_monthly_storage_byte_seconds"`
	MaxMonthlyHistoryByteSeconds int64                          `json:"max_monthly_history_byte_seconds"`
	MaxMonthlyEgressBytes        int64                          `json:"max_monthly_egress_bytes"`
	LineItems                    []ManagedPostgresUsageLineItem `json:"line_items"`
}

type CreateManagedPostgresDatabaseRequest struct {
	Name                 string `json:"name"`
	Region               string `json:"region"`
	PostgresMajor        int    `json:"postgres_major,omitempty"`
	ServiceClass         string `json:"service_class,omitempty"`
	Availability         string `json:"availability,omitempty"`
	ScaleToZero          *bool  `json:"scale_to_zero,omitempty"`
	StorageLimitBytes    int64  `json:"storage_limit_bytes,omitempty"`
	RestoreWindowSeconds int64  `json:"restore_window_seconds,omitempty"`
}

type RestoreManagedPostgresDatabaseRequest struct {
	Name        string `json:"name"`
	PointInTime string `json:"point_in_time"`
}

type ManagedPostgresBinding struct {
	ID                   string `json:"id"`
	DatabaseID           string `json:"database_id"`
	AppID                string `json:"app_id"`
	Scope                string `json:"scope"`
	EnvironmentKey       string `json:"environment_key"`
	Access               string `json:"access"`
	CredentialGeneration int64  `json:"credential_generation"`
	State                string `json:"state"`
	LastErrorCode        string `json:"last_error_code,omitempty"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type ManagedPostgresBindingList struct {
	Items []ManagedPostgresBinding `json:"items"`
}

type CreateManagedPostgresBindingRequest struct {
	AppID          string `json:"app_id"`
	Scope          string `json:"scope"`
	EnvironmentKey string `json:"environment_key"`
	Access         string `json:"access"`
}
