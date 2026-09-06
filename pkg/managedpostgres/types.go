// Package managedpostgres defines Gregale's provider-neutral managed
// PostgreSQL control-plane contracts. Provider adapters translate these
// contracts to Neon, Xata, a hosted operator, or a future in-house service.
package managedpostgres

import (
	"context"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"
)

var (
	ErrUnavailable   = errors.New("managed postgres unavailable")
	ErrNotFound      = errors.New("managed postgres resource not found")
	ErrConflict      = errors.New("managed postgres resource conflict")
	ErrInvalid       = errors.New("invalid managed postgres request")
	ErrUnsupported   = errors.New("managed postgres feature unsupported")
	ErrQuotaExceeded = errors.New("managed postgres quota exceeded")
	ErrUsageStale    = errors.New("managed postgres usage is stale")
)

type State string

const (
	StateProvisioning State = "provisioning"
	StateReady        State = "ready"
	StateUpdating     State = "updating"
	StateDeleting     State = "deleting"
	StateFailed       State = "failed"
	StateDeleted      State = "deleted"
)

type ServiceClass string

// Service classes are Gregale product promises, not upstream SKU names. Each
// adapter owns the mapping from a class to its provider's current settings.
const (
	ClassDevelopment ServiceClass = "development"
	ClassBurstable   ServiceClass = "burstable"
	ClassProduction  ServiceClass = "production"
)

type Availability string

const (
	AvailabilitySingleZone      Availability = "single_zone"
	AvailabilityHighlyAvailable Availability = "high_availability"
)

type Spec struct {
	Region               string
	PostgresMajor        int
	Class                ServiceClass
	Availability         Availability
	ScaleToZero          bool
	StorageLimitBytes    int64
	RestoreWindowSeconds int64
}

func (s Spec) Validate() error {
	validRegion := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	if !validRegion.MatchString(s.Region) || s.PostgresMajor < 12 || s.PostgresMajor > 99 {
		return ErrInvalid
	}
	if s.Class != ClassDevelopment && s.Class != ClassBurstable && s.Class != ClassProduction {
		return ErrInvalid
	}
	if s.Availability != AvailabilitySingleZone && s.Availability != AvailabilityHighlyAvailable {
		return ErrInvalid
	}
	if s.StorageLimitBytes < 0 || s.RestoreWindowSeconds < 0 {
		return ErrInvalid
	}
	return nil
}

type ProviderStatus string

const (
	ProviderStatusPending  ProviderStatus = "pending"
	ProviderStatusReady    ProviderStatus = "ready"
	ProviderStatusDeleting ProviderStatus = "deleting"
	ProviderStatusFailed   ProviderStatus = "failed"
)

type ProvisionRequest struct {
	ResourceID     string
	Spec           Spec
	IdempotencyKey string
}

type UpdateRequest struct {
	ResourceID     string
	Spec           Spec
	Generation     int64
	IdempotencyKey string
}

type ObservedDatabase struct {
	ProviderResourceID string
	Status             ProviderStatus
	Spec               Spec
}

type DeleteRequest struct {
	// ResourceID is Gregale's stable logical identity. Providers use it to
	// discover an upstream resource when creation may have succeeded before
	// its opaque provider ID could be persisted.
	ResourceID         string
	ProviderResourceID string
	IdempotencyKey     string
}

type DeleteResult struct {
	Done bool
}

type CredentialAccess string

const (
	CredentialReadWrite CredentialAccess = "read_write"
	CredentialReadOnly  CredentialAccess = "read_only"
)

type EndpointRole string

const (
	EndpointPooled   EndpointRole = "pooled"
	EndpointDirect   EndpointRole = "direct"
	EndpointReadOnly EndpointRole = "read_only"
)

type Endpoint struct {
	Role EndpointRole
	Host string
	Port uint16
}

