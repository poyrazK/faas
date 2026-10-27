package api

import "time"

// ManagedPostgresPlanLimits is the customer-facing allowance for managed
// PostgreSQL. Provider adapters may impose a lower ceiling, but never raise
// these plan limits.
type ManagedPostgresPlanLimits struct {
	DatabasesMax int
	// StorageLimitBytes is the account's pooled storage (ADR-1430): every
	// database together, and so also the most one database may use. The pool
	// is held as the account's monthly storage byte-seconds ceiling.
	StorageLimitBytes int64
	// MonthlyComputeUnitSeconds is the account's included compute across all
	// databases per UTC month (ADR-1430). Until compute overage is billed it is
	// a ceiling; zero keeps the operator's.
	MonthlyComputeUnitSeconds int64
	RestoreWindowSeconds      int64
	DevelopmentAllowed        bool
	BurstableAllowed          bool
	ProductionAllowed         bool
	// AlwaysOnAllowed is deliberately disabled for the current bundled plans.
	// Keeping this as an entitlement makes the future always-on tier explicit
	// instead of allowing a customer request to bypass the COGS guardrail.
	AlwaysOnAllowed bool
}

const (
	managedPostgresGiB                 int64 = 1 << 30
	managedPostgresComputeUnitHour     int64 = 60 * 60
	managedPostgresRestoreWindowSecond int64 = 7 * 24 * 60 * 60
)

// managedPostgresPlanLimits follows the October 2026 COGS report (ADR-1430):
// databases, highest service class, pooled storage and monthly compute units.
var managedPostgresPlanLimits = map[Plan]ManagedPostgresPlanLimits{
	PlanFree: {DatabasesMax: 1, StorageLimitBytes: managedPostgresGiB / 2, MonthlyComputeUnitSeconds: 5 * managedPostgresComputeUnitHour,
		RestoreWindowSeconds: managedPostgresRestoreWindowSecond, DevelopmentAllowed: true},
	PlanHobby: {DatabasesMax: 1, StorageLimitBytes: managedPostgresGiB, MonthlyComputeUnitSeconds: 25 * managedPostgresComputeUnitHour,
		RestoreWindowSeconds: managedPostgresRestoreWindowSecond, DevelopmentAllowed: true},
	PlanPro: {DatabasesMax: 3, StorageLimitBytes: 5 * managedPostgresGiB, MonthlyComputeUnitSeconds: 75 * managedPostgresComputeUnitHour,
		RestoreWindowSeconds: managedPostgresRestoreWindowSecond, DevelopmentAllowed: true, BurstableAllowed: true},
	PlanScale: {DatabasesMax: 10, StorageLimitBytes: 20 * managedPostgresGiB, MonthlyComputeUnitSeconds: 200 * managedPostgresComputeUnitHour,
		RestoreWindowSeconds: managedPostgresRestoreWindowSecond, DevelopmentAllowed: true, BurstableAllowed: true, ProductionAllowed: true},
}

// ManagedPostgresLimitsFor returns a copy of the closed-set entitlement for p.
func ManagedPostgresLimitsFor(p Plan) (ManagedPostgresPlanLimits, bool) {
	limits, ok := managedPostgresPlanLimits[p]
	return limits, ok
}

type ManagedPostgresDatabase struct {
	Health                  *ManagedPostgresHealth `json:"health,omitempty"`
	ID                      string                 `json:"id"`
	Name                    string                 `json:"name"`
	Region                  string                 `json:"region"`
	PostgresMajor           int                    `json:"postgres_major"`
	ServiceClass            string                 `json:"service_class"`
	Availability            string                 `json:"availability"`
	ScaleToZero             bool                   `json:"scale_to_zero"`
	StorageLimitBytes       int64                  `json:"storage_limit_bytes"`
	RestoreWindowSeconds    int64                  `json:"restore_window_seconds"`
	RestoreSourceDatabaseID string                 `json:"restore_source_database_id,omitempty"`
	RestorePointInTime      string                 `json:"restore_point_in_time,omitempty"`
	State                   string                 `json:"state"`
	LastErrorCode           string                 `json:"last_error_code,omitempty"`
	CreatedAt               string                 `json:"created_at"`
	UpdatedAt               string                 `json:"updated_at"`
	DeletedAt               string                 `json:"deleted_at,omitempty"`
}

