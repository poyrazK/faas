// Package firecracker is the native managed-PostgreSQL backend (ADR-1027):
// one Firecracker VM per database on Gregale's own database nodes, provisioned
// through schedd's database scheduler and reached through pgproxyd. It
// implements managedpostgres.Provider. The driver never stores a password:
// credentials are SCRAM verifiers set inside the VM and published to the
// proxy.
package firecracker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

// Scheduler is the schedd surface the driver uses; scheddpb.ScheddClient
// satisfies it.
type Scheduler interface {
	ProvisionDatabase(ctx context.Context, in *scheddpb.ProvisionDatabaseRequest, opts ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	GetDatabase(ctx context.Context, in *scheddpb.DatabaseRef, opts ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	DatabaseAdmin(ctx context.Context, in *scheddpb.DatabaseAdminRequest, opts ...grpc.CallOption) (*scheddpb.DatabaseAdminResponse, error)
	DeleteDatabase(ctx context.Context, in *scheddpb.DatabaseRef, opts ...grpc.CallOption) (*scheddpb.DeleteDatabaseResponse, error)
	SetDatabaseAlwaysOn(ctx context.Context, in *scheddpb.SetDatabaseAlwaysOnRequest, opts ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	// ParkDatabase suspends a database; only the scale-to-zero prober uses it.
	ParkDatabase(ctx context.Context, in *scheddpb.DatabaseRef, opts ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	RestoreDatabase(ctx context.Context, in *scheddpb.RestoreDatabaseRequest, opts ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error)
	DatabaseUsage(ctx context.Context, in *scheddpb.DatabaseUsageRequest, opts ...grpc.CallOption) (*scheddpb.DatabaseUsageResponse, error)
}

// Verifiers publishes role verifiers to pgproxyd; *dbcreds.VerifierStore
// satisfies it.
type Verifiers interface {
	Put(ctx context.Context, databaseID, role string, v pgscram.Verifier) error
	Delete(ctx context.Context, databaseID, role string) error
	DeleteDatabase(ctx context.Context, databaseID string) error
}

// shape is one service class's VM size.
type shape struct {
	vcpus  int
	memMiB int
}

// Config is the driver's view of its BackendConfig.
type Config struct {
	Region          string
	HostSuffix      string
	Port            uint16
	TLSMode         string
	Database        string
	Majors          []int
	Classes         map[managedpostgres.ServiceClass]shape
	MaxStorageBytes int64
	// MaxRestoreWindowSeconds turns point-in-time restore on when positive
	// (set only where dbbackupd archives the database nodes' WAL) and bounds
	// a spec's restore window.
	MaxRestoreWindowSeconds int64
	// CredentialKey derives each credential's password and SCRAM salt
	// (at least 32 bytes, from the variable secret_env.credential_key names).
	// Issuing the same identity again yields the same login, which the
	// provider contract requires, while no password is ever stored.
	CredentialKey []byte
}

// MinCredentialKeyBytes is the shortest accepted credential key.
const MinCredentialKeyBytes = 32

// Settings keys in BackendConfig.Settings.
const (
	settingHostSuffix = "host_suffix"
	settingPort       = "port"
	settingTLSMode    = "tls_mode"
	settingDatabase   = "database_name"
	settingMajors     = "postgres_majors"
	settingMaxStorage = "max_storage_bytes"
	settingMaxRestore = "max_restore_window_seconds"
)

// ParseConfig reads the backend settings. Class shapes are fixed and
// distinct, so Inspect can derive a database's class from its VM size.
func ParseConfig(backend managedpostgres.BackendConfig) (Config, error) {
	s := backend.Settings
	cfg := Config{
		Region: backend.Region, HostSuffix: strings.TrimPrefix(strings.ToLower(s[settingHostSuffix]), "."),
		Port: 5432, TLSMode: "verify-full", Database: "gregale", Majors: []int{17}, MaxStorageBytes: 100 << 30,
		Classes: map[managedpostgres.ServiceClass]shape{
			managedpostgres.ClassDevelopment: {vcpus: 1, memMiB: 512},
			managedpostgres.ClassBurstable:   {vcpus: 2, memMiB: 1024},
		},
	}
	if cfg.HostSuffix == "" {
		return Config{}, errors.New("firecracker: settings.host_suffix is required")
	}
	if v := s[settingPort]; v != "" {
		port, err := strconv.ParseUint(v, 10, 16)
		if err != nil || port == 0 {
			return Config{}, fmt.Errorf("firecracker: invalid port %q", v)
		}
		cfg.Port = uint16(port)
	}
	if v := s[settingTLSMode]; v != "" {
		if v != "require" && v != "verify-ca" && v != "verify-full" {
			return Config{}, fmt.Errorf("firecracker: invalid tls_mode %q", v)
		}
		cfg.TLSMode = v
	}
	if v := s[settingDatabase]; v != "" {
		if !identifierPattern.MatchString(v) || v == "postgres" {
			return Config{}, fmt.Errorf("firecracker: invalid database_name %q", v)
		}
		cfg.Database = v
	}
	if v := s[settingMajors]; v != "" {
		cfg.Majors = nil
		for _, field := range strings.Split(v, ",") {
			major, err := strconv.Atoi(strings.TrimSpace(field))
			if err != nil || major < 14 || major > 18 {
				return Config{}, fmt.Errorf("firecracker: invalid postgres_majors %q", v)
			}
			cfg.Majors = append(cfg.Majors, major)
		}
	}
	if v := s[settingMaxStorage]; v != "" {
		max, err := strconv.ParseInt(v, 10, 64)
		if err != nil || max < 1<<30 {
			return Config{}, fmt.Errorf("firecracker: invalid max_storage_bytes %q", v)
		}
		cfg.MaxStorageBytes = max
	}
	if v := s[settingMaxRestore]; v != "" {
		window, err := strconv.ParseInt(v, 10, 64)
		if err != nil || window < 0 {
			return Config{}, fmt.Errorf("firecracker: invalid max_restore_window_seconds %q", v)
		}
		cfg.MaxRestoreWindowSeconds = window
	}
	return cfg, nil
}

// Connector opens a SQL session with issued credentials. Qualification
// probes use it; the default dials the material's endpoint (pgproxyd).
type Connector func(ctx context.Context, material managedpostgres.CredentialMaterial) (*pgx.Conn, error)

// Provider is the native backend.
type Provider struct {
	cfg       Config
	scheduler Scheduler
	verifiers Verifiers
	connect   Connector
	poll      time.Duration
}

// New builds a Provider over a schedd client and the verifier store.
func New(cfg Config, scheduler Scheduler, verifiers Verifiers) (*Provider, error) {
	if scheduler == nil || verifiers == nil {
		return nil, errors.New("firecracker: scheduler and verifier store are required")
	}
	if len(cfg.CredentialKey) < MinCredentialKeyBytes {
		return nil, fmt.Errorf("firecracker: the credential key needs at least %d bytes", MinCredentialKeyBytes)
	}
	return &Provider{cfg: cfg, scheduler: scheduler, verifiers: verifiers, connect: connectEndpoint, poll: 500 * time.Millisecond}, nil
}

// WithConnector replaces how probes reach the database (tests, or an
// operator host that reaches pgproxyd differently).
func (p *Provider) WithConnector(c Connector) *Provider {
	p.connect = c
	return p
}

var _ managedpostgres.Provider = (*Provider)(nil)

// Capabilities states what the backend does today. Point-in-time restore is
// on where the deployment archives WAL (max_restore_window_seconds). Usage
// comes from schedd's ledger, and a restore is its own database, metered
// apart from its source. A database switches between scale-to-zero and
// always-on in place.
func (p *Provider) Capabilities() managedpostgres.Capabilities {
	classes := make([]managedpostgres.ServiceClass, 0, len(p.cfg.Classes))
	for _, class := range []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction} {
		if _, ok := p.cfg.Classes[class]; ok {
			classes = append(classes, class)
		}
	}
	return managedpostgres.Capabilities{
		PostgresMajors:          append([]int(nil), p.cfg.Majors...),
		ServiceClasses:          classes,
		Availability:            []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		CredentialAccess:        []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration},
		ScaleToZero:             true,
		ScaleToZeroUpdate:       true,
		MaxStorageBytes:         p.cfg.MaxStorageBytes,
		PointInTimeRestore:      p.cfg.MaxRestoreWindowSeconds > 0,
		MaxRestoreWindowSeconds: p.cfg.MaxRestoreWindowSeconds,
		RestoreUsageIsolated:    p.cfg.MaxRestoreWindowSeconds > 0,
		UsageMeters:             append([]managedpostgres.Meter(nil), usageMeters...),
	}
}

// usageMeters are the meters schedd's ledger answers for (ADR-1027): VM run
// time, vCPU-seconds, provisioned volume byte-seconds, archive byte-seconds
// and bytes pgproxyd sent to clients.
var usageMeters = []managedpostgres.Meter{
	managedpostgres.MeterActiveSeconds, managedpostgres.MeterComputeUnitSeconds, managedpostgres.MeterStorageByteSeconds,
	managedpostgres.MeterHistoryByteSeconds, managedpostgres.MeterEgressBytes,
}

// Provision creates the database VM and its customer database. Schedd
// placement is idempotent per resource id, and so is the init batch.
func (p *Provider) Provision(ctx context.Context, req managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	if err := p.supports(req.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	id := resourceUUID(req.ResourceID)
	rt, err := p.scheduler.ProvisionDatabase(ctx, p.provisionRequest(id, req.Spec))
	if err != nil {
		return managedpostgres.ObservedDatabase{}, providerError(err)
	}
	if rt.GetState() == "failed" {
		return p.observed(rt), nil
	}
	init, err := initSQL(p.cfg.Database)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if err := p.admin(ctx, id, init); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	return p.Inspect(ctx, id)
}

func (p *Provider) provisionRequest(id string, spec managedpostgres.Spec) *scheddpb.ProvisionDatabaseRequest {
	shape := p.cfg.Classes[spec.Class]
	return &scheddpb.ProvisionDatabaseRequest{
		DatabaseId: id, PostgresMajor: int32(spec.PostgresMajor), VcpuCount: int32(shape.vcpus),
		MemMib: int32(shape.memMiB), VolumeBytes: spec.StorageLimitBytes, AlwaysOn: !spec.ScaleToZero,
		RestoreWindowSeconds: spec.RestoreWindowSeconds,
	}
}

// resourceNamespace scopes the database ids derived from resource ids.
var resourceNamespace = uuid.MustParse("6f1b7c2e-0d4a-5e8f-9b3c-1a2d3e4f5a6b")

// resourceUUID is the schedd database id of a resource id: the id itself
// when it is a UUID (Gregale's database ids are), otherwise a name-based
// UUID of it, so a retry with the same resource id names the same VM.
func resourceUUID(resourceID string) string {
	if id, err := uuid.Parse(resourceID); err == nil && len(resourceID) == 36 {
		return id.String()
	}
	return uuid.NewSHA1(resourceNamespace, []byte(resourceID)).String()
}

// supports admits only specs Inspect reports back unchanged, since the
// service fails a database whose observed spec differs. An unbounded volume
// has no VM equivalent.
func (p *Provider) supports(spec managedpostgres.Spec) error {
	if err := p.Capabilities().Supports(spec); err != nil {
		return err
	}
	if spec.Region != p.cfg.Region || spec.StorageLimitBytes <= 0 {
		return managedpostgres.ErrUnsupported
	}
	return nil
}

// Inspect reports the database and the spec it implements, derived from
// the VM schedd runs.
func (p *Provider) Inspect(ctx context.Context, providerResourceID string) (managedpostgres.ObservedDatabase, error) {
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: providerResourceID})
	if err != nil {
		return managedpostgres.ObservedDatabase{}, providerError(err)
	}
	return p.observed(rt), nil
}

func (p *Provider) observed(rt *scheddpb.DatabaseRuntime) managedpostgres.ObservedDatabase {
	// The dataset is the database's volume, which only this resource id
	// names; a restore creates a new id.
	obs := managedpostgres.ObservedDatabase{ProviderResourceID: rt.GetDatabaseId(), DataResourceID: rt.GetDatabaseId(), ComputeState: managedpostgres.ComputeStateUnknown}
	if rt.GetRestoreSourceId() != "" && rt.GetRestoreTarget() != nil {
		// Schedd's record of what the volume was recovered from, fixed when
		// the restore was placed.
		obs.RestoreLineage = &managedpostgres.RestoreLineage{SourceResourceID: rt.GetRestoreSourceId(), PointInTime: rt.GetRestoreTarget().AsTime()}
	}
	switch rt.GetState() {
	case "parked":
		obs.Status, obs.ComputeState = managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateSuspended
	case "running":
		obs.Status, obs.ComputeState = managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateActive
	case "waking", "parking":
		obs.Status, obs.ComputeState = managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateWaking
	case "deleting":
		obs.Status = managedpostgres.ProviderStatusDeleting
	case "failed":
		obs.Status = managedpostgres.ProviderStatusFailed
	default:
		obs.Status = managedpostgres.ProviderStatusPending
	}
	obs.Spec = p.specFor(rt)
	return obs
}

// specFor derives the portable spec from the stored VM shape.
func (p *Provider) specFor(rt *scheddpb.DatabaseRuntime) managedpostgres.Spec {
	spec := managedpostgres.Spec{
		Region: p.cfg.Region, PostgresMajor: int(rt.GetPostgresMajor()), Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: !rt.GetAlwaysOn(), StorageLimitBytes: rt.GetVolumeBytes(), RestoreWindowSeconds: rt.GetRestoreWindowSeconds(),
	}
	for class, s := range p.cfg.Classes {
		if int32(s.vcpus) == rt.GetVcpuCount() && int32(s.memMiB) == rt.GetMemMib() {
			spec.Class = class
		}
	}
	return spec
}

// Update switches a database between scale-to-zero and always-on. Other
// spec changes (class, storage, major) are not supported yet. Turning
// always-on on leaves a parked database to schedd's keep-warm sweep, so
// the observation may still be suspended for one sweep.
func (p *Provider) Update(ctx context.Context, req managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	if req.ResourceID == "" || (req.DataResourceID != "" && req.DataResourceID != req.ResourceID) {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	previous := req.PreviousSpec
	previous.ScaleToZero = req.Spec.ScaleToZero
	if previous != req.Spec {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
	}
	if err := p.supports(req.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	rt, err := p.scheduler.SetDatabaseAlwaysOn(ctx, &scheddpb.SetDatabaseAlwaysOnRequest{DatabaseId: resourceUUID(req.ResourceID), AlwaysOn: !req.Spec.ScaleToZero})
	if err != nil {
		return managedpostgres.ObservedDatabase{}, providerError(err)
	}
	return p.observed(rt), nil
}

// Restore creates the database as the source recovered to the point in
// time, from the source's archived WAL and base backups (ADR-1027
// durability). Schedd restores it in the background: the database reports
// pending until it has recovered, promoted and taken its own first base
// backup. Repeating the request returns the same database.
func (p *Provider) Restore(ctx context.Context, req managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	if req.ResourceID == "" || req.SourceResourceID == "" || req.ResourceID == req.SourceResourceID || req.PointInTime.IsZero() {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	if !p.Capabilities().PointInTimeRestore {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
	}
	if err := p.supports(req.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	rt, err := p.scheduler.RestoreDatabase(ctx, &scheddpb.RestoreDatabaseRequest{
		Database: p.provisionRequest(resourceUUID(req.ResourceID), req.Spec), SourceDatabaseId: req.SourceResourceID,
		PointInTime: timestamppb.New(req.PointInTime),
	})
	if err != nil {
		return managedpostgres.ObservedDatabase{}, providerError(err)
	}
	return p.observed(rt), nil
}

// Delete removes the VM, its volume and every published verifier.
func (p *Provider) Delete(ctx context.Context, req managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	id := req.ProviderResourceID
	if id == "" {
		id = resourceUUID(req.ResourceID)
	}
	if _, err := p.scheduler.DeleteDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: id}); err != nil && status.Code(err) != codes.NotFound {
		return managedpostgres.DeleteResult{}, providerError(err)
	}
	if err := p.verifiers.DeleteDatabase(ctx, id); err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	return managedpostgres.DeleteResult{Done: true}, nil
}

// IssueCredentials sets the binding's password on its role, as a verifier,
// and publishes the same verifier to pgproxyd. Issuing the same identity
// again returns the same login (the contract's idempotency); the password
// is derived, never stored.
func (p *Provider) IssueCredentials(ctx context.Context, req managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	if req.ProviderResourceID == "" || req.IdentityKey == "" {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrInvalid
	}
	role := credentialRoleName(req.ProviderResourceID, req.IdentityKey, req.Access)
	password, salt := p.derive(req)
	verifier, err := pgscram.NewVerifierWithSalt(password, salt, pgscram.DefaultIterations)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	batch, err := credentialSQL(p.cfg.Database, role, req.Access, verifier.String())
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	if err := p.admin(ctx, req.ProviderResourceID, batch); err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	if err := p.verifiers.Put(ctx, req.ProviderResourceID, role, verifier); err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: role, Username: role, Password: password, Database: p.cfg.Database, TLSMode: p.cfg.TLSMode,
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: req.ProviderResourceID + "." + p.cfg.HostSuffix, Port: p.cfg.Port}},
	}
	return material, material.Validate()
}