// CredentialMaterial is deliberately returned only by IssueCredentials. It
// must be handed directly to a CredentialSink and never written to the
// managed_postgres_databases or managed_postgres_bindings catalog rows.
type CredentialMaterial struct {
	// ProviderIdentityID is the provider's opaque, non-secret identity for
	// this credential. It is persisted for lifecycle audit; passwords and
	// connection URLs never are.
	ProviderIdentityID string
	Username           string
	Password           string
	Database           string
	TLSMode            string
	RootCertificatePEM string
	Endpoints          []Endpoint
}

func (CredentialMaterial) String() string { return "[REDACTED]" }

func (CredentialMaterial) GoString() string { return "managedpostgres.CredentialMaterial{[REDACTED]}" }

func (c CredentialMaterial) Validate() error {
	if !validOpaqueID(c.ProviderIdentityID) || c.Username == "" || c.Password == "" || c.Database == "" || len(c.Endpoints) == 0 {
		return ErrInvalid
	}
	if c.TLSMode != "require" && c.TLSMode != "verify-ca" && c.TLSMode != "verify-full" {
		return ErrInvalid
	}
	seen := make(map[EndpointRole]struct{}, len(c.Endpoints))
	for _, endpoint := range c.Endpoints {
		if endpoint.Host == "" || endpoint.Port == 0 {
			return ErrInvalid
		}
		if endpoint.Role != EndpointPooled && endpoint.Role != EndpointDirect && endpoint.Role != EndpointReadOnly {
			return ErrInvalid
		}
		if _, ok := seen[endpoint.Role]; ok {
			return ErrInvalid
		}
		seen[endpoint.Role] = struct{}{}
	}
	return nil
}

type CredentialRequest struct {
	ProviderResourceID string
	// IdentityKey is a stable, non-secret identity for one credential
	// generation. Rotation uses a new key; revocation repeats the key that
	// created the credential.
	IdentityKey    string
	Access         CredentialAccess
	IdempotencyKey string
}

type Meter string

// Meter names include their unit so usage can be normalized without assuming
// whether an upstream bills by compute time, active time, or operations.
const (
	MeterActiveSeconds      Meter = "active_seconds"
	MeterComputeUnitSeconds Meter = "compute_unit_seconds"
	MeterStorageByteSeconds Meter = "storage_byte_seconds"
	MeterHistoryByteSeconds Meter = "history_byte_seconds"
	MeterEgressBytes        Meter = "egress_bytes"
	MeterOperations         Meter = "operations"
)

type UsageWindow struct {
	From time.Time
	To   time.Time
}

type MeterReading struct {
	Meter    Meter
	Quantity int64
}

type Usage struct {
	Window   UsageWindow
	Readings []MeterReading
}

func (u Usage) Validate() error {
	if u.Window.From.IsZero() || !u.Window.To.After(u.Window.From) {
		return ErrInvalid
	}
	seen := make(map[Meter]struct{}, len(u.Readings))
	for _, reading := range u.Readings {
		if !validMeter(reading.Meter) || reading.Quantity < 0 {
			return ErrInvalid
		}
		if _, ok := seen[reading.Meter]; ok {
			return ErrInvalid
		}
		seen[reading.Meter] = struct{}{}
	}
	return nil
}

// UsagePolicy is the operator-owned safety policy for the dark managed
// PostgreSQL preview. It deliberately uses canonical Gregale meters rather
// than provider SKU names. A zero policy is disabled; enabling it requires a
// finite collection window and stale-data bound.
type UsagePolicy struct {
	Enabled                      bool
	CollectionInterval           time.Duration
	Window                       time.Duration
	StaleAfter                   time.Duration
	MaxMonthlyCostMillicents     int64
	MaxMonthlyComputeUnitSeconds int64
	MaxMonthlyStorageByteSeconds int64
	MaxMonthlyHistoryByteSeconds int64
	MaxMonthlyEgressBytes        int64
	ComputeUnitHourMillicents    int64
	StorageGiBHourMillicents     int64
	HistoryGiBHourMillicents     int64
	EgressGiBMillicents          int64
}

