package state

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GitHubAccountConnections removes deployment access without changing sign-in
// identities, uninstalling the GitHub App, or touching another account.
type GitHubAccountConnections interface {
	DisconnectGitHubAccountInstallation(context.Context, string, int64) error
}

// RefreshGitHubConnectionToken updates an existing authorization only. Matching
// its old envelope prevents a delayed refresh from resurrecting a disconnect or
// overwriting a newer OAuth authorization.
func (s *PgStore) RefreshGitHubConnectionToken(ctx context.Context, previous []byte, inst GitHubInstall) error {
	account, err := parsePgUUID(inst.AccountID)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().RefreshGitHubConnectionToken(ctx, s.pool, sqlc.RefreshGitHubConnectionTokenParams{
		AccountID: account, InstallationID: inst.InstallationID, PreviousToken: previous,
		Token: inst.SealedToken, Expires: pgtype.Timestamptz{Time: inst.TokenExpiresAt, Valid: true},
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) DisconnectGitHubAccountInstallation(ctx context.Context, accountID string, installationID int64) error {
	account, err := parsePgUUID(accountID)
	if err != nil || installationID <= 0 {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	// Delete first: a concurrent refresh/bind can no longer authorize this edge.
	if err := q.DisconnectGitHubConnection(ctx, tx, sqlc.DisconnectGitHubConnectionParams{AccountID: account, InstallationID: installationID}); err != nil {
		return err
	}
	apps, err := q.GitHubConnectionApps(ctx, tx, sqlc.GitHubConnectionAppsParams{AccountID: account, GithubInstallID: pgtype.Int8{Int64: installationID, Valid: true}})
	if err != nil {
		return err
	}
	for _, appID := range apps {
		if err := markAppPRPreviewsStaleTx(ctx, tx, appID); err != nil {
			return err
		}
	}
	if err := q.DisconnectGitHubConnectionApps(ctx, tx, sqlc.DisconnectGitHubConnectionAppsParams{AccountID: account, GithubInstallID: pgtype.Int8{Int64: installationID, Valid: true}}); err != nil {
		return err
	}
	if err := q.DisconnectGitHubConnectionProjects(ctx, tx, sqlc.DisconnectGitHubConnectionProjectsParams{AccountID: account, InstallID: pgtype.Int8{Int64: installationID, Valid: true}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *MemStore) RefreshGitHubConnectionToken(_ context.Context, previous []byte, inst GitHubInstall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := inst.AccountID + "\x00" + strconv.FormatInt(inst.InstallationID, 10)
	current, ok := m.githubInstalls[key]
	if !ok || !bytes.Equal(current.SealedToken, previous) {
		return ErrConflict
	}
	current.SealedToken = inst.SealedToken
	current.TokenExpiresAt = inst.TokenExpiresAt
	current.SealedAt = time.Now()
	m.githubInstalls[key] = current
	return nil
}

func (m *MemStore) DisconnectGitHubAccountInstallation(_ context.Context, accountID string, installationID int64) error {
	if accountID == "" || installationID <= 0 {
		return ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.githubInstalls, accountID+"\x00"+strconv.FormatInt(installationID, 10))
	delete(m.githubVerifications, accountID+"\x00"+strconv.FormatInt(installationID, 10))
	for id, app := range m.apps {
		if app.AccountID != accountID {
			continue
		}
		b := m.githubBindings[id]
		p := m.projects[app.ProjectID]
		if b.InstallID == installationID || p.InstallID == installationID {
			m.markAppPRPreviewsStaleLocked(app)
		}
		if b.InstallID == installationID {
			delete(m.githubBindings, id)
		}
	}
	for id, p := range m.projects {
		if p.AccountID == accountID && p.InstallID == installationID {
			delete(m.projectsByInstallRepo, installRepoKey{AccountID: p.AccountID, InstallID: p.InstallID, RepoFullName: p.RepoFullName})
			p.InstallID = 0
			m.projects[id] = p
		}
	}
	return nil
}

// GitHubConnectionVerification is a credential-free snapshot of live access.
type GitHubConnectionVerification struct {
	AccountType         string            `json:"account_type"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
	RepositoryCount     int               `json:"repository_count"`
	VerifiedAt          time.Time         `json:"verified_at"`
}

func (s *PgStore) RecordGitHubConnectionVerification(ctx context.Context, accountID string, installID int64, v GitHubConnectionVerification) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	n, err := sqlc.New().RecordGitHubConnectionVerification(ctx, s.pool, sqlc.RecordGitHubConnectionVerificationParams{AccountID: account, InstallationID: installID, VerifiedAt: pgtype.Timestamptz{Time: v.VerifiedAt, Valid: true}, Access: data})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PgStore) GitHubConnectionVerifications(ctx context.Context, accountID string) (map[int64]GitHubConnectionVerification, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	rows, err := sqlc.New().GitHubConnectionVerifications(ctx, s.pool, account)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]GitHubConnectionVerification, len(rows))
	for _, row := range rows {
		var v GitHubConnectionVerification
		if err := json.Unmarshal(row.Access, &v); err != nil {
			return nil, err
		}
		v.VerifiedAt = row.VerifiedAt.Time
		out[row.InstallationID] = v
	}
	return out, nil
}
func (m *MemStore) RecordGitHubConnectionVerification(_ context.Context, accountID string, installID int64, v GitHubConnectionVerification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + strconv.FormatInt(installID, 10)
	if _, ok := m.githubInstalls[key]; !ok {
		return ErrNotFound
	}
	if m.githubVerifications == nil {
		m.githubVerifications = make(map[string]GitHubConnectionVerification)
	}
	v.Permissions = maps.Clone(v.Permissions)
	m.githubVerifications[key] = v
	return nil
}
func (m *MemStore) GitHubConnectionVerifications(_ context.Context, accountID string) (map[int64]GitHubConnectionVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64]GitHubConnectionVerification)
	for key, inst := range m.githubInstalls {
		if inst.AccountID == accountID {
			if v, ok := m.githubVerifications[key]; ok {
				v.Permissions = maps.Clone(v.Permissions)
				out[inst.InstallationID] = v
			}
		}
	}
	return out, nil
}
