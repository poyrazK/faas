package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"reflect"
	"time"
)

var ErrMCPCatalogMonitorChanged = errors.New("MCP catalog monitor configuration changed")

type MCPCatalogMonitorStore interface {
	GetMCPCatalogMonitor(context.Context, string, string) (api.MCPCatalogMonitorSnapshot, error)
	ConfigureMCPCatalogMonitor(context.Context, string, string, api.MCPCatalogMonitorConfig) (api.MCPCatalogMonitorSnapshot, error)
	RecordMCPCatalogObservation(context.Context, string, string, api.MCPCatalogReceipt) (api.MCPCatalogReceipt, error)
}

func (s *PgStore) GetMCPCatalogMonitor(ctx context.Context, account, app string) (api.MCPCatalogMonitorSnapshot, error) {
	data, err := sqlc.New().GetMCPCatalogMonitor(ctx, s.pool, sqlc.GetMCPCatalogMonitorParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
	var out api.MCPCatalogMonitorSnapshot
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(data, &out)
	if err == nil {
		err = s.hydrateRetirements(ctx, s.pool, account, &out)
	}
	return out, err
}
func (s *PgStore) writeMCPCatalogMonitor(ctx context.Context, account, app string, config *api.MCPCatalogMonitorConfig, receipt *api.MCPCatalogReceipt) (api.MCPCatalogMonitorSnapshot, error) {
	return s.editMCPCatalogMonitor(ctx, account, app, config != nil, func(out *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return mutateMCPCatalogMonitor(out, config, receipt, at)
	})
}
func (s *PgStore) editMCPCatalogMonitor(ctx context.Context, account, app string, create bool, edit func(*api.MCPCatalogMonitorSnapshot, time.Time) error) (api.MCPCatalogMonitorSnapshot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPCatalogMonitorSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize initial configuration too, using the owning app row lock.
	if _, err = sqlc.New().LockMCPCatalogMonitorApp(ctx, tx, sqlc.LockMCPCatalogMonitorAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPCatalogMonitorSnapshot{}, mapErr(err)
	}
	data, err := sqlc.New().LockMCPCatalogMonitor(ctx, tx, sqlc.LockMCPCatalogMonitorParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
	out := api.MCPCatalogMonitorSnapshot{History: []api.MCPCatalogReceipt{}}
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return out, err
	}
	if err == nil {
		if err = json.Unmarshal(data, &out); err != nil {
			return out, err
		}
	} else if !create {
		return out, ErrNotFound
	}
	if err = s.hydrateRetirements(ctx, tx, account, &out); err != nil {
		return out, err
	}
	before := cloneMCPCatalogMonitor(out)
	at := time.Now().UTC()
	if err = edit(&out, at); err != nil {
		return out, err
	}
	if err = s.syncRetirements(ctx, tx, account, app, before, &out, at); err != nil {
		return out, err
	}
	data, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	rows, err := sqlc.New().PutMCPCatalogMonitor(ctx, tx, sqlc.PutMCPCatalogMonitorParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account), Payload: data})
	if err != nil {
		return out, err
	}
	if rows != 1 {
		return out, ErrNotFound
	}
	err = tx.Commit(ctx)
	return out, err
}
func (s *PgStore) ConfigureMCPCatalogMonitor(ctx context.Context, account, app string, config api.MCPCatalogMonitorConfig) (api.MCPCatalogMonitorSnapshot, error) {
	out, err := s.writeMCPCatalogMonitor(ctx, account, app, &config, nil)
	view, _ := api.CatalogMonitorRoleSnapshot(out, config.Role)
	return view, err
}
func (s *PgStore) RecordMCPCatalogObservation(ctx context.Context, account, app string, receipt api.MCPCatalogReceipt) (api.MCPCatalogReceipt, error) {
	out, err := s.writeMCPCatalogMonitor(ctx, account, app, nil, &receipt)
	if err != nil {
		return api.MCPCatalogReceipt{}, err
	}
	view, _ := api.CatalogMonitorRoleSnapshot(out, receipt.Role)
	return *view.Latest, nil
}
func mutateMCPCatalogMonitor(out *api.MCPCatalogMonitorSnapshot, config *api.MCPCatalogMonitorConfig, receipt *api.MCPCatalogReceipt, at time.Time) error {
	role := ""
	if config != nil {
		role = config.Role
	} else if receipt != nil {
		role = receipt.Role
	} else {
		return ErrInvalidArgument
	}
	if receipt != nil {
		view, exists := api.CatalogMonitorRoleSnapshot(*out, role)
		if exists && view.Config.Observer != nil && view.Config.Observer.Enabled {
			return ErrMCPCatalogMonitorChanged
		}
	}
	if !api.ValidCatalogMonitorRole(role) || (config != nil && !api.ValidCatalogMonitorTokenEnv(config.TokenEnv)) {
		return ErrInvalidArgument
	}
	role = api.CatalogMonitorRole(role)
	if config != nil {
		if api.ValidateScope(api.MCPConnectionScope(config.Scope)) != nil {
			return ErrInvalidArgument
		}
		prior, exists := api.CatalogMonitorRoleSnapshot(*out, role)
		// A role is a durable connection identity. Moving one could reuse another
		// environment's grant, so create a new role when changing environments.
		if exists && prior.Config.Revision != "" && api.MCPConnectionScope(prior.Config.Scope) != api.MCPConnectionScope(config.Scope) {
			return ErrInvalidArgument
		}
	}
	if config != nil && config.Observer != nil && config.Observer.OAuth != nil {
		views := map[string]api.MCPCatalogMonitorSnapshot{"default": *out}
		for name, view := range out.Roles {
			views[name] = view
		}
		for name, view := range views {
			if name != role && api.MCPConnectionScope(view.Config.Scope) == api.MCPConnectionScope(config.Scope) && view.Config.Observer != nil && view.Config.Observer.OAuth != nil && config.Observer.OAuth.RefreshTokenSecretRef != "" && view.Config.Observer.OAuth.RefreshTokenSecretRef == config.Observer.OAuth.RefreshTokenSecretRef {
				return ErrInvalidArgument
			}
		}
	}
	if role != "default" {
		child, exists := out.Roles[role]
		if !exists {
			if config == nil {
				return ErrNotFound
			}
			count := len(out.Roles)
			if out.Config.Revision != "" {
				count++
			}
			if count >= api.MCPCatalogMonitorRoleLimit {
				return ErrInvalidArgument
			}
			child.History = []api.MCPCatalogReceipt{}
		}
		if err := mutateMCPCatalogMonitorRole(&child, config, receipt, at); err != nil {
			return err
		}
		if out.Roles == nil {
			out.Roles = map[string]api.MCPCatalogMonitorSnapshot{}
		}
		out.Roles[role] = child
		return nil
	}
	if config != nil && out.Config.Revision == "" && len(out.Roles) >= api.MCPCatalogMonitorRoleLimit {
		return ErrInvalidArgument
	}
	return mutateMCPCatalogMonitorRole(out, config, receipt, at)
}
func mutateMCPCatalogMonitorRole(out *api.MCPCatalogMonitorSnapshot, config *api.MCPCatalogMonitorConfig, receipt *api.MCPCatalogReceipt, at time.Time) error {
	if config != nil {
		var renewal *api.MCPCatalogOAuthRenewal
		var oauth *api.MCPCatalogObserverOAuthStatus
		if out.Config.Observer != nil && config.Observer != nil && out.Config.Observer.OAuth != nil && config.Observer.OAuth != nil && reflect.DeepEqual(out.Config.Observer.OAuth, config.Observer.OAuth) && out.ObserverStatus != nil {
			oauth = out.ObserverStatus.OAuth
			renewal = out.ObserverStatus.Renewal
		}
		if oauth == nil && out.ObserverStatus != nil && out.ObserverStatus.OAuth != nil {
			out.OAuthLifecycle = api.CatalogOAuthLifecyclePublic(out.OAuthLifecycle)
			phase := retirementPhase(out.Config.Observer.OAuth, out.ObserverStatus.OAuth, at)
			lifecycleEvent(out, "credentials_discarded", at)
			out.OAuthLifecycle.Revocation = phase
			out.OAuthLifecycle.LocalState = "disconnected"
		}
		out.Config = *config
		out.ObserverStatus = nil
		if oauth != nil || renewal != nil {
			out.ObserverStatus = &api.MCPCatalogObserverStatus{State: "scheduled", OAuth: oauth, Renewal: renewal}
		}
		out.Latest = nil
		out.CheckedAt = nil
		out.State = ""
		return nil
	}
	if !out.Config.Enabled || receipt.Revision != out.Config.Revision {
		return ErrMCPCatalogMonitorChanged
	}
	if receipt.State != "healthy" && receipt.State != "drift" && receipt.State != "unavailable" {
		return ErrInvalidArgument
	}
	out.CheckedAt = &at
	out.State = ""
	if out.Latest != nil && out.Latest.State == receipt.State && out.Latest.ObservedSHA256 == receipt.ObservedSHA256 {
		return nil
	}
	receipt.CapturedAt = at
	out.Latest = receipt
	out.History = append([]api.MCPCatalogReceipt{*receipt}, out.History...)
	if len(out.History) > api.MCPCatalogMonitorHistoryLimit {
		out.History = out.History[:api.MCPCatalogMonitorHistoryLimit]
	}
	return nil
}
func cloneMCPCatalogMonitor(in api.MCPCatalogMonitorSnapshot) api.MCPCatalogMonitorSnapshot {
	data, _ := json.Marshal(in)
	var out api.MCPCatalogMonitorSnapshot
	_ = json.Unmarshal(data, &out)
	return out
}
func (m *MemStore) catalogMonitorAppLocked(account, app string) bool {
	a, ok := m.apps[app]
	return ok && a.AccountID == account
}
func (m *MemStore) GetMCPCatalogMonitor(_ context.Context, account, app string) (api.MCPCatalogMonitorSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.mcpCatalogMonitors[app]
	if !ok || !m.catalogMonitorAppLocked(account, app) || m.mcpCatalogMonitorOwners[app] != account {
		return api.MCPCatalogMonitorSnapshot{}, ErrNotFound
	}
	out = cloneMCPCatalogMonitor(out)
	m.hydrateRetirementsLocked(account, &out)
	return out, nil
}
func (m *MemStore) writeMCPCatalogMonitor(account, app string, config *api.MCPCatalogMonitorConfig, receipt *api.MCPCatalogReceipt) (api.MCPCatalogMonitorSnapshot, error) {
	return m.editMCPCatalogMonitor(account, app, config != nil, func(out *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return mutateMCPCatalogMonitor(out, config, receipt, at)
	})
}
func (m *MemStore) editMCPCatalogMonitor(account, app string, create bool, edit func(*api.MCPCatalogMonitorSnapshot, time.Time) error) (api.MCPCatalogMonitorSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.catalogMonitorAppLocked(account, app) {
		return api.MCPCatalogMonitorSnapshot{}, ErrNotFound
	}
	if m.mcpCatalogMonitors == nil {
		m.mcpCatalogMonitors = map[string]api.MCPCatalogMonitorSnapshot{}
	}
	out, ok := m.mcpCatalogMonitors[app]
	if m.mcpCatalogMonitorOwners[app] != account {
		out = api.MCPCatalogMonitorSnapshot{}
		ok = false
	}
	out = cloneMCPCatalogMonitor(out)
	if !ok {
		if !create {
			return out, ErrNotFound
		}
		out.History = []api.MCPCatalogReceipt{}
	}
	m.hydrateRetirementsLocked(account, &out)
	before := cloneMCPCatalogMonitor(out)
	at := time.Now().UTC()
	if err := edit(&out, at); err != nil {
		return out, err
	}
	if err := m.syncRetirementsLocked(account, app, before, &out, at); err != nil {
		return out, err
	}
	if m.mcpCatalogMonitorOwners == nil {
		m.mcpCatalogMonitorOwners = map[string]string{}
	}
	m.mcpCatalogMonitorOwners[app] = account
	m.mcpCatalogMonitors[app] = cloneMCPCatalogMonitor(out)
	return cloneMCPCatalogMonitor(out), nil
}
func (m *MemStore) ConfigureMCPCatalogMonitor(_ context.Context, account, app string, config api.MCPCatalogMonitorConfig) (api.MCPCatalogMonitorSnapshot, error) {
	out, err := m.writeMCPCatalogMonitor(account, app, &config, nil)
	view, _ := api.CatalogMonitorRoleSnapshot(out, config.Role)
	return view, err
}
func (m *MemStore) RecordMCPCatalogObservation(_ context.Context, account, app string, receipt api.MCPCatalogReceipt) (api.MCPCatalogReceipt, error) {
	out, err := m.writeMCPCatalogMonitor(account, app, nil, &receipt)
	if err != nil {
		return api.MCPCatalogReceipt{}, err
	}
	view, _ := api.CatalogMonitorRoleSnapshot(out, receipt.Role)
	return *view.Latest, nil
}
