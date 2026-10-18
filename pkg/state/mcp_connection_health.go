package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"net/url"
	"sort"
	"strings"
	"time"
)

type MCPConnectionHealthStore interface {
	MCPConnectionHealth(context.Context, string, string, int, time.Time) (api.MCPConnectionHealthSnapshot, error)
}
type mcpHealthRow struct {
	Scope            string     `json:"scope,omitempty"`
	ObserverState    string     `json:"observer_state"`
	AppID            string     `json:"app_id"`
	AppSlug          string     `json:"app_slug"`
	Role             string     `json:"role"`
	OAuthState       string     `json:"oauth_state"`
	CredentialState  string     `json:"credential_state"`
	Guided           bool       `json:"guided"`
	Enabled          bool       `json:"enabled"`
	Paused           bool       `json:"paused"`
	JournalState     string     `json:"journal_state"`
	JournalOutcome   string     `json:"journal_outcome"`
	ExpiresAt        *time.Time `json:"expires_at"`
	LeaseUntil       *time.Time `json:"lease_until"`
	FlowExpiresAt    *time.Time `json:"flow_expires_at"`
	FailureReason    string     `json:"failure_reason"`
	RetirementID     string     `json:"retirement_id"`
	RetirementState  string     `json:"retirement_state"`
	RetirementReason string     `json:"retirement_reason"`
	RetainUntil      *time.Time `json:"retain_until"`
	NextAttemptAt    *time.Time `json:"next_attempt_at"`
	AppDeleted       bool       `json:"app_deleted"`
}

