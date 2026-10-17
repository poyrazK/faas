package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"reflect"
	"sort"
	"strings"
	"time"
)

type mcpRetirementEntry struct {
	AccountID, Binding string
	Record             api.MCPOAuthRetirement
}
type MCPOAuthRetirementStore interface {
	PrepareMCPOAuthRetirement(context.Context, MCPCatalogObserverJob, []byte) (api.MCPOAuthRetirement, error)
	RecordMCPOAuthIssuedGrant(context.Context, string, string, []byte, string) error
	ListMCPOAuthRetirements(context.Context, string, int) (api.MCPOAuthRetirementsResponse, error)
	RecoverMCPOAuthRetirement(context.Context, string, string, api.MCPOAuthRetirementAction) (api.MCPOAuthRetirement, error)
}

func retirementBinding(c *api.MCPCatalogObserverOAuthConfig) string {
	if c == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(c.TokenEndpoint + "\n" + c.ClientID))
	return hex.EncodeToString(sum[:])
}
func retirementEvent(r *api.MCPOAuthRetirement, action string, at time.Time) {
	r.History = append(r.History, api.MCPOAuthRetirementEvent{At: at, Action: action})
	if len(r.History) > api.MCPOAuthRetirementHistoryLimit {
		r.History = r.History[len(r.History)-api.MCPOAuthRetirementHistoryLimit:]
	}
}
func newRetirement(app, slug, role, reason string, phase *api.MCPCatalogOAuthRevocation, at time.Time) api.MCPOAuthRetirement {
	r := api.MCPOAuthRetirement{ID: phase.ID, AppID: app, AppSlug: slug, Role: api.CatalogMonitorRole(role), Reason: reason, State: phase.State, CreatedAt: at, RetainUntil: at.Add(time.Duration(api.MCPOAuthRetirementRetentionSeconds) * time.Second), Revocation: phase, History: []api.MCPOAuthRetirementEvent{}}
	retirementEvent(&r, "queued", at)
	return r
}
func cloneRetirement(r api.MCPOAuthRetirement) api.MCPOAuthRetirement {
	data, _ := json.Marshal(r)
	var out api.MCPOAuthRetirement
	_ = json.Unmarshal(data, &out)
	return out
}
func retirementPhase(c *api.MCPCatalogObserverOAuthConfig, oauth *api.MCPCatalogObserverOAuthStatus, at time.Time) *api.MCPCatalogOAuthRevocation {
	cfg := *c
	cfg.Scopes = append([]string(nil), c.Scopes...)
	phase := &api.MCPCatalogOAuthRevocation{ID: uuid.NewString(), State: "pending", NextAttemptAt: at, Config: &cfg, Ciphertext: append([]byte(nil), oauth.Ciphertext...)}
	if c.RevocationEndpoint == "" {
		phase.State = "manual_required"
	}
	return phase
}
func retirementViews(root api.MCPCatalogMonitorSnapshot) map[string]api.MCPCatalogMonitorSnapshot {
	out := map[string]api.MCPCatalogMonitorSnapshot{}
	if root.Config.Revision != "" {
		v, _ := api.CatalogMonitorRoleSnapshot(root, "default")
		out["default"] = v
	}
	for role := range root.Roles {
		v, _ := api.CatalogMonitorRoleSnapshot(root, role)
		out[role] = v
	}
	return out
}
func retirementPgTime(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: true}
}
func insertRetirement(ctx context.Context, db sqlc.DBTX, account string, r api.MCPOAuthRetirement) error {
	if err := sqlc.New().LockMCPOAuthRetirementAccount(ctx, db, account); err != nil {
		return err
	}
	count, err := sqlc.New().CountMCPOAuthRetirements(ctx, db, mustPgUUID(account))
	if err != nil {
		return err
	}
	if count >= api.MCPOAuthRetirementMaxOutstanding {
		return ErrConflict
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = sqlc.New().InsertMCPOAuthRetirement(ctx, db, sqlc.InsertMCPOAuthRetirementParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(r.AppID), BindingSha256: retirementBinding(r.Revocation.Config), CreatedAt: retirementPgTime(r.CreatedAt), RetainUntil: retirementPgTime(r.RetainUntil), NextAttemptAt: retirementPgTime(r.Revocation.NextAttemptAt), Payload: data})
	return err
}
func (s *PgStore) captureRetirementClient(ctx context.Context, db sqlc.DBTX, account, app string, phase *api.MCPCatalogOAuthRevocation) {
	if phase.Config == nil || phase.Config.ClientSecretRef == "" || phase.Config.ResolvedRevocationAuthMethod() == "none" || len(phase.ClientCiphertext) > 0 {
		return
	}
	data, err := sqlc.New().MCPOAuthRetirementClientCiphertext(ctx, db, sqlc.MCPOAuthRetirementClientCiphertextParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Key: strings.TrimPrefix(phase.Config.ClientSecretRef, api.SecretRefPrefix)})
	if err == nil {
		phase.ClientCiphertext = append([]byte(nil), data...)
	}
}
func (m *MemStore) captureRetirementClientLocked(account, app string, phase *api.MCPCatalogOAuthRevocation) {
	if phase.Config == nil || phase.Config.ClientSecretRef == "" || phase.Config.ResolvedRevocationAuthMethod() == "none" || len(phase.ClientCiphertext) > 0 {
		return
	}
	key := strings.TrimPrefix(phase.Config.ClientSecretRef, api.SecretRefPrefix)
	secret, ok := m.secrets[secretKey{AppID: app, Scope: DefaultEnvScope, Key: key}]
	if ok && secret.AccountID == account && secret.ManagedPostgresBindingID == "" && secret.ManagedObjectStorageCredentialID == "" && secret.ManagedCredentialRef == "" {
		phase.ClientCiphertext = append([]byte(nil), secret.Ciphertext...)
	}
}
func (m *MemStore) insertRetirementLocked(account string, r api.MCPOAuthRetirement) error {
	count := 0
	for _, entry := range m.mcpOAuthRetirements {
		switch entry.Record.State {
		case "pending", "held", "awaiting_issuance", "manual_required":
			if entry.AccountID == account {
				count++
			}
		}
	}
	if count >= api.MCPOAuthRetirementMaxOutstanding {
		return ErrConflict
	}
	if m.mcpOAuthRetirements == nil {
		m.mcpOAuthRetirements = map[string]mcpRetirementEntry{}
	}
	if _, ok := m.mcpOAuthRetirements[r.ID]; ok {
		return ErrConflict
	}
	m.mcpOAuthRetirements[r.ID] = mcpRetirementEntry{AccountID: account, Binding: retirementBinding(r.Revocation.Config), Record: cloneRetirement(r)}
	return nil
}
func (s *PgStore) PrepareMCPOAuthRetirement(ctx context.Context, job MCPCatalogObserverJob, client []byte) (api.MCPOAuthRetirement, error) {
	var out api.MCPOAuthRetirement
	if job.Snapshot.Config.Observer == nil {
		return out, ErrInvalidArgument
	}
	cfg := job.Snapshot.Config.Observer.OAuth
	if cfg == nil || cfg.Validate() != nil || len(client) > api.MCPCatalogOAuthMaxResponseBytes {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockMCPOAuthRetirementAccount(ctx, tx, job.AccountID); err != nil {
		return out, err
	}
	config, _ := json.Marshal(cfg)
	valid, err := sqlc.New().MCPOAuthRetirementFlowCurrent(ctx, tx, sqlc.MCPOAuthRetirementFlowCurrentParams{AccountID: mustPgUUID(job.AccountID), AppID: mustPgUUID(job.AppID), Role: job.Role, Revision: job.Snapshot.Config.Revision, Config: config})
	if err != nil {
		return out, err
	}
	if !valid {
		return out, ErrMCPCatalogMonitorChanged
	}
	busy, err := sqlc.New().MCPOAuthRetirementBindingBusy(ctx, tx, sqlc.MCPOAuthRetirementBindingBusyParams{AccountID: mustPgUUID(job.AccountID), BindingSha256: retirementBinding(cfg)})
	if err != nil {
		return out, err
	}
	if busy {
		return out, ErrConflict
	}
	slug, err := sqlc.New().GetMCPOAuthRetirementApp(ctx, tx, sqlc.GetMCPOAuthRetirementAppParams{AppID: mustPgUUID(job.AppID), AccountID: mustPgUUID(job.AccountID)})
	if err != nil {
		return out, mapErr(err)
	}
	at := time.Now().UTC()
	phase := retirementPhase(cfg, &api.MCPCatalogObserverOAuthStatus{}, at)
	phase.State = "pending"
	phase.ClientCiphertext = append([]byte(nil), client...)
	phase.NextAttemptAt = at.Add(time.Duration(api.MCPCatalogOAuthFlowSeconds) * time.Second)
	out = newRetirement(job.AppID, slug, job.Role, "authorization_exchange", phase, at)
	out.State = "awaiting_issuance"
	if err = insertRetirement(ctx, tx, job.AccountID, out); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) PrepareMCPOAuthRetirement(_ context.Context, job MCPCatalogObserverJob, client []byte) (api.MCPOAuthRetirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out api.MCPOAuthRetirement
	if job.Snapshot.Config.Observer == nil {
		return out, ErrInvalidArgument
	}
	cfg := job.Snapshot.Config.Observer.OAuth
	app, ok := m.apps[job.AppID]
	root := m.mcpCatalogMonitors[job.AppID]
	view, found := api.CatalogMonitorRoleSnapshot(root, job.Role)
	if !ok || app.AccountID != job.AccountID || app.Status == AppDeleted || !found || view.Config.Revision != job.Snapshot.Config.Revision || view.ObserverStatus == nil || view.ObserverStatus.OAuthFlow == nil || !view.ObserverStatus.OAuthFlow.Consumed || !view.ObserverStatus.OAuthFlow.ExpiresAt.After(time.Now()) {
		return out, ErrMCPCatalogMonitorChanged
	}
	if cfg == nil || cfg.Validate() != nil || len(client) > api.MCPCatalogOAuthMaxResponseBytes {
		return out, ErrInvalidArgument
	}
	current, _ := json.Marshal(view.Config.Observer.OAuth)
	expected, _ := json.Marshal(cfg)
	if string(current) != string(expected) {
		return out, ErrMCPCatalogMonitorChanged
	}
	for _, entry := range m.mcpOAuthRetirements {
		if entry.AccountID == job.AccountID && entry.Binding == retirementBinding(cfg) && entry.Record.State == "pending" {
			return out, ErrConflict
		}
	}
	at := time.Now().UTC()
	phase := retirementPhase(cfg, &api.MCPCatalogObserverOAuthStatus{}, at)
	phase.State = "pending"
	phase.ClientCiphertext = append([]byte(nil), client...)
	phase.NextAttemptAt = at.Add(time.Duration(api.MCPCatalogOAuthFlowSeconds) * time.Second)
	out = newRetirement(app.ID, app.Slug, job.Role, "authorization_exchange", phase, at)
	out.State = "awaiting_issuance"
	err := m.insertRetirementLocked(job.AccountID, out)
	return out, err
}
func (s *PgStore) editRetirement(ctx context.Context, account, id string, edit func(*api.MCPOAuthRetirement, bool, time.Time) error) (api.MCPOAuthRetirement, error) {
	var out api.MCPOAuthRetirement
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockMCPOAuthRetirementAccount(ctx, tx, account); err != nil {
		return out, err
	}
	data, err := sqlc.New().LockMCPOAuthRetirement(ctx, tx, sqlc.LockMCPOAuthRetirementParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return out, mapErr(err)
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	active := false
	if cfg := out.Revocation.Config; cfg != nil {
		found, lookupErr := sqlc.New().MCPOAuthRetirementActiveBinding(ctx, tx, sqlc.MCPOAuthRetirementActiveBindingParams{AccountID: mustPgUUID(account), TokenEndpoint: cfg.TokenEndpoint, ClientID: cfg.ClientID, BindingSha256: retirementBinding(cfg), ID: mustPgUUID(id)})
		active = found.Valid && found.Bool
		err = lookupErr
		if err != nil {
			return out, err
		}
	}
	if err = edit(&out, active, time.Now().UTC()); err != nil {
		return out, err
	}
	data, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	_, err = sqlc.New().PutMCPOAuthRetirement(ctx, tx, sqlc.PutMCPOAuthRetirementParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), Payload: data, NextAttemptAt: retirementPgTime(out.Revocation.NextAttemptAt)})
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) activeRetirementBindingLocked(account, id, binding string, at time.Time) bool {
	for appID, root := range m.mcpCatalogMonitors {
		app, ok := m.apps[appID]
		if !ok || app.AccountID != account || app.Status == AppDeleted || m.mcpCatalogMonitorOwners[appID] != account {
			continue
		}
		for _, view := range retirementViews(root) {
			if view.ObserverStatus != nil && (view.ObserverStatus.OAuth != nil || view.ObserverStatus.Renewal != nil && (view.ObserverStatus.Renewal.State == "pending" || view.ObserverStatus.Renewal.State == "saved") || view.OAuthLifecycle != nil && (view.OAuthLifecycle.CredentialState == "credential_changed" || view.OAuthLifecycle.CredentialState == "retirement_held")) && view.Config.Observer != nil && retirementBinding(view.Config.Observer.OAuth) == binding {
				return true
			}
		}
	}
	for other, entry := range m.mcpOAuthRetirements {
		if other != id && entry.AccountID == account && entry.Binding == binding && entry.Record.State == "awaiting_issuance" && entry.Record.Revocation.NextAttemptAt.After(at) {
			return true
		}
	}
	return false
}
func (m *MemStore) editRetirement(account, id string, edit func(*api.MCPOAuthRetirement, bool, time.Time) error) (api.MCPOAuthRetirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.mcpOAuthRetirements[id]
	if !ok || entry.AccountID != account {
		return api.MCPOAuthRetirement{}, ErrNotFound
	}
	out := cloneRetirement(entry.Record)
	at := time.Now().UTC()
	if err := edit(&out, m.activeRetirementBindingLocked(account, id, entry.Binding, at), at); err != nil {
		return out, err
	}
	entry.Record = cloneRetirement(out)
	m.mcpOAuthRetirements[id] = entry
	return out, nil
}
func recordIssuedGrant(r *api.MCPOAuthRetirement, cipher []byte, reason string, at time.Time) error {
	if r.State != "awaiting_issuance" || !r.RetainUntil.After(at) || len(cipher) == 0 || len(cipher) > api.MCPCatalogOAuthMaxResponseBytes {
		return ErrMCPCatalogMonitorChanged
	}
	r.Revocation.Ciphertext = append([]byte(nil), cipher...)
	r.State = "pending"
	r.Revocation.NextAttemptAt = at.Add(time.Duration(api.MCPOAuthRetirementInstallGraceSeconds) * time.Second)
	r.Reason = reason
	if r.Revocation.Config == nil || r.Revocation.Config.RevocationEndpoint == "" {
		r.State = "manual_required"
		r.Revocation.State = "manual_required"
	}
	retirementEvent(r, "grant_observed", at)
	return nil
}
func (s *PgStore) RecordMCPOAuthIssuedGrant(ctx context.Context, account, id string, cipher []byte, reason string) error {
	_, err := s.editRetirement(ctx, account, id, func(r *api.MCPOAuthRetirement, _ bool, at time.Time) error {
		return recordIssuedGrant(r, cipher, reason, at)
	})
	return err
}
func (m *MemStore) RecordMCPOAuthIssuedGrant(_ context.Context, account, id string, cipher []byte, reason string) error {
	_, err := m.editRetirement(account, id, func(r *api.MCPOAuthRetirement, _ bool, at time.Time) error {
		return recordIssuedGrant(r, cipher, reason, at)
	})
	return err
}
func (s *PgStore) ListMCPOAuthRetirements(ctx context.Context, account string, offset int) (api.MCPOAuthRetirementsResponse, error) {
	out := api.MCPOAuthRetirementsResponse{Items: []api.MCPOAuthRetirement{}}
	if offset < 0 || offset > api.MCPOAuthRetirementMaxOffset {
		return out, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListMCPOAuthRetirements(ctx, s.pool, sqlc.ListMCPOAuthRetirementsParams{AccountID: mustPgUUID(account), PageOffset: int32(offset)})
	if err != nil {
		return out, err
	}
	for _, data := range rows {
		var r api.MCPOAuthRetirement
		if err = json.Unmarshal(data, &r); err != nil {
			return out, err
		}
		out.Items = append(out.Items, api.MCPOAuthRetirementPublic(r))
	}
	if len(out.Items) == api.MCPOAuthRetirementPageSize {
		out.NextOffset = offset + len(out.Items)
	}
	return out, nil
}
func (m *MemStore) ListMCPOAuthRetirements(_ context.Context, account string, offset int) (api.MCPOAuthRetirementsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.MCPOAuthRetirementsResponse{Items: []api.MCPOAuthRetirement{}}
	if offset < 0 || offset > api.MCPOAuthRetirementMaxOffset {
		return out, ErrInvalidArgument
	}
	for _, entry := range m.mcpOAuthRetirements {
		if entry.AccountID == account {
			out.Items = append(out.Items, api.MCPOAuthRetirementPublic(entry.Record))
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
	if offset >= len(out.Items) {
		out.Items = []api.MCPOAuthRetirement{}
		return out, nil
	}
	out.Items = out.Items[offset:]
	if len(out.Items) > api.MCPOAuthRetirementPageSize {
		out.Items = out.Items[:api.MCPOAuthRetirementPageSize]
		out.NextOffset = offset + len(out.Items)
	}
	return out, nil
}
func recoverRetirement(r *api.MCPOAuthRetirement, action api.MCPOAuthRetirementAction, at time.Time) error {
	phase := r.Revocation
	if phase.LeaseUntil != nil && phase.LeaseUntil.After(at) {
		return ErrConflict
	}
	if !r.RetainUntil.After(at) || r.State == "installed" || r.State == "provider_acknowledged" || r.State == "manually_acknowledged" || r.State == "expired" {
		return ErrConflict
	}
	switch action.Action {
	case "retry":
		if len(phase.Ciphertext) == 0 || phase.Config == nil || phase.Config.RevocationEndpoint == "" {
			return ErrInvalidArgument
		}
		r.State = "pending"
		phase.State = "pending"
		phase.Attempts = 0
		phase.FailureReason = ""
		phase.NextAttemptAt = at
		phase.LeaseID = ""
		phase.LeaseUntil = nil
		r.AllowActiveGrantRevocation = action.AllowActiveGrantRevocation
	case "acknowledge_manual":
		r.State = "manually_acknowledged"
		r.Revocation = api.MCPOAuthRetirementPublic(*r).Revocation
	default:
		return ErrInvalidArgument
	}
	retirementEvent(r, action.Action, at)
	return nil
}
func (s *PgStore) RecoverMCPOAuthRetirement(ctx context.Context, account, id string, action api.MCPOAuthRetirementAction) (api.MCPOAuthRetirement, error) {
	out, err := s.editRetirement(ctx, account, id, func(r *api.MCPOAuthRetirement, _ bool, at time.Time) error { return recoverRetirement(r, action, at) })
	return api.MCPOAuthRetirementPublic(out), err
}
func (m *MemStore) RecoverMCPOAuthRetirement(_ context.Context, account, id string, action api.MCPOAuthRetirementAction) (api.MCPOAuthRetirement, error) {
	out, err := m.editRetirement(account, id, func(r *api.MCPOAuthRetirement, _ bool, at time.Time) error { return recoverRetirement(r, action, at) })
	return api.MCPOAuthRetirementPublic(out), err
}

func queuedRetirements(before, after api.MCPCatalogMonitorSnapshot, app, slug string, at time.Time) []api.MCPOAuthRetirement {
	out := []api.MCPOAuthRetirement{}
	oldViews := retirementViews(before)
	for role, next := range retirementViews(after) {
		old := oldViews[role]
		if old.ObserverStatus != nil && old.ObserverStatus.Renewal != nil && old.ObserverStatus.Renewal.Result != nil && old.Config.Observer != nil && old.Config.Observer.OAuth != nil {
			prior := old.ObserverStatus.Renewal
			retained := next.ObserverStatus != nil && next.ObserverStatus.Renewal != nil && next.ObserverStatus.Renewal.ID == prior.ID && next.ObserverStatus.Renewal.Result != nil
			installed := next.ObserverStatus != nil && next.ObserverStatus.OAuth != nil && bytes.Equal(next.ObserverStatus.OAuth.Ciphertext, prior.Result.Ciphertext)
			if !retained && !installed {
				phase := retirementPhase(old.Config.Observer.OAuth, prior.Result, at)
				reason := "renewal_result_discarded"
				if next.OAuthLifecycle != nil && next.OAuthLifecycle.CredentialState != "" {
					reason = "credential_changed"
				}
				out = append(out, newRetirement(app, slug, role, reason, phase, at))
			}
		}
		if next.OAuthLifecycle == nil || next.OAuthLifecycle.Revocation == nil {
			continue
		}
		phase := next.OAuthLifecycle.Revocation
		if old.OAuthLifecycle != nil && old.OAuthLifecycle.Revocation != nil && old.OAuthLifecycle.Revocation.ID == phase.ID {
			continue
		}
		if old.ObserverStatus == nil || old.ObserverStatus.OAuth == nil || old.Config.Observer == nil || old.Config.Observer.OAuth == nil {
			continue
		}
		copy := *phase
		cfg := *old.Config.Observer.OAuth
		cfg.Scopes = append([]string(nil), cfg.Scopes...)
		copy.Config = &cfg
		copy.Ciphertext = append([]byte(nil), old.ObserverStatus.OAuth.Ciphertext...)
		reason := "disconnect"
		if next.ObserverStatus != nil && next.ObserverStatus.OAuth != nil {
			reason = "replacement"
		} else if next.Config.Observer == nil || !reflect.DeepEqual(next.Config.Observer.OAuth, &cfg) {
			reason = "configuration_changed"
		}
		if next.OAuthLifecycle.CredentialState != "" {
			reason = "credential_changed"
		}
		out = append(out, newRetirement(app, slug, role, reason, &copy, at))
	}
	return out
}
func publicRetirementViews(root *api.MCPCatalogMonitorSnapshot) {
	for role, view := range retirementViews(*root) {
		view.OAuthLifecycle = api.CatalogOAuthLifecyclePublic(view.OAuthLifecycle)
		setObserverView(root, role, view)
	}
}
func (s *PgStore) syncRetirements(ctx context.Context, db sqlc.DBTX, account, app string, before api.MCPCatalogMonitorSnapshot, after *api.MCPCatalogMonitorSnapshot, at time.Time) error {
	slug, err := sqlc.New().GetMCPOAuthRetirementApp(ctx, db, sqlc.GetMCPOAuthRetirementAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
	if err != nil {
		return mapErr(err)
	}
	for _, r := range queuedRetirements(before, *after, app, slug, at) {
		s.captureRetirementClient(ctx, db, account, app, r.Revocation)
		if err = insertRetirement(ctx, db, account, r); err != nil {
			return err
		}
	}
	for role, view := range retirementViews(*after) {
		if view.ObserverStatus == nil || view.ObserverStatus.OAuth == nil {
			continue
		}
		oauth := view.ObserverStatus.OAuth
		old, _ := api.CatalogMonitorRoleSnapshot(before, role)
		if oauth.RetirementID == "" || old.ObserverStatus != nil && old.ObserverStatus.OAuth != nil && old.ObserverStatus.OAuth.RetirementID == oauth.RetirementID {
			continue
		}
		if view.Config.Observer == nil || view.Config.Observer.OAuth == nil {
			return ErrInvalidArgument
		}
		phase := &api.MCPCatalogOAuthRevocation{ID: oauth.RetirementID, State: "pending", NextAttemptAt: at}
		data, _ := json.Marshal(phase)
		rows, e := sqlc.New().InstallMCPOAuthRetirement(ctx, db, sqlc.InstallMCPOAuthRetirementParams{ID: mustPgUUID(oauth.RetirementID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Role: role, BindingSha256: retirementBinding(view.Config.Observer.OAuth), CiphertextBase64: base64.StdEncoding.EncodeToString(oauth.Ciphertext), PublicPhase: data})
		if e != nil {
			return e
		}
		if rows != 1 {
			return ErrMCPCatalogMonitorChanged
		}
	}
	publicRetirementViews(after)
	return nil
}
func (m *MemStore) syncRetirementsLocked(account, app string, before api.MCPCatalogMonitorSnapshot, after *api.MCPCatalogMonitorSnapshot, at time.Time) error {
	// Roll back canonical queue changes if any part of this monitor mutation fails.
	saved := make(map[string]mcpRetirementEntry, len(m.mcpOAuthRetirements))
	for id, entry := range m.mcpOAuthRetirements {
		saved[id] = entry
	}
	failed := true
	defer func() {
		if failed {
			m.mcpOAuthRetirements = saved
		}
	}()
	for _, r := range queuedRetirements(before, *after, app, m.apps[app].Slug, at) {
		m.captureRetirementClientLocked(account, app, r.Revocation)
		if err := m.insertRetirementLocked(account, r); err != nil {
			return err
		}
	}
	for role, view := range retirementViews(*after) {
		if view.ObserverStatus == nil || view.ObserverStatus.OAuth == nil {
			continue
		}
		oauth := view.ObserverStatus.OAuth
		old, _ := api.CatalogMonitorRoleSnapshot(before, role)
		if oauth.RetirementID == "" || old.ObserverStatus != nil && old.ObserverStatus.OAuth != nil && old.ObserverStatus.OAuth.RetirementID == oauth.RetirementID {
			continue
		}
		entry, ok := m.mcpOAuthRetirements[oauth.RetirementID]
		if !ok || entry.AccountID != account || entry.Record.AppID != app || entry.Record.Role != role || (entry.Record.State != "pending" && entry.Record.State != "held" && entry.Record.State != "manual_required") || entry.Record.Reason != "authorization_exchange" || view.Config.Observer == nil || entry.Binding != retirementBinding(view.Config.Observer.OAuth) || base64.StdEncoding.EncodeToString(entry.Record.Revocation.Ciphertext) != base64.StdEncoding.EncodeToString(oauth.Ciphertext) || entry.Record.Revocation.LeaseUntil != nil && entry.Record.Revocation.LeaseUntil.After(at) {
			return ErrMCPCatalogMonitorChanged
		}
		entry.Record = cloneRetirement(entry.Record)
		entry.Record.State = "installed"
		entry.Record.Revocation = api.MCPOAuthRetirementPublic(entry.Record).Revocation
		retirementEvent(&entry.Record, "installed", at)
		m.mcpOAuthRetirements[oauth.RetirementID] = entry
	}
	publicRetirementViews(after)
	failed = false
	return nil
}
func (s *PgStore) hydrateRetirements(ctx context.Context, db sqlc.DBTX, account string, root *api.MCPCatalogMonitorSnapshot) error {
	for role, view := range retirementViews(*root) {
		if view.OAuthLifecycle == nil || view.OAuthLifecycle.Revocation == nil {
			continue
		}
		data, err := sqlc.New().ReadMCPOAuthRetirement(ctx, db, sqlc.ReadMCPOAuthRetirementParams{ID: mustPgUUID(view.OAuthLifecycle.Revocation.ID), AccountID: mustPgUUID(account)})
		if errors.Is(mapErr(err), ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var r api.MCPOAuthRetirement
		if err = json.Unmarshal(data, &r); err != nil {
			return err
		}
		applyRetirementView(&view, r)
		setObserverView(root, role, view)
	}
	return nil
}
func (m *MemStore) hydrateRetirementsLocked(account string, root *api.MCPCatalogMonitorSnapshot) {
	for role, view := range retirementViews(*root) {
		if view.OAuthLifecycle == nil || view.OAuthLifecycle.Revocation == nil {
			continue
		}
		entry, ok := m.mcpOAuthRetirements[view.OAuthLifecycle.Revocation.ID]
		if ok && entry.AccountID == account {
			applyRetirementView(&view, entry.Record)
			setObserverView(root, role, view)
		}
	}
}
func applyRetirementView(view *api.MCPCatalogMonitorSnapshot, r api.MCPOAuthRetirement) {
	phase := api.MCPOAuthRetirementPublic(r).Revocation
	if phase == nil {
		return
	}
	if r.State == "held" || r.State == "expired" || r.State == "manually_acknowledged" {
		phase.State = "manual_required"
	}
	view.OAuthLifecycle = api.CatalogOAuthLifecyclePublic(view.OAuthLifecycle)
	view.OAuthLifecycle.Revocation = phase
	if r.Reason == "credential_changed" && r.State == "held" && view.OAuthLifecycle.CredentialState != "reconnect_required" {
		view.OAuthLifecycle.CredentialState = "retirement_held"
	}
}
func (m *MemStore) retireDeletedAppLocked(app App, at time.Time) error {
	for _, dep := range m.deployments {
		if dep.AppID == app.ID && m.recoveryProtectedDeploymentLocked(dep.ID) {
			return ErrConflict
		}
	}
	root, ok := m.mcpCatalogMonitors[app.ID]
	if !ok || m.mcpCatalogMonitorOwners[app.ID] != app.AccountID {
		return nil
	}
	root = cloneMCPCatalogMonitor(root)
	before := cloneMCPCatalogMonitor(root)
	for role, view := range retirementViews(root) {
		if view.Config.Observer == nil || view.Config.Observer.OAuth == nil || view.ObserverStatus == nil {
			continue
		}
		if view.ObserverStatus.OAuth != nil {
			phase := retirementPhase(view.Config.Observer.OAuth, view.ObserverStatus.OAuth, at)
			lifecycleEvent(&view, "disconnected", at)
			view.OAuthLifecycle.LocalState = "disconnected"
			view.OAuthLifecycle.Revocation = phase
		}
		status := *view.ObserverStatus
		status.OAuth = nil
		status.Renewal = nil
		status.OAuthFlow = nil
		status.LeaseID = ""
		status.LeaseUntil = nil
		view.ObserverStatus = &status
		view.Config.Revision = uuid.NewString()
		setObserverView(&root, role, view)
	}
	if err := m.syncRetirementsLocked(app.AccountID, app.ID, before, &root, at); err != nil {
		return err
	}
	for id, entry := range m.mcpOAuthRetirements {
		if entry.AccountID == app.AccountID && entry.Record.AppID == app.ID && entry.Record.CreatedAt.Equal(at) {
			entry.Record = cloneRetirement(entry.Record)
			entry.Record.Reason = "app_deleted"
			m.mcpOAuthRetirements[id] = entry
		}
	}
	m.mcpCatalogMonitors[app.ID] = root
	return nil
}
