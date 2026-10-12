// Package firecracker is the native managed-PostgreSQL backend (ADR-1027):
// one Firecracker VM per database on Gregale's own database nodes, provisioned
// through schedd's database scheduler and reached through pgproxyd. It
// implements managedpostgres.Provider. The driver never stores a password:
// credentials are SCRAM verifiers set inside the VM and published to the
// proxy.
package firecracker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
}

// Settings keys in BackendConfig.Settings.
const (
	settingHostSuffix = "host_suffix"
	settingPort       = "port"
	settingTLSMode    = "tls_mode"
	settingDatabase   = "database_name"
	settingMajors     = "postgres_majors"
	settingMaxStorage = "max_storage_bytes"
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
	return cfg, nil
}

// Provider is the native backend.
type Provider struct {
	cfg       Config
	scheduler Scheduler
	verifiers Verifiers
}

// New builds a Provider over a schedd client and the verifier store.
func New(cfg Config, scheduler Scheduler, verifiers Verifiers) (*Provider, error) {
	if scheduler == nil || verifiers == nil {
		return nil, errors.New("firecracker: scheduler and verifier store are required")
	}
	return &Provider{cfg: cfg, scheduler: scheduler, verifiers: verifiers}, nil
}

var _ managedpostgres.Provider = (*Provider)(nil)

// Capabilities states what the backend does today. It has no point-in-time
// restore or usage meters yet (ADR-1027 phasing), so it accepts only specs
// without a restore window and reports no usage.
func (p *Provider) Capabilities() managedpostgres.Capabilities {
	classes := make([]managedpostgres.ServiceClass, 0, len(p.cfg.Classes))
	for _, class := range []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction} {
		if _, ok := p.cfg.Classes[class]; ok {
			classes = append(classes, class)
		}
	}
	return managedpostgres.Capabilities{
		PostgresMajors:   append([]int(nil), p.cfg.Majors...),
		ServiceClasses:   classes,
		Availability:     []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration},
		ScaleToZero:      true,
		MaxStorageBytes:  p.cfg.MaxStorageBytes,
	}
}

// Provision creates the database VM and its customer database. Schedd
// placement is idempotent per resource id, and so is the init batch.
func (p *Provider) Provision(ctx context.Context, req managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	if err := p.supports(req.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	shape := p.cfg.Classes[req.Spec.Class]
	rt, err := p.scheduler.ProvisionDatabase(ctx, &scheddpb.ProvisionDatabaseRequest{
		DatabaseId: req.ResourceID, PostgresMajor: int32(req.Spec.PostgresMajor), VcpuCount: int32(shape.vcpus),
		MemMib: int32(shape.memMiB), VolumeBytes: req.Spec.StorageLimitBytes,
	})
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
	if err := p.admin(ctx, req.ResourceID, init); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	return p.Inspect(ctx, req.ResourceID)
}

// supports admits only specs Inspect reports back unchanged, since the
// service fails a database whose observed spec differs. Always-on (no
// idle parking) and an unbounded volume have no VM equivalent yet.
func (p *Provider) supports(spec managedpostgres.Spec) error {
	if err := p.Capabilities().Supports(spec); err != nil {
		return err
	}
	if spec.Region != p.cfg.Region || !spec.ScaleToZero || spec.StorageLimitBytes <= 0 {
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
	obs := managedpostgres.ObservedDatabase{ProviderResourceID: rt.GetDatabaseId(), ComputeState: managedpostgres.ComputeStateUnknown}
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
		ScaleToZero: true, StorageLimitBytes: rt.GetVolumeBytes(),
	}
	for class, s := range p.cfg.Classes {
		if int32(s.vcpus) == rt.GetVcpuCount() && int32(s.memMiB) == rt.GetMemMib() {
			spec.Class = class
		}
	}
	return spec
}

// Update, Restore: not yet (ADR-1027 phasing).
func (p *Provider) Update(context.Context, managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
}

func (p *Provider) Restore(context.Context, managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
}

// Delete removes the VM, its volume and every published verifier.
func (p *Provider) Delete(ctx context.Context, req managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	id := req.ProviderResourceID
	if id == "" {
		id = req.ResourceID
	}
	if _, err := p.scheduler.DeleteDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: id}); err != nil && status.Code(err) != codes.NotFound {
		return managedpostgres.DeleteResult{}, providerError(err)
	}
	if err := p.verifiers.DeleteDatabase(ctx, id); err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	return managedpostgres.DeleteResult{Done: true}, nil
}

// IssueCredentials sets a fresh password on the binding's role, as a
// verifier, and publishes the verifier to pgproxyd. Issuing again rotates
// the password; the returned material is the only copy.
func (p *Provider) IssueCredentials(ctx context.Context, req managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	if req.ProviderResourceID == "" || req.IdentityKey == "" {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrInvalid
	}
	role := credentialRoleName(req.ProviderResourceID, req.IdentityKey, req.Access)
	password, err := newPassword()
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	verifier, err := pgscram.NewVerifier(password, pgscram.DefaultIterations)
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

// Usage reports no meters yet (ADR-1027 phasing: metering).
func (p *Provider) Usage(_ context.Context, _ string, window managedpostgres.UsageWindow) (managedpostgres.Usage, error) {
	return managedpostgres.Usage{Window: window}, nil
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

// newPassword returns 32 random bytes as URL-safe base64: printable ASCII,
// so SASLprep is the identity and the verifier matches every client.
func newPassword() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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
