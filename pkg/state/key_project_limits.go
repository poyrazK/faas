package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// MaxKeyProjects is how many projects one key may be limited to; the
// api_key_project_limits CHECK enforces the same bound.
const MaxKeyProjects = 100

// APIKeyProjectLimitReader reads immutable key restrictions without credential
// material. The object upload edge enforces them independently of apid's version.
type APIKeyProjectLimitReader interface {
	APIKeyProjectLimit(ctx context.Context, keyID string) ([]string, bool, error)
}

var _ APIKeyProjectLimitReader = (*EdgeStore)(nil)

// ProjectLimitable reports whether a key can carry a project restriction.
func (k APIKey) ProjectLimitable() bool {
	return slices.Contains(k.Scopes, api.ScopeAgent) || k.Label == api.MCPLoginKeyLabel
}

// KeyProjectLimitStore keeps the projects an agent or mcp-login key is
// limited to, and the projects a hosted MCP sign-in's code was limited to
// on the approval page (ADR-20261104085712). apid is the only writer.
type KeyProjectLimitStore interface {
	APIKeyProjectLimitReader
	// SetAPIKeyProjectLimit limits a key to projects; it is set once, when
	// the key is minted. ErrConflict when the key already has a limit.
	SetAPIKeyProjectLimit(ctx context.Context, keyID string, projectIDs []string) error
	// SetCliAuthCodeProjects records the projects chosen for a code waiting
	// for approval (nil: every project). ErrNotFound when no such code waits.
	SetCliAuthCodeProjects(ctx context.Context, tokenHash []byte, projectIDs []string) error
	// CliAuthCodeProjects returns the projects a code was limited to, and
	// whether it was.
	CliAuthCodeProjects(ctx context.Context, tokenHash []byte) ([]string, bool, error)
}

func validKeyProjects(projectIDs []string) bool {
	return len(projectIDs) > 0 && len(projectIDs) <= MaxKeyProjects
}

func (s *PgStore) SetAPIKeyProjectLimit(ctx context.Context, keyID string, projectIDs []string) error {
	if !validKeyProjects(projectIDs) {
		return ErrConflict
	}
	err := sqlc.New().InsertAPIKeyProjectLimit(ctx, s.pool, sqlc.InsertAPIKeyProjectLimitParams{KeyID: mustPgUUID(keyID), ProjectIds: pgUUIDList(projectIDs)})
	return mapErr(err)
}

// APIKeyProjectLimit returns the key's projects and whether it is limited.
// It is also available on EdgeStore; only apid can change the restriction.
func (s *pgCore) APIKeyProjectLimit(ctx context.Context, keyID string) ([]string, bool, error) {
	ids, err := sqlc.New().GetAPIKeyProjectLimit(ctx, s.pool, mustPgUUID(keyID))
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return pgUUIDStrings(ids), true, nil
}

func (s *PgStore) SetCliAuthCodeProjects(ctx context.Context, tokenHash []byte, projectIDs []string) error {
	if projectIDs != nil && !validKeyProjects(projectIDs) {
		return ErrConflict
	}
	var ids []pgtype.UUID
	if projectIDs != nil {
		ids = pgUUIDList(projectIDs)
	}
	_, err := firstCredential(ctx, credentialLookups(credTagCLIAuthCode, tokenHash), func(ctx context.Context, value []byte) (struct{}, error) {
		n, err := sqlc.New().SetCliAuthCodeProjects(ctx, s.pool, sqlc.SetCliAuthCodeProjectsParams{ProjectIds: ids, TokenHash: value})
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		if n == 0 {
			return struct{}{}, ErrNotFound
		}
		return struct{}{}, nil
	})
	return err
}

// copyKeyProjectLimitSQL gives a rotated key's successor the predecessor's
// project limit, if it has one.
const copyKeyProjectLimitSQL = `insert into api_key_project_limits (key_id, project_ids)
select $2::uuid, project_ids from api_key_project_limits where key_id = $1::uuid`

// copyKeyProjectLimitTx runs in a rotation's transaction, so a key limited to
// projects is never rotated into an account-wide one (ADR-20261104085712).
func copyKeyProjectLimitTx(ctx context.Context, tx pgx.Tx, oldKeyID, newKeyID string) error {
	if _, err := tx.Exec(ctx, copyKeyProjectLimitSQL, oldKeyID, newKeyID); err != nil {
		return fmt.Errorf("copy key project limit: %w", mapErr(err))
	}
	return nil
}

// copyKeyProjectLimitLocked is copyKeyProjectLimitTx for the MemStore; the
// caller holds m.mu.
func (m *MemStore) copyKeyProjectLimitLocked(oldKeyID, newKeyID string) {
	if ids, ok := m.keyProjectLimits[oldKeyID]; ok {
		m.keyProjectLimits[newKeyID] = slices.Clone(ids)
	}
}

func (s *PgStore) CliAuthCodeProjects(ctx context.Context, tokenHash []byte) ([]string, bool, error) {
	type limit struct {
		ids     []string
		limited bool
	}
	got, err := firstCredential(ctx, credentialLookups(credTagCLIAuthCode, tokenHash), func(ctx context.Context, value []byte) (limit, error) {
		ids, err := sqlc.New().GetCliAuthCodeProjects(ctx, s.pool, value)
		if err != nil {
			return limit{}, mapErr(err)
		}
		if ids == nil {
			return limit{}, nil
		}
		return limit{ids: pgUUIDStrings(ids), limited: true}, nil
	})
	return got.ids, got.limited, err
}

func (m *MemStore) SetAPIKeyProjectLimit(_ context.Context, keyID string, projectIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validKeyProjects(projectIDs) {
		return ErrConflict
	}
	if _, ok := m.keyProjectLimits[keyID]; ok {
		return ErrConflict
	}
	found := false
	for _, k := range m.keys {
		if k.ID == keyID {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	if m.keyProjectLimits == nil {
		m.keyProjectLimits = map[string][]string{}
	}
	m.keyProjectLimits[keyID] = slices.Clone(projectIDs)
	return nil
}

func (m *MemStore) APIKeyProjectLimit(_ context.Context, keyID string) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids, ok := m.keyProjectLimits[keyID]
	return slices.Clone(ids), ok, nil
}

func (m *MemStore) SetCliAuthCodeProjects(_ context.Context, tokenHash []byte, projectIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if projectIDs != nil && !validKeyProjects(projectIDs) {
		return ErrConflict
	}
	key, ok := rawCredentialKey(m.cliAuthCodes, credTagCLIAuthCode, tokenHash)
	row := m.cliAuthCodes[key]
	if !ok || row.AccountID != "" || !row.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	row.ProjectIDs = slices.Clone(projectIDs)
	m.cliAuthCodes[key] = row
	return nil
}

func (m *MemStore) CliAuthCodeProjects(_ context.Context, tokenHash []byte) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := rawCredentialKey(m.cliAuthCodes, credTagCLIAuthCode, tokenHash)
	if !ok {
		return nil, false, ErrNotFound
	}
	ids := m.cliAuthCodes[key].ProjectIDs
	return slices.Clone(ids), ids != nil, nil
}
