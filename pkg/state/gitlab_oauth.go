package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GitLabOAuthConnection contains only sealed credentials. No access or
// refresh token is exposed to the API or audit surface.
type GitLabOAuthConnection struct {
	AccountID         string
	GitLabUserID      int64
	Username          string
	CredentialsSealed []byte
	ExpiresAt         time.Time
	Version           int64
}

type GitLabOAuthStore interface {
	GitLabOAuthConnection(context.Context, string) (GitLabOAuthConnection, error)
	PutGitLabOAuthConnection(context.Context, GitLabOAuthConnection) (GitLabOAuthConnection, error)
	RotateGitLabOAuthConnection(context.Context, string, int64, []byte, time.Time) (GitLabOAuthConnection, error)
	DeleteGitLabOAuthConnection(context.Context, string) error
}

func cloneGitLabOAuthConnection(c GitLabOAuthConnection) GitLabOAuthConnection {
	c.CredentialsSealed = append([]byte(nil), c.CredentialsSealed...)
	return c
}

func validGitLabOAuthConnection(c GitLabOAuthConnection) bool {
	return c.AccountID != "" && c.GitLabUserID > 0 && c.Username != "" && len(c.CredentialsSealed) > 0 && !c.ExpiresAt.IsZero()
}

func (m *MemStore) GitLabOAuthConnection(_ context.Context, accountID string) (GitLabOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.gitlabOAuthConnections[accountID]
	if !ok {
		return GitLabOAuthConnection{}, ErrNotFound
	}
	return cloneGitLabOAuthConnection(c), nil
}

func (m *MemStore) PutGitLabOAuthConnection(_ context.Context, c GitLabOAuthConnection) (GitLabOAuthConnection, error) {
	if !validGitLabOAuthConnection(c) {
		return GitLabOAuthConnection{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[c.AccountID]; !ok {
		return GitLabOAuthConnection{}, ErrNotFound
	}
	if m.gitlabOAuthConnections == nil {
		m.gitlabOAuthConnections = make(map[string]GitLabOAuthConnection)
	}
	c.Version = m.gitlabOAuthConnections[c.AccountID].Version + 1
	m.gitlabOAuthConnections[c.AccountID] = cloneGitLabOAuthConnection(c)
	return cloneGitLabOAuthConnection(c), nil
}

func (m *MemStore) RotateGitLabOAuthConnection(_ context.Context, accountID string, version int64, sealed []byte, expires time.Time) (GitLabOAuthConnection, error) {
	if accountID == "" || version <= 0 || len(sealed) == 0 || expires.IsZero() {
		return GitLabOAuthConnection{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.gitlabOAuthConnections[accountID]
	if !ok {
		return GitLabOAuthConnection{}, ErrNotFound
	}
	if c.Version != version {
		return GitLabOAuthConnection{}, ErrConflict
	}
	c.Version++
	c.CredentialsSealed = append([]byte(nil), sealed...)
	c.ExpiresAt = expires
	m.gitlabOAuthConnections[accountID] = c
	return cloneGitLabOAuthConnection(c), nil
}

func (m *MemStore) DeleteGitLabOAuthConnection(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.gitlabOAuthConnections[accountID]; !ok {
		return ErrNotFound
	}
	delete(m.gitlabOAuthConnections, accountID)
	return nil
}

func gitLabOAuthConnectionFromSQL(row sqlc.GitlabOauthConnection) GitLabOAuthConnection {
	return GitLabOAuthConnection{AccountID: pgUUIDString(row.AccountID), GitLabUserID: row.GitlabUserID,
		Username: row.Username, CredentialsSealed: append([]byte(nil), row.CredentialsSealed...),
		ExpiresAt: row.ExpiresAt.Time, Version: row.Version}
}

func (s *PgStore) GitLabOAuthConnection(ctx context.Context, accountID string) (GitLabOAuthConnection, error) {
	row, err := sqlc.New().GetGitLabOAuthConnection(ctx, s.pool, mustPgUUID(accountID))
	if err != nil {
		return GitLabOAuthConnection{}, mapErr(err)
	}
	return gitLabOAuthConnectionFromSQL(row), nil
}

func (s *PgStore) PutGitLabOAuthConnection(ctx context.Context, c GitLabOAuthConnection) (GitLabOAuthConnection, error) {
	if !validGitLabOAuthConnection(c) {
		return GitLabOAuthConnection{}, ErrInvalidArgument
	}
	row, err := sqlc.New().PutGitLabOAuthConnection(ctx, s.pool, sqlc.PutGitLabOAuthConnectionParams{
		AccountID: mustPgUUID(c.AccountID), GitlabUserID: c.GitLabUserID,
		Username: c.Username, CredentialsSealed: c.CredentialsSealed, ExpiresAt: gitOpsTime(c.ExpiresAt),
	})
	if err != nil {
		return GitLabOAuthConnection{}, mapErr(err)
	}
	return gitLabOAuthConnectionFromSQL(row), nil
}

func (s *PgStore) RotateGitLabOAuthConnection(ctx context.Context, accountID string, version int64, sealed []byte, expires time.Time) (GitLabOAuthConnection, error) {
	if accountID == "" || version <= 0 || len(sealed) == 0 || expires.IsZero() {
		return GitLabOAuthConnection{}, ErrInvalidArgument
	}
	row, err := sqlc.New().RotateGitLabOAuthConnection(ctx, s.pool, sqlc.RotateGitLabOAuthConnectionParams{
		AccountID: mustPgUUID(accountID), ExpectedVersion: version,
		CredentialsSealed: sealed, ExpiresAt: gitOpsTime(expires),
	})
	if err != nil {
		return GitLabOAuthConnection{}, mapErr(err)
	}
	return gitLabOAuthConnectionFromSQL(row), nil
}

func (s *PgStore) DeleteGitLabOAuthConnection(ctx context.Context, accountID string) error {
	rows, err := sqlc.New().DeleteGitLabOAuthConnection(ctx, s.pool, mustPgUUID(accountID))
	if err != nil {
		return mapErr(err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