// RevokeCredentials disables the role and removes its verifier, so the
// proxy refuses it before anything wakes.
func (p *Provider) RevokeCredentials(ctx context.Context, req managedpostgres.CredentialRequest) error {
	role := credentialRoleName(req.ProviderResourceID, req.IdentityKey, req.Access)
	if err := p.verifiers.Delete(ctx, req.ProviderResourceID, role); err != nil {
		return err
	}
	batch, err := revokeSQL(role)
	if err != nil {
		return err
	}
	err = p.admin(ctx, req.ProviderResourceID, batch)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return nil
	}
	return err
}

// Usage reads the window from schedd's ledger, which outlives the database
// so its last windows stay collectable after deletion. Asking again for a
// past window gives the same answer.
func (p *Provider) Usage(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow) (managedpostgres.Usage, error) {
	if providerResourceID == "" || window.From.IsZero() || !window.To.After(window.From) {
		return managedpostgres.Usage{}, managedpostgres.ErrInvalid
	}
	resp, err := p.scheduler.DatabaseUsage(ctx, &scheddpb.DatabaseUsageRequest{
		DatabaseId: providerResourceID, From: timestamppb.New(window.From), To: timestamppb.New(window.To),
	})
	if err != nil {
		return managedpostgres.Usage{}, providerError(err)
	}
	usage := managedpostgres.Usage{Window: window, Readings: []managedpostgres.MeterReading{
		{Meter: managedpostgres.MeterActiveSeconds, Quantity: resp.GetActiveSeconds()},
		{Meter: managedpostgres.MeterComputeUnitSeconds, Quantity: resp.GetComputeUnitSeconds()},
		{Meter: managedpostgres.MeterStorageByteSeconds, Quantity: resp.GetStorageByteSeconds()},
		{Meter: managedpostgres.MeterHistoryByteSeconds, Quantity: resp.GetHistoryByteSeconds()},
		{Meter: managedpostgres.MeterEgressBytes, Quantity: resp.GetEgressBytes()},
	}}
	if err := usage.Validate(); err != nil {
		return managedpostgres.Usage{}, managedpostgres.ErrUnavailable
	}
	return usage, nil
}