// ManagedPostgresHealth reports cached provider metadata. Healthy does not
// assert application-to-database SQL connectivity; use bindings verify for it.
type ManagedPostgresHealth struct {
	Enabled           bool   `json:"enabled"`
	Status            string `json:"status"`
	Fresh             bool   `json:"fresh"`
	ProviderStatus    string `json:"provider_status"`
	ComputeState      string `json:"compute_state"`
	StaleAfterSeconds int64  `json:"stale_after_seconds"`
	CheckedAt         string `json:"checked_at,omitempty"`
	LastSuccessAt     string `json:"last_success_at,omitempty"`
	LastErrorCode     string `json:"last_error_code,omitempty"`
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
	// Compute caps on running databases (ADR-20261025094915). The allowance is
	// the plan's monthly compute; the ceiling is where running databases are
	// suspended. ComputeCapState is healthy, warning (at 80% of the allowance),
	// overage (past the allowance while the account allows overage) or
	// suspended, and reports suspended only while caps are enforced.
	ComputeAllowanceUnitSeconds int64  `json:"compute_allowance_unit_seconds,omitempty"`
	ComputeCeilingUnitSeconds   int64  `json:"compute_ceiling_unit_seconds,omitempty"`
	ComputeCapState             string `json:"compute_cap_state,omitempty"`
	// OverageEnabled is true while the account runs its databases past the
	// allowance, up to the ceiling (ADR-20261025094915 decision 2).
	OverageEnabled bool `json:"overage_enabled,omitempty"`
}

// ManagedPostgresOverage is GET and PUT /v1/postgres/overage. With overage on,
// a paid account's databases keep running past the monthly compute allowance
// until the ceiling, twice the allowance, and are then suspended; with it
// off (the default) they are suspended at the allowance.
type ManagedPostgresOverage struct {
	Enabled                     bool  `json:"enabled"`
	ComputeAllowanceUnitSeconds int64 `json:"compute_allowance_unit_seconds"`
	ComputeCeilingUnitSeconds   int64 `json:"compute_ceiling_unit_seconds"`
}

// ManagedPostgresOverageRequest is the PUT /v1/postgres/overage body.
type ManagedPostgresOverageRequest struct {
	Enabled *bool `json:"enabled"`
}

const (
	ManagedPostgresComputeCapHealthy   = "healthy"
	ManagedPostgresComputeCapWarning   = "warning"
	ManagedPostgresComputeCapOverage   = "overage"
	ManagedPostgresComputeCapSuspended = "suspended"
)

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
	RotationPending      bool   `json:"rotation_pending"`
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

// ManagedPostgresCutover reports staging and control-plane SQL evidence only.
// Credentials remain unpublished and workloads continue using the source.
type ManagedPostgresCutover struct {
	ID                        string                         `json:"id"`
	SourceDatabaseID          string                         `json:"source_database_id"`
	TargetDatabaseID          string                         `json:"target_database_id"`
	AppID                     string                         `json:"app_id"`
	Scope                     string                         `json:"scope"`
	State                     string                         `json:"state"`
	VerificationFresh         bool                           `json:"verification_fresh"`
	VerificationMaxAgeSeconds int64                          `json:"verification_max_age_seconds"`
	VerifiedAt                string                         `json:"verified_at,omitempty"`
	LastErrorCode             string                         `json:"last_error_code,omitempty"`
	Members                   []ManagedPostgresCutoverMember `json:"members"`
	CreatedAt                 string                         `json:"created_at"`
	UpdatedAt                 string                         `json:"updated_at"`
}
type ManagedPostgresCutoverMember struct {
	SourceBindingID string `json:"source_binding_id"`
	EnvironmentKey  string `json:"environment_key"`
	Access          string `json:"access"`
	State           string `json:"state"`
	VerifiedAt      string `json:"verified_at,omitempty"`
}
type PrepareManagedPostgresCutoverRequest struct {
	SourceDatabaseID string `json:"source_database_id"`
	TargetDatabaseID string `json:"target_database_id"`
	AppID            string `json:"app_id"`
	Scope            string `json:"scope"`
}

