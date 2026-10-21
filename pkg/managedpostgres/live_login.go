package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/livequery/schemalog"
)

// LiveLoginRequest names the database whose live-query login to issue or
// revoke (ADR-20261016151947).
type LiveLoginRequest struct {
	ProviderResourceID string
	// Publication is the publication the login decodes; issuing creates it
	// FOR ALL TABLES when it is missing.
	Publication string
	// Slot is the replication slot the login decodes; revoking drops it.
	Slot string
}

// LiveLoginIssuer is a provider that prepares a database for live queries
// and issues the login livequeryd decodes it with:
//   - preparing installs the schema-change log and creates the publication,
//     as the database's owner;
//   - the login has REPLICATION, reads the customer schemas, and may SET ROLE
//     to the database's binding logins, so a live query reads as the app it
//     serves; it inherits none of their privileges.
//
// Issuing again keeps the login and returns a new password. Revoking stops
// the login's sessions, drops its slot and the login, and leaves the
// schema-change log and publication.
type LiveLoginIssuer interface {
	IssueLiveLogin(context.Context, LiveLoginRequest) (CredentialMaterial, error)
	RevokeLiveLogin(context.Context, LiveLoginRequest) error
}

// IssueLiveLogin issues database's live-query login. ok is false when its
// provider issues none: livequeryd then serves the database only when an
// operator lists it.
func (s *Service) IssueLiveLogin(ctx context.Context, database Database, slot string) (CredentialMaterial, bool, error) {
	issuer, err := s.liveLoginIssuer(database)
	if err != nil || issuer == nil {
		return CredentialMaterial{}, false, err
	}
	material, err := issuer.IssueLiveLogin(ctx, LiveLoginRequest{ProviderResourceID: database.ProviderResourceID,
		Publication: schemalog.Publication, Slot: slot})
	if err != nil {
		return CredentialMaterial{}, false, err
	}
	return material, true, nil
}

// RevokeLiveLogin revokes database's live-query login and drops its slot. A
// provider that issues no logins has nothing to revoke.
func (s *Service) RevokeLiveLogin(ctx context.Context, database Database, slot string) error {
	issuer, err := s.liveLoginIssuer(database)
	if err != nil || issuer == nil {
		return err
	}
	return issuer.RevokeLiveLogin(ctx, LiveLoginRequest{ProviderResourceID: database.ProviderResourceID,
		Publication: schemalog.Publication, Slot: slot})
}

func (s *Service) liveLoginIssuer(database Database) (LiveLoginIssuer, error) {
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	issuer, _ := backend.Provider.(LiveLoginIssuer)
	return issuer, nil
}