func (p UsagePolicy) Validate() error {
	if !p.Enabled {
		return nil
	}
	if p.CollectionInterval < time.Minute || p.Window < time.Hour || p.Window > 24*time.Hour || p.StaleAfter < p.Window || p.StaleAfter > 7*24*time.Hour {
		return ErrInvalid
	}
	if p.MaxMonthlyCostMillicents <= 0 || p.MaxMonthlyComputeUnitSeconds <= 0 || p.MaxMonthlyStorageByteSeconds <= 0 || p.MaxMonthlyEgressBytes <= 0 {
		return ErrInvalid
	}
	if p.MaxMonthlyHistoryByteSeconds < 0 || p.ComputeUnitHourMillicents < 0 || p.StorageGiBHourMillicents < 0 || p.HistoryGiBHourMillicents < 0 || p.EgressGiBMillicents < 0 {
		return ErrInvalid
	}
	return nil
}

// UsageRecord is one provider observation for one complete window and meter.
// The database key plus window makes recording idempotent; a later provider
// correction replaces the quantity instead of double-counting it.
type UsageRecord struct {
	AccountID          string
	DatabaseID         string
	BackendID          string
	BackendFingerprint string
	WindowFrom         time.Time
	WindowTo           time.Time
	ObservedAt         time.Time
	Meter              Meter
	Quantity           int64
	CostMillicents     int64
}

func (r UsageRecord) Validate() error {
	if r.AccountID == "" || r.DatabaseID == "" || !ValidName(r.BackendID) || len(r.BackendFingerprint) != 64 ||
		r.WindowFrom.IsZero() || !r.WindowTo.After(r.WindowFrom) || r.ObservedAt.IsZero() ||
		!validMeter(r.Meter) || r.Quantity < 0 || r.CostMillicents < 0 {
		return ErrInvalid
	}
	return nil
}

type UsageSnapshot struct {
	PeriodStart        time.Time
	LastObservedAt     time.Time
	ReadyDatabases     int
	ComputeUnitSeconds int64
	StorageByteSeconds int64
	HistoryByteSeconds int64
	EgressBytes        int64
	CostMillicents     int64
}

func (s UsageSnapshot) Stale(policy UsagePolicy, now time.Time) bool {
	if !policy.Enabled || s.ReadyDatabases == 0 {
		return false
	}
	return s.LastObservedAt.IsZero() || now.Sub(s.LastObservedAt) > policy.StaleAfter
}

func (s UsageSnapshot) Exceeds(policy UsagePolicy) bool {
	if !policy.Enabled {
		return false
	}
	return s.CostMillicents >= policy.MaxMonthlyCostMillicents ||
		s.ComputeUnitSeconds >= policy.MaxMonthlyComputeUnitSeconds ||
		s.StorageByteSeconds >= policy.MaxMonthlyStorageByteSeconds ||
		(policy.MaxMonthlyHistoryByteSeconds > 0 && s.HistoryByteSeconds >= policy.MaxMonthlyHistoryByteSeconds) ||
		s.EgressBytes >= policy.MaxMonthlyEgressBytes
}

type Capabilities struct {
	PostgresMajors          []int
	ServiceClasses          []ServiceClass
	Availability            []Availability
	ScaleToZero             bool
	PooledConnections       bool
	PointInTimeRestore      bool
	MaxRestoreWindowSeconds int64
	MaxStorageBytes         int64
	UsageMeters             []Meter
}