// ManagedPostgresCutoverPreflight is a historical catalog observation. Stale
// is recomputed when read; ActivationAllowed remains false in this release.
type ManagedPostgresCutoverPreflight struct {
	CutoverID                string                                `json:"cutover_id"`
	State                    string                                `json:"state"`
	Revision                 int64                                 `json:"revision"`
	ActivationAllowed        bool                                  `json:"activation_allowed"`
	Stale                    bool                                  `json:"stale"`
	BindingIDs               []string                              `json:"binding_ids"`
	Blockers                 []string                              `json:"blockers"`
	VerificationFresh        bool                                  `json:"verification_fresh"`
	SourceMatches            bool                                  `json:"source_matches"`
	TargetMatches            bool                                  `json:"target_matches"`
	BindingsMatch            bool                                  `json:"bindings_match"`
	ObservedCutoverUpdatedAt string                                `json:"observed_cutover_updated_at,omitempty"`
	CheckedAt                string                                `json:"checked_at,omitempty"`
	ExpiresAt                string                                `json:"expires_at,omitempty"`
	CreatedAt                string                                `json:"created_at"`
	UpdatedAt                string                                `json:"updated_at"`
	RuntimeDrain             *ManagedPostgresCutoverRuntimeDrain   `json:"runtime_drain,omitempty"`
	SourceSessions           *ManagedPostgresCutoverSourceSessions `json:"source_sessions,omitempty"`
}

// ManagedPostgresCutoverSourceSessions describes a selected SQL database set,
// never complete writer isolation or permission to activate.
type ManagedPostgresCutoverSourceSessions struct {
	State                    string                                 `json:"state"`
	AdmissionClosed          bool                                   `json:"admission_closed"`
	SelectedDatabasesDrained bool                                   `json:"selected_databases_drained"`
	SelectedDatabaseCount    int                                    `json:"selected_database_count"`
	Sessions                 int64                                  `json:"sessions"`
	PreparedTransactions     int64                                  `json:"prepared_transactions"`
	UnselectedDatabases      int64                                  `json:"unselected_databases"`
	Blockers                 []string                               `json:"blockers"`
	ObservedAt               string                                 `json:"observed_at,omitempty"`
	WriterIsolation          *ManagedPostgresCutoverWriterIsolation `json:"writer_isolation,omitempty"`
}

// ManagedPostgresCutoverWriterIsolation reports safe historical diagnostics and
// provider evidence; it is never permission to activate a customer cutover.
type ManagedPostgresCutoverWriterIsolation struct {
	State                   string `json:"state"`
	Confirmed               bool   `json:"confirmed"`
	InventoryAvailable      bool   `json:"inventory_available"`
	CatalogMatches          bool   `json:"catalog_matches"`
	CatalogStable           bool   `json:"catalog_stable"`
	BackgroundWritersFenced bool   `json:"background_writers_fenced"`
	ClientSessions          int64  `json:"client_sessions"`
	BackgroundWorkers       int64  `json:"background_workers"`
	ReplicationWorkers      int64  `json:"replication_workers"`
	UnknownBackends         int64  `json:"unknown_backends"`
	ExpiresAt               string `json:"expires_at,omitempty"`
}

// ManagedPostgresCutoverRuntimeDrain covers the captured instance catalog only.
// Catalog retirement does not establish complete writer isolation.
type ManagedPostgresCutoverRuntimeDrain struct {
	State          string                                     `json:"state"`
	InventoryCount int                                        `json:"inventory_count"`
	Pending        int                                        `json:"pending"`
	Retired        int                                        `json:"retired"`
	Blocked        int                                        `json:"blocked"`
	Blockers       []string                                   `json:"blockers"`
	Members        []ManagedPostgresCutoverRuntimeDrainMember `json:"members"`
}

type ManagedPostgresCutoverRuntimeDrainMember struct {
	InstanceID   string `json:"instance_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Scope        string `json:"scope"`
	Mode         string `json:"mode"`
	State        string `json:"state"`
	BlockerCode  string `json:"blocker_code,omitempty"`
}