func healthIssues(row mcpHealthRow, at time.Time) []api.MCPConnectionHealthIssue {
	out := []api.MCPConnectionHealthIssue{}
	add := func(kind, state, action string, expiry *time.Time) {
		if state == "" {
			state = "unavailable"
		}
		id := row.AppID + "/" + row.Role + "/" + kind
		path := "/v1/apps/" + url.PathEscape(row.AppSlug) + "/mcp-catalog-monitor?role=" + url.QueryEscape(row.Role)
		if row.RetirementID != "" {
			id = "retirement/" + row.RetirementID + "/" + kind
			path = "/v1/mcp-oauth-retirements"
		}
		out = append(out, api.MCPConnectionHealthIssue{ID: id, AppID: row.AppID, AppSlug: row.AppSlug, Scope: api.MCPConnectionScope(row.Scope), Role: row.Role, Kind: kind, State: state, AppDeleted: row.AppDeleted, RetirementID: row.RetirementID, ExpiresAt: expiry, RecoveryAction: action, RecoveryPath: path})
	}
	if row.RetirementID != "" {
		if row.RetirementReason == "issuance_unknown" || row.RetirementState == "awaiting_issuance" && row.NextAttemptAt != nil && !row.NextAttemptAt.After(at) {
			add("issuance_unknown", row.RetirementState, "review_provider", row.RetainUntil)
		}
		if row.RetainUntil != nil && (row.RetirementState == "expired" || !row.RetainUntil.After(at.Add(time.Duration(api.MCPConnectionRetirementWarningSeconds)*time.Second))) {
			add("retirement_expiring", row.RetirementState, "review_retirement", row.RetainUntil)
		}
		return out
	}
	unknown := row.JournalOutcome == "issuance_unknown" || row.JournalState == "pending" && (row.LeaseUntil == nil || !row.LeaseUntil.After(at))
	if unknown {
		action := "reconnect"
		if !row.Guided {
			action = "update_refresh_secret"
		}
		add("issuance_unknown", row.JournalState, action, row.ExpiresAt)
		return out
	}
	needed := row.OAuthState == "reauthorization_required" || row.CredentialState == "reconnect_required" || row.JournalState == "reauthorization_required"
	if row.Enabled && !row.Paused && row.Guided && row.OAuthState == "" && (row.FlowExpiresAt == nil || !row.FlowExpiresAt.After(at)) {
		needed = true
	}
	if needed {
		action := "reconnect"
		if !row.Guided {
			action = "update_refresh_secret"
		}
		if row.CredentialState == "reconnect_required" {
			action = "restore_credential"
		}
		add("reauthorization_required", row.OAuthState, action, row.ExpiresAt)
		return out
	}
	if row.Enabled && !row.Paused && (row.JournalState == "failed" || (row.FailureReason == "credential_unavailable" && row.ObserverState == "degraded") || row.OAuthState == "connected" && row.ExpiresAt != nil && !row.ExpiresAt.After(at)) {
		add("renewal_failed", row.JournalState, "inspect_connection", row.ExpiresAt)
	}
	return out
}
func connectionHealthSnapshot(rows []mcpHealthRow, complete bool, offset int, at time.Time) api.MCPConnectionHealthSnapshot {
	out := api.MCPConnectionHealthSnapshot{ObservedAt: at, Complete: complete, Counts: map[string]int{"reauthorization_required": 0, "renewal_failed": 0, "retirement_expiring": 0, "issuance_unknown": 0}, Items: []api.MCPConnectionHealthIssue{}, Fingerprints: map[string]string{}}
	issues := []api.MCPConnectionHealthIssue{}
	seen := map[string]bool{}
	for _, row := range rows {
		for _, issue := range healthIssues(row, at) {
			if !seen[issue.ID] {
				seen[issue.ID] = true
				issues = append(issues, issue)
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	ids := map[string][]string{}
	for _, issue := range issues {
		out.Counts[issue.Kind]++
		ids[issue.Kind] = append(ids[issue.Kind], issue.ID)
	}
	for kind := range out.Counts {
		sum := sha256.Sum256([]byte(strings.Join(ids[kind], "\n")))
		out.Fingerprints[kind] = hex.EncodeToString(sum[:])
	}
	out.TotalIssues = len(issues)
	if offset < len(issues) {
		end := min(offset+api.MCPConnectionHealthPageSize, len(issues))
		out.Items = issues[offset:end]
		if end < len(issues) && end <= api.MCPConnectionHealthMaxOffset {
			out.NextOffset = end
		}
	}
	return out
}
func (s *PgStore) MCPConnectionHealth(ctx context.Context, account, app string, offset int, at time.Time) (api.MCPConnectionHealthSnapshot, error) {
	if offset < 0 || offset > api.MCPConnectionHealthMaxOffset {
		return api.MCPConnectionHealthSnapshot{}, ErrInvalidArgument
	}
	// A repeatable-read transaction keeps bindings and the retirement ledger consistent.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPConnectionHealthSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return api.MCPConnectionHealthSnapshot{}, err
	}
	binding, err := sqlc.New().MCPConnectionHealthBindings(ctx, tx, sqlc.MCPConnectionHealthBindingsParams{AccountID: mustPgUUID(account), AppFilter: app, SourceLimit: int32(api.MCPConnectionHealthSourceLimit + 1)})
	if err != nil {
		return api.MCPConnectionHealthSnapshot{}, err
	}
	retired, err := sqlc.New().MCPConnectionHealthRetirements(ctx, tx, sqlc.MCPConnectionHealthRetirementsParams{AccountID: mustPgUUID(account), AppFilter: app, SourceLimit: int32(api.MCPConnectionHealthSourceLimit + 1)})
	if err != nil {
		return api.MCPConnectionHealthSnapshot{}, err
	}
	rows := []mcpHealthRow{}
	for _, group := range [][][]byte{binding, retired} {
		for _, data := range group {
			var row mcpHealthRow
			if err = json.Unmarshal(data, &row); err != nil {
				return api.MCPConnectionHealthSnapshot{}, err
			}
			rows = append(rows, row)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return api.MCPConnectionHealthSnapshot{}, err
	}
	complete := len(rows) <= api.MCPConnectionHealthSourceLimit
	rows = rows[:min(len(rows), api.MCPConnectionHealthSourceLimit)]
	return connectionHealthSnapshot(rows, complete, offset, at), nil
}
func (m *MemStore) MCPConnectionHealth(_ context.Context, account, app string, offset int, at time.Time) (api.MCPConnectionHealthSnapshot, error) {
	if offset < 0 || offset > api.MCPConnectionHealthMaxOffset {
		return api.MCPConnectionHealthSnapshot{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []mcpHealthRow{}
	for id, root := range m.mcpCatalogMonitors {
		a, ok := m.apps[id]
		if !ok || a.AccountID != account || a.Status == AppDeleted || m.mcpCatalogMonitorOwners[id] != account || app != "" && id != app {
			continue
		}
		for role, view := range retirementViews(root) {
			if view.Config.Observer == nil || view.Config.Observer.OAuth == nil {
				continue
			}
			cfg := view.Config.Observer
			row := mcpHealthRow{AppID: id, AppSlug: a.Slug, Scope: api.MCPConnectionScope(view.Config.Scope), Role: role, Guided: cfg.OAuth.Guided, Enabled: view.Config.Enabled && cfg.Enabled, Paused: cfg.Paused}
			if st := view.ObserverStatus; st != nil {
				row.ObserverState = st.State
				row.LeaseUntil = st.LeaseUntil
				row.FailureReason = st.FailureReason
				if st.OAuth != nil {
					row.OAuthState = st.OAuth.State
					row.ExpiresAt = st.OAuth.ExpiresAt
				}
				if st.Renewal != nil {
					row.JournalState = st.Renewal.State
					row.JournalOutcome = st.Renewal.Outcome
				}
				if st.OAuthFlow != nil {
					row.FlowExpiresAt = &st.OAuthFlow.ExpiresAt
				}
			}
			if view.OAuthLifecycle != nil {
				row.CredentialState = view.OAuthLifecycle.CredentialState
			}
			rows = append(rows, row)
		}
	}
	for _, entry := range m.mcpOAuthRetirements {
		r := entry.Record
		if entry.AccountID != account || app != "" && r.AppID != app {
			continue
		}
		switch r.State {
		case "awaiting_issuance", "pending", "held", "manual_required", "expired":
		default:
			continue
		}
		a, exists := m.apps[r.AppID]
		next := r.Revocation.NextAttemptAt
		rows = append(rows, mcpHealthRow{AppID: r.AppID, AppSlug: r.AppSlug, Scope: api.MCPConnectionScope(r.Scope), Role: r.Role, RetirementID: r.ID, RetirementState: r.State, RetirementReason: r.Reason, RetainUntil: &r.RetainUntil, NextAttemptAt: &next, AppDeleted: !exists || a.Status == AppDeleted})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].AppID+rows[i].Role+rows[i].RetirementID < rows[j].AppID+rows[j].Role+rows[j].RetirementID
	})
	complete := len(rows) <= api.MCPConnectionHealthSourceLimit
	rows = rows[:min(len(rows), api.MCPConnectionHealthSourceLimit)]
	return connectionHealthSnapshot(rows, complete, offset, at), nil
}