func (c Capabilities) Validate() error {
	if len(c.PostgresMajors) == 0 || len(c.ServiceClasses) == 0 || len(c.Availability) == 0 {
		return ErrInvalid
	}
	if c.MaxRestoreWindowSeconds < 0 || c.MaxStorageBytes < 0 {
		return ErrInvalid
	}
	if hasDuplicates(c.PostgresMajors) || hasDuplicates(c.ServiceClasses) || hasDuplicates(c.Availability) || hasDuplicates(c.UsageMeters) {
		return ErrInvalid
	}
	for _, major := range c.PostgresMajors {
		if major < 12 || major > 99 {
			return ErrInvalid
		}
	}
	for _, class := range c.ServiceClasses {
		if class != ClassDevelopment && class != ClassBurstable && class != ClassProduction {
			return ErrInvalid
		}
	}
	for _, availability := range c.Availability {
		if availability != AvailabilitySingleZone && availability != AvailabilityHighlyAvailable {
			return ErrInvalid
		}
	}
	for _, meter := range c.UsageMeters {
		if !validMeter(meter) {
			return ErrInvalid
		}
	}
	return nil
}

func (c Capabilities) Supports(spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if !contains(c.PostgresMajors, spec.PostgresMajor) || !contains(c.ServiceClasses, spec.Class) || !contains(c.Availability, spec.Availability) {
		return ErrUnsupported
	}
	if spec.ScaleToZero && !c.ScaleToZero {
		return ErrUnsupported
	}
	if spec.RestoreWindowSeconds > 0 && (!c.PointInTimeRestore || (c.MaxRestoreWindowSeconds > 0 && spec.RestoreWindowSeconds > c.MaxRestoreWindowSeconds)) {
		return ErrUnsupported
	}
	if spec.StorageLimitBytes > 0 && c.MaxStorageBytes > 0 && spec.StorageLimitBytes > c.MaxStorageBytes {
		return ErrUnsupported
	}
	return nil
}

// Provider is the complete vendor boundary. Mutating methods must be
// idempotent for their supplied key, return opaque resource IDs, normalize
// errors to the package sentinels, and never include credentials in an error.
type Provider interface {
	Capabilities() Capabilities
	Provision(context.Context, ProvisionRequest) (ObservedDatabase, error)
	Inspect(context.Context, string) (ObservedDatabase, error)
	Update(context.Context, UpdateRequest) (ObservedDatabase, error)
	Delete(context.Context, DeleteRequest) (DeleteResult, error)
	IssueCredentials(context.Context, CredentialRequest) (CredentialMaterial, error)
	RevokeCredentials(context.Context, CredentialRequest) error
	Usage(context.Context, string, UsageWindow) (Usage, error)
}

type Database struct {
	ID                 string
	AccountID          string
	Name               string
	Spec               Spec
	BackendID          string
	BackendFingerprint string
	ProviderResourceID string
	State              State
	DesiredGeneration  int64
	ObservedGeneration int64
	LastErrorCode      string
	LeaseToken         string
	LeaseUntil         time.Time
	AttemptCount       int32
	RetryAt            time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
}

type BindingState string

const (
	BindingStateProvisioning BindingState = "provisioning"
	BindingStateReady        BindingState = "ready"
	BindingStateDeleting     BindingState = "deleting"
	BindingStateFailed       BindingState = "failed"
	BindingStateDeleted      BindingState = "deleted"
)

type Binding struct {
	ID                   string
	AccountID            string
	DatabaseID           string
	AppID                string
	Scope                string
	EnvironmentKey       string
	Access               CredentialAccess
	ProviderIdentityID   string
	CredentialRef        string
	CredentialGeneration int64
	State                BindingState
	LastErrorCode        string
	LeaseToken           string
	LeaseUntil           time.Time
	AttemptCount         int32
	RetryAt              time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            *time.Time
}

// CredentialSink is implemented by the app-secret subsystem. Put must seal
// material before returning an opaque reference and be idempotent for a
// binding ID + generation. It is intentionally separate from Store.
type CredentialSink interface {
	Put(context.Context, Binding, CredentialMaterial) (string, error)
	// Delete receives the whole binding so a sink can recover the
	// deterministic reference after a crash between Put and catalog commit.
	Delete(context.Context, Binding) error
}