// admin runs a batch through schedd, which wakes the database first.
func (p *Provider) admin(ctx context.Context, databaseID, sql string) error {
	resp, err := p.scheduler.DatabaseAdmin(ctx, &scheddpb.DatabaseAdminRequest{DatabaseId: databaseID, Sql: sql})
	if err != nil {
		return providerError(err)
	}
	if resp.GetErrorCode() != "" {
		// P0001 is the batch's own privilege assertion; anything else is
		// an unexpected SQL failure. Neither carries PostgreSQL output.
		if resp.GetSqlstate() == "P0001" {
			return managedpostgres.ErrConflict
		}
		return fmt.Errorf("%w: admin batch failed (sqlstate %s)", managedpostgres.ErrUnavailable, resp.GetSqlstate())
	}
	return nil
}

// derive returns the credential's password and SCRAM salt. Both are keyed
// HMACs of the binding (database, identity, access), so a re-issue is
// byte-identical: the same password and the same verifier, so pgproxyd's
// cached verifier never goes stale. A rotation is a new identity key, hence
// a new role and password. The password is URL-safe base64: printable
// ASCII, for which SASLprep is the identity.
func (p *Provider) derive(req managedpostgres.CredentialRequest) (string, []byte) {
	mac := func(label string) []byte {
		h := hmac.New(sha256.New, p.cfg.CredentialKey)
		h.Write([]byte(label + "\x00" + req.ProviderResourceID + "\x00" + req.IdentityKey + "\x00" + string(req.Access)))
		return h.Sum(nil)
	}
	return base64.RawURLEncoding.EncodeToString(mac("firecracker-password-v1")), mac("firecracker-scram-salt-v1")[:16]
}

// providerError normalizes schedd errors to the package sentinels. Schedd's
// status messages carry ids and states, never credentials.
func providerError(err error) error {
	sentinel := managedpostgres.ErrUnavailable
	st := status.Convert(err)
	switch st.Code() {
	case codes.NotFound:
		sentinel = managedpostgres.ErrNotFound
	case codes.InvalidArgument:
		sentinel = managedpostgres.ErrInvalid
	case codes.FailedPrecondition:
		sentinel = managedpostgres.ErrConflict
	case codes.ResourceExhausted:
		sentinel = managedpostgres.ErrQuotaExceeded
	}
	return fmt.Errorf("%w: schedd %s: %s", sentinel, st.Code(), st.Message())
}
