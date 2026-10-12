package firecracker

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/overlay"
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
)

// Factory returns the managedpostgres.Factory for this backend. It dials
// schedd with apid's client certificate and publishes role verifiers
// through pool, the control-plane database apid already owns.
func Factory(pool *pgxpool.Pool) managedpostgres.Factory {
	return func(backend managedpostgres.BackendConfig, _ func(string) string) (managedpostgres.Provider, error) {
		if pool == nil {
			return nil, errors.New("firecracker: the native backend needs the control-plane database")
		}
		cfg, err := ParseConfig(backend)
		if err != nil {
			return nil, err
		}
		s := backend.Settings
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
}