type Store interface {
	Reserve(context.Context, Database, int) (Database, bool, error)
	FindByName(context.Context, string, string) (Database, error)
	Get(context.Context, string, string) (Database, error)
	List(context.Context, string) ([]Database, error)
	Due(context.Context, bool, int, time.Time) ([]Database, error)
	Claim(context.Context, string, string, string, State, time.Time, time.Time) (Database, error)
	RecordProviderResource(context.Context, string, string, string, time.Time) error
	FinishProvision(context.Context, string, string, time.Time) (Database, error)
	Release(context.Context, string, string, State, string, time.Time, time.Time) error
	FinishDelete(context.Context, string, string, time.Time) (Database, error)
}

// UsageStore is the durable metering boundary. It is separate from Store so
// lifecycle test doubles remain small while production PostgreSQL can provide
// an atomic, idempotent usage ledger and account snapshot.
type UsageStore interface {
	ListUsageDatabases(context.Context, int) ([]Database, error)
	RecordUsage(context.Context, []UsageRecord) error
	UsageSnapshot(context.Context, string, time.Time) (UsageSnapshot, error)
}

// BindingStore persists the saga that connects one ready database to one app
// secret target. The lease token fences stale reconcilers; credential material
// itself never crosses this interface.
type BindingStore interface {
	ReserveBinding(context.Context, Binding) (Binding, bool, error)
	GetBinding(context.Context, string, string) (Binding, error)
	ListBindings(context.Context, string, string) ([]Binding, error)
	DueBindings(context.Context, bool, int, time.Time) ([]Binding, error)
	ClaimBinding(context.Context, string, string, string, BindingState, time.Time, time.Time) (Binding, error)
	FinishBindingProvision(context.Context, string, string, string, string, time.Time) (Binding, error)
	ReleaseBinding(context.Context, string, string, BindingState, string, time.Time, time.Time) error
	FinishBindingDelete(context.Context, string, string, time.Time) (Binding, error)
}

func ValidName(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	validName := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	return validName.MatchString(name)
}

func validBindingScope(scope string) bool {
	if !utf8.ValidString(scope) {
		return false
	}
	validScope := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	return validScope.MatchString(scope)
}

func validEnvironmentKey(key string) bool {
	if !utf8.ValidString(key) {
		return false
	}
	validKey := regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,126}$`)
	return validKey.MatchString(key)
}

func validOpaqueID(value string) bool {
	if value == "" || len(value) > 255 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validErrorCode(code string) bool {
	if code == "" {
		return true
	}
	validCode := regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	return validCode.MatchString(code)
}

func normalizeProviderError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrConflict):
		return ErrConflict
	case errors.Is(err, ErrInvalid):
		return ErrInvalid
	case errors.Is(err, ErrUnsupported):
		return ErrUnsupported
	case errors.Is(err, ErrQuotaExceeded):
		return ErrQuotaExceeded
	default:
		return ErrUnavailable
	}
}

func providerErrorCode(err error) string {
	normalized := normalizeProviderError(err)
	switch {
	case errors.Is(normalized, ErrNotFound):
		return "not_found"
	case errors.Is(normalized, ErrConflict):
		return "conflict"
	case errors.Is(normalized, ErrInvalid):
		return "invalid"
	case errors.Is(normalized, ErrUnsupported):
		return "unsupported"
	case errors.Is(normalized, ErrQuotaExceeded):
		return "quota_exceeded"
	default:
		return "unavailable"
	}
}

func validMeter(meter Meter) bool {
	switch meter {
	case MeterActiveSeconds, MeterComputeUnitSeconds, MeterStorageByteSeconds, MeterHistoryByteSeconds, MeterEgressBytes, MeterOperations:
		return true
	default:
		return false
	}
}

func contains[T comparable](items []T, target T) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func hasDuplicates[T comparable](items []T) bool {
	seen := make(map[T]struct{}, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			return true
		}
		seen[item] = struct{}{}
	}
	return false
}
