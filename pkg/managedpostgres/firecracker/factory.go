package firecracker

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/pgscram"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Driver is the registry driver name of this backend.
const Driver = "firecracker"

// Settings keys for reaching schedd as apid.
const (
	settingScheddTarget     = "schedd_target"
	settingScheddClientCert = "schedd_client_cert_path"
	settingScheddClientKey  = "schedd_client_key_path"
	settingScheddClientCA   = "schedd_client_ca_path"
	// credentialKeySecret is the secret_env entry naming the environment
	// variable (sealed in apid's env) that holds the credential key.
	credentialKeySecret = "credential-key"
)

// Factory returns the managedpostgres.Factory for this backend. It dials
// schedd with apid's client certificate and publishes role verifiers
// through pool, the control-plane database apid already owns.
func Factory(pool *pgxpool.Pool) managedpostgres.Factory {
	return func(backend managedpostgres.BackendConfig, getenv func(string) string) (managedpostgres.Provider, error) {
		return newFromBackend(pool, backend, getenv)
	}
}

// newFromBackend builds the provider from its backend configuration.
//
//nolint:contextcheck // the schedd connection lives as long as the provider, past any caller's context; the dial is lazy.
func newFromBackend(pool *pgxpool.Pool, backend managedpostgres.BackendConfig, getenv func(string) string) (managedpostgres.Provider, error) {
	if pool == nil {
		return nil, errors.New("firecracker: the native backend needs the control-plane database")
	}
	cfg, err := ParseConfig(backend)
	if err != nil {
		return nil, err
	}
	s := backend.Settings
	keyEnv := backend.SecretEnv[credentialKeySecret]
	if keyEnv == "" || getenv == nil {
		return nil, fmt.Errorf("firecracker: secret_env.%s must name the credential key variable", credentialKeySecret)
	}
	cfg.CredentialKey = []byte(getenv(keyEnv))
	target := s[settingScheddTarget]
	if target == "" {
		target = "unix:///run/faas/schedd.sock"
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("schedd_client", s[settingScheddClientCert], s[settingScheddClientKey], s[settingScheddClientCA])
	if err != nil {
		return nil, err
	}
	conn, err := overlay.Dial(context.Background(), overlay.New(target), tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("firecracker: dial schedd: %w", err)
	}
	store, err := dbcreds.NewVerifierStore(pool, 0)
	if err != nil {
		return nil, err
	}
	return New(cfg, scheddpb.NewScheddClient(conn), store)
}

// DescribeFactory builds the provider from its backend configuration alone,
// for tooling that only reads what a backend is: approval verification and
// configuration checks compare capabilities and the configuration
// fingerprint. It connects to nothing and needs no secrets; every call that
// would reach schedd or the verifier store fails ErrUnavailable, so it can
// never provision, issue credentials or delete.
func DescribeFactory() managedpostgres.Factory {
	return func(backend managedpostgres.BackendConfig, _ func(string) string) (managedpostgres.Provider, error) {
		cfg, err := ParseConfig(backend)
		if err != nil {
			return nil, err
		}
		// Capabilities never read the key; nothing can be issued with it.
		cfg.CredentialKey = make([]byte, MinCredentialKeyBytes)
		return New(cfg, unavailableScheduler{}, unavailableVerifiers{})
	}
}

var errDescribeOnly = status.Error(codes.Unavailable, "firecracker: a described backend reaches no schedd")

// unavailableScheduler answers every call Unavailable.
type unavailableScheduler struct{}

func (unavailableScheduler) ProvisionDatabase(context.Context, *scheddpb.ProvisionDatabaseRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) GetDatabase(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) DatabaseAdmin(context.Context, *scheddpb.DatabaseAdminRequest, ...grpc.CallOption) (*scheddpb.DatabaseAdminResponse, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) DeleteDatabase(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*scheddpb.DeleteDatabaseResponse, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) SetDatabaseAlwaysOn(context.Context, *scheddpb.SetDatabaseAlwaysOnRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) ParkDatabase(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) RestoreDatabase(context.Context, *scheddpb.RestoreDatabaseRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) DatabaseUsage(context.Context, *scheddpb.DatabaseUsageRequest, ...grpc.CallOption) (*scheddpb.DatabaseUsageResponse, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) ResizeDatabase(context.Context, *scheddpb.ResizeDatabaseRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) UpgradeDatabase(context.Context, *scheddpb.UpgradeDatabaseRequest, ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	return nil, errDescribeOnly
}

func (unavailableScheduler) ImportDatabase(context.Context, *scheddpb.ImportDatabaseRequest, ...grpc.CallOption) (*scheddpb.DatabaseImportProgress, error) {
	return nil, errDescribeOnly
}

func (unavailableScheduler) DatabaseImportStatus(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*scheddpb.DatabaseImportProgress, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) CaptureDatabaseSnapshot(context.Context, *scheddpb.CaptureDatabaseSnapshotRequest, ...grpc.CallOption) (*scheddpb.DatabaseSnapshot, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) GetDatabaseSnapshot(context.Context, *scheddpb.DatabaseSnapshotRef, ...grpc.CallOption) (*scheddpb.DatabaseSnapshot, error) {
	return nil, errDescribeOnly
}
func (unavailableScheduler) DeleteDatabaseSnapshot(context.Context, *scheddpb.DatabaseSnapshotRef, ...grpc.CallOption) (*scheddpb.DeleteDatabaseSnapshotResponse, error) {
	return nil, errDescribeOnly
}

// unavailableVerifiers refuses every write.
type unavailableVerifiers struct{}

func (unavailableVerifiers) Put(context.Context, string, string, pgscram.Verifier) error {
	return managedpostgres.ErrUnavailable
}
func (unavailableVerifiers) Delete(context.Context, string, string) error {
	return managedpostgres.ErrUnavailable
}
func (unavailableVerifiers) DeleteDatabase(context.Context, string) error {
	return managedpostgres.ErrUnavailable
}
