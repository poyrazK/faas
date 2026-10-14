// Projection helpers for the operator observability backend
// (issue #777 / ADR-091).
//
// Every function in this file builds a wire-shape value from
// the state.Store. The handlers in handlers_admin_obs.go are
// intentionally thin (≤ 50 lines each, CLAUDE.md "Handlers ≤
// 50 lines — extract") and delegate the projection to the
// helpers here so the sensitive-field omissions are in one
// place. A future contributor adding a field to a wire DTO is
// forced to think about which state fields never belong on the
// wire (the grep tests in
// handlers_admin_obs_security_test.go pin every omission).
//
// Sensitive fields NEVER projected (ADR-091 §"Sensitive fields
// (never exposed)"):
//
//   - accounts.mfa_secret_encrypted, mfa_recovery_codes_hash
//   - account_passwords.hash
//   - api_keys.key_sha256 (fingerprint only — see projectAPIKeys below)
//   - sessions.binding_hash, sessions.issued_ip
//   - login_tokens.token_hash, cli_auth_codes.token_hash,
//     org_invitations.token_hash
//   - app_secrets.ciphertext, app_envs.value
//   - app_registry_credentials.password_encrypted
//   - app_webhooks.webhook_secret_sealed,
//     alert_rules.webhook_secret_sealed
//   - instances.netns, guest_uid, host_ip, lease_token
//   - invoices.raw (provider payload)
//   - accounts.email (only via ?include_pii=1 with audit row)
//   - orgs.provider_customer_id
//
// The helpers in this file only read fields outside that set.
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type obsOverviewSnapshot struct {
	accounts           []state.Account
	apps               []state.App
	deployments        []state.Deployment
	instances          []state.Instance
	nodes              []state.ComputeNode
	firstSuccessByAcct map[string]time.Time
	activity           state.OperatorOverviewActivity
	rateLimited        []api.ObsOverviewRateLimited
}

func loadObsOverviewSnapshot(ctx context.Context, store state.Store, now time.Time) (obsOverviewSnapshot, error) {
	var snap obsOverviewSnapshot
	var err error
	if snap.accounts, err = store.ListAllAccounts(ctx); err != nil {
		return snap, fmt.Errorf("list accounts: %w", err)
	}
	if snap.apps, err = store.ListAllApps(ctx); err != nil {
		return snap, fmt.Errorf("list apps: %w", err)
	}
	if snap.deployments, err = store.ListAllDeployments(ctx); err != nil {
		return snap, fmt.Errorf("list deployments: %w", err)
	}
	if snap.instances, err = store.ListAllInstances(ctx); err != nil {
		return snap, fmt.Errorf("list live instances: %w", err)
	}
	if snap.nodes, err = store.ListComputeNodes(ctx, true); err != nil {
		return snap, fmt.Errorf("list compute nodes: %w", err)
	}
	snap.firstSuccessByAcct, err = loadFirstSuccessfulRequests(ctx, store, now)
	if err != nil {
		return snap, err
	}
	snap.activity, err = store.OperatorOverviewActivity(ctx, now.Add(-api.ObsOverviewAuditWindow), now.Add(-api.ObsOverviewFailureWindow))
	if err != nil {
		return snap, err
	}
	rows, err := store.PerAccountRateLimitAggregate(ctx, sqlc.PerAccountRateLimitAggregateParams{At: pgtype.Timestamptz{Time: now.Add(-api.ObsOverviewAuditWindow), Valid: true}, Column2: api.ObsOverviewTopN})
	if err != nil {
		return snap, err
	}
	snap.rateLimited = make([]api.ObsOverviewRateLimited, 0, len(rows))
	for _, row := range rows {
		snap.rateLimited = append(snap.rateLimited, api.ObsOverviewRateLimited{AccountID: uuidString(row.AccountID), Hits: int(row.Hits)})
	}
	return snap, err
}

func loadFirstSuccessfulRequests(ctx context.Context, store state.Store, now time.Time) (map[string]time.Time, error) {
	first := make(map[string]time.Time)
	rows, err := store.ListFirstSuccessfulRequestsForAccountsCreatedSince(ctx, now.Add(-14*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("list beta first-success times: %w", err)
	}
	for _, row := range rows {
		first[row.AccountID] = row.At
	}
	return first, nil
}

// summariseAccounts reduces the canonical accounts slice to the
// five headline counts on the overview. PII is NEVER projected
// here — the per-account view is on the tenants endpoint, not
// the overview.
func summariseAccounts(rows []state.Account) api.ObsOverviewTotals {
	var t api.ObsOverviewTotals
	for _, a := range rows {
		switch a.Status {
		case state.AccountActive:
			t.AccountsActive++
		case state.AccountPastDue:
			t.AccountsPastDue++
		case state.AccountSuspended:
			t.AccountsSuspended++
		}
	}
	return t
}

func summariseBetaFunnel(now time.Time, accounts []state.Account, apps []state.App, deployments []state.Deployment, firstSuccess map[string]time.Time) api.ObsBetaFunnel {
	out := api.ObsBetaFunnel{WindowStartedAt: now.Add(-14 * 24 * time.Hour)}
	cohort := make(map[string]state.Account)
	verified := make(map[string]bool)
	for _, account := range accounts {
		if account.CreatedAt.Before(out.WindowStartedAt) || account.CreatedAt.After(now) {
			continue
		}
		cohort[account.ID] = account
		out.AccountsCreated++
		if account.EmailVerifiedAt != nil && !account.EmailVerifiedAt.After(now) {
			verified[account.ID] = true
			out.EmailVerified++
		}
	}
	appAccounts := make(map[string]bool)
	appOwners := make(map[string]string)
	for _, app := range apps {
		appOwners[app.ID] = app.AccountID
		if verified[app.AccountID] {
			appAccounts[app.AccountID] = true
		}
	}
	out.WithApp = len(appAccounts)
	liveAccounts := make(map[string]bool)
	for _, deployment := range deployments {
		accountID := appOwners[deployment.AppID]
		if deployment.Status == state.DeployLive && appAccounts[accountID] {
			liveAccounts[accountID] = true
		}
	}
	out.WithLiveDeployment = len(liveAccounts)
	latencies := make([]int64, 0, len(liveAccounts))
	for accountID := range liveAccounts {
		seen, ok := firstSuccess[accountID]
		if !ok {
			continue
		}
		out.WithSuccessfulRequest++
		latencies = append(latencies, int64(seen.Sub(cohort[accountID].CreatedAt).Seconds()))
	}
	out.SignupToFirstSuccessSamples = len(latencies)
	out.SignupToFirstSuccessP50Seconds = nearestRankSeconds(latencies, 0.50)
	out.SignupToFirstSuccessP95Seconds = nearestRankSeconds(latencies, 0.95)
	return out
}

func nearestRankSeconds(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := int(math.Ceil(float64(len(values)) * percentile))
	if rank < 1 {
		rank = 1
	}
	return values[rank-1]
}

// summariseInstances counts live + waking instances across the
// fleet. The state strings are written by schedd (CLAUDE.md
// ownership rule) and the obs read is a snapshot — the values
// reflect the schedd state at the moment of the SELECT.
func summariseInstances(rows []state.Instance) (live, waking int) {
	for _, i := range rows {
		switch instanceState := state.State(strings.ToLower(i.State)); instanceState {
		case state.StateRunning:
			live++
		case state.StateWaking, state.StateColdBooting:
			waking++
		}
	}
	return live, waking
}

// summariseNodes counts active vs inactive compute nodes. The
// Store.ListComputeNodes(includeInactive=true) call already
// returns both; the helper only tallies.
func summariseNodes(rows []state.ComputeNode) (active, inactive int) {
	for _, n := range rows {
		if n.Active {
			active++
		} else {
			inactive++
		}
	}
	return active, inactive
}

// toNodeHealthRows projects state.ComputeNode to the operator
// overview's per-node tile. Stale is a heuristic the frontend
// can render as a yellow chip without a second round-trip; the
// threshold (60s) matches schedd's watchdog cycle so a node
// the watchdog already considers stale flips the flag.
func toNodeHealthRows(rows []state.ComputeNode, now time.Time) []api.ObsOverviewNodeHealth {
	out := make([]api.ObsOverviewNodeHealth, 0, len(rows))
	for _, n := range rows {
		stale := false
		if !n.LastHeartbeatAt.IsZero() && now.Sub(n.LastHeartbeatAt) > 60*time.Second {
			stale = true
		}
		out = append(out, api.ObsOverviewNodeHealth{
			Name:            n.Name,
			Active:          n.Active,
			LastHeartbeatAt: n.LastHeartbeatAt,
			Stale:           stale,
		})
	}
	return out
}

// summariseRecentFailures preserves the top failure kinds and folds the rest
// into an other bucket without dropping their counts.
func summariseRecentFailures(rows []api.ObsOverviewFailureKind) []api.ObsOverviewFailureKind {
	out := make([]api.ObsOverviewFailureKind, 0, api.ObsOverviewTopN+1)
	other := 0
	for i, row := range rows {
		if i < api.ObsOverviewTopN {
			out = append(out, row)
		} else {
			other += row.Count
		}
	}
	if other > 0 {
		out = append(out, api.ObsOverviewFailureKind{Kind: "other", Count: other})
	}
	return out
}

// filterTenantRows applies the ?plan= and ?status= query
// filters to the canonical accounts slice. Mismatched values
// return an empty result rather than a 400 — the operator UI
// renders a "no matches" state, not a form error, and a typo
// in the dashboard input doesn't surface as a server error.
func filterTenantRows(r *http.Request, rows []state.Account) []state.Account {
	q := r.URL.Query()
	plan := strings.TrimSpace(strings.ToLower(q.Get("plan")))
	status := strings.TrimSpace(strings.ToLower(q.Get("status")))
	if plan == "" && status == "" {
		return rows
	}
	out := make([]state.Account, 0, len(rows))
	for _, a := range rows {
		if plan != "" && strings.ToLower(string(a.Plan)) != plan {
			continue
		}
		if status != "" && strings.ToLower(string(a.Status)) != status {
			continue
		}
		out = append(out, a)
	}
	return out
}

// paginateTenantRows is a simple cursor-by-CreatedAt-Desc
// paginator. The cursor is a RFC 3339 timestamp; the response
// renders the last row's CreatedAt as the next cursor. The
// shape matches the customer-side /v1/audit-log paginator
// (cmd/apid/handlers_audit_log.go) so a frontend building
// against either endpoint sees one cursor convention.
func paginateTenantRows(rows []state.Account, cursor string, limit int) ([]state.Account, string) {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	start := 0
	if cursor != "" {
		if t, err := time.Parse(time.RFC3339, cursor); err == nil {
			for i, a := range rows {
				if a.CreatedAt.Before(t) {
					start = i
					break
				}
				start = i + 1
			}
		}
	}
	end := start + limit
	next := ""
	if end < len(rows) {
		next = rows[end-1].CreatedAt.UTC().Format(time.RFC3339)
	}
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], next
}

// projectTenantList builds the per-tenant row. Email is the
// empty string unless includePII is true; the projection
// helper never reads mfa_secret_encrypted, the password hash,
// or any other sealed blob.
func projectTenantList(ctx context.Context, st state.Store, rows []state.Account, includePII bool) ([]api.ObsTenantRow, error) {
	out := make([]api.ObsTenantRow, 0, len(rows))
	for _, a := range rows {
		row, err := projectTenantRow(ctx, st, a, includePII)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// projectTenantRow projects one account. OrgSlug resolves
// from the personal org (issue #190 / ADR-061) and the call is
// best-effort: a missing personal-org row (pre-ADR-061 era) is
// not a 500 — the field is simply empty.
func projectTenantRow(ctx context.Context, st state.Store, a state.Account, includePII bool) (api.ObsTenantRow, error) {
	row := api.ObsTenantRow{
		AccountID:   a.ID,
		Plan:        string(a.Plan),
		Status:      string(a.Status),
		IsPersonal:  true, // updated below if the account has an org
		CreatedAt:   a.CreatedAt,
		MFAEnrolled: a.MFAEnrolled(),
	}
	if st != nil {
		if org, err := st.OrgByPersonalAccount(ctx, a.ID); err == nil {
			row.OrgSlug = org.Slug
			row.IsPersonal = org.Personal
		} else if !errors.Is(err, state.ErrNotFound) {
			return row, err
		}
		var err error
		row.AppsCount, row.DeploymentsLiveCount, err = st.OperatorTenantCounts(ctx, a.ID)
		if err != nil {
			return row, err
		}
	}
	if includePII {
		row.Email = a.Email
	}
	return row, nil
}

// buildTenantDetail assembles the per-tenant drawer. Mirrors
// projectTenantRow + adds apps, orgs, api-key counts, and
// session counts. Pre-aggregated so the frontend renders the
// drawer without a second round-trip.
func buildTenantDetail(ctx context.Context, st state.Store, a state.Account, includePII bool) (api.ObsTenantDetailResponse, error) {
	row, err := projectTenantRow(ctx, st, a, includePII)
	if err != nil {
		return api.ObsTenantDetailResponse{}, err
	}
	apps, err := st.ListApps(ctx, a.ID)
	if err != nil {
		return api.ObsTenantDetailResponse{}, err
	}
	appRows := make([]api.ObsTenantApp, 0, len(apps))
	for _, app := range apps {
		// ListDeploymentsForApp is paginated; the operator's
		// per-tenant view renders the live count, so a single
		// over-read of 1000 is fine (the customer cap is far
		// lower; the over-read is bounded by the per-tenant
		// app count).
		deps, err := st.ListDeploymentsForApp(ctx, app.ID, 1000, 0)
		if err != nil {
			return api.ObsTenantDetailResponse{}, err
		}
		live := 0
		for _, d := range deps {
			if d.Status == state.DeployLive {
				live++
			}
		}
		appRows = append(appRows, api.ObsTenantApp{
			ID:          app.ID,
			Slug:        app.Slug,
			Status:      string(app.Status),
			Deployments: live,
		})
	}
	orgs, err := st.ListOrgsForAccount(ctx, a.ID)
	if err != nil {
		return api.ObsTenantDetailResponse{}, err
	}
	orgRows := make([]api.ObsTenantOrg, 0, len(orgs))
	for _, o := range orgs {
		mem, err := st.OrgMemberByAccount(ctx, o.ID, a.ID)
		if err != nil {
			return api.ObsTenantDetailResponse{}, err
		}
		orgRows = append(orgRows, api.ObsTenantOrg{
			ID:   o.ID,
			Slug: o.Slug,
			Role: string(mem.Role),
		})
	}
	keys, err := st.ListAPIKeys(ctx, a.ID)
	if err != nil {
		return api.ObsTenantDetailResponse{}, err
	}
	apiCounts := api.ObsTenantCounts{}
	for _, k := range keys {
		if k.RevokedAt != nil {
			apiCounts.Revoked++
		} else {
			apiCounts.Active++
		}
	}
	sessions, err := st.ListSessions(ctx, a.ID)
	if err != nil {
		return api.ObsTenantDetailResponse{}, err
	}
	sessionCounts := api.ObsTenantCounts{}
	for _, sess := range sessions {
		if sess.RevokedAt != nil {
			sessionCounts.Revoked++
		} else {
			sessionCounts.Active++
		}
	}
	return api.ObsTenantDetailResponse{
		Account:  row,
		Apps:     appRows,
		Orgs:     orgRows,
		APIKeys:  apiCounts,
		Sessions: sessionCounts,
	}, nil
}

// paginateNodes applies the offset cursor to the stable active/name ordering.
// The list is small (single-digit count in production) and the
// response always returns the full set up to the limit; the
// cursor is the empty string when end == len. The shape stays
// consistent with the rest of the obs surface so the frontend
// has one cursor contract.
//
// PR #4 (ADR-092) adds the per-node live-utilization fold:
// liveStats is the PerNodeLiveStats aggregate (one row per node
// that has at least one live instance) and hbStats is the
// LatestHeartbeatStats aggregate (one row per node that has
// heartbeated). Both are keyed by compute_nodes.name so the
// fold is a map lookup, not a nested loop.
func paginateNodes(rows []state.ComputeNode, limit int, liveStats []state.PerNodeStats, hbStats []state.ComputeNodeHeartbeatStats, offsets ...int) ([]api.ObsNodeRow, string) {
	sort.Slice(rows, func(i, j int) bool {
		// active first, then name asc
		if rows[i].Active != rows[j].Active {
			return rows[i].Active
		}
		return rows[i].Name < rows[j].Name
	})
	// Index the aggregates by node name for O(1) fold. The list
	// is small (tens of nodes in the multi-host story) so the
	// map allocation is bounded.
	liveByName := make(map[string]state.PerNodeStats, len(liveStats))
	for _, l := range liveStats {
		liveByName[l.NodeName] = l
	}
	hbByName := make(map[string]state.ComputeNodeHeartbeatStats, len(hbStats))
	for _, h := range hbStats {
		hbByName[h.NodeID] = h
	}
	// Mirror of hbByName keyed by node name (instead of node id).
	// The hbStats carries the node uuid because that's what
	// LatestHeartbeatStats' DISTINCT ON (node_id) returns; the
	// liveStats carries the node name because that's what
	// PerNodeLiveStats' GROUP BY n.name returns. We resolve
	// node-id → node-name via the rows slice so the fold stays
	// a single map lookup per row.
	idToName := make(map[string]string, len(rows))
	for _, n := range rows {
		idToName[n.ID] = n.Name
	}
	hbByNameResolved := make(map[string]state.ComputeNodeHeartbeatStats, len(hbStats))
	for _, h := range hbStats {
		if name, ok := idToName[h.NodeID]; ok {
			hbByNameResolved[name] = h
		}
	}
	start := 0
	if len(offsets) > 0 {
		start = offsets[0]
	}
	if start < 0 || start >= len(rows) {
		return []api.ObsNodeRow{}, ""
	}
	end := start + limit
	next := ""
	if end > len(rows) {
		end = len(rows)
	}
	out := make([]api.ObsNodeRow, 0, end-start)
	for i := start; i < end; i++ {
		n := rows[i]
		row := api.ObsNodeRow{
			ID:                 n.ID,
			Name:               n.Name,
			Active:             n.Active,
			VPCPUs:             n.VPCPUs,
			MemMB:              n.MemMB,
			MaxConcurrency:     n.MaxConcurrency,
			AdmissionCeilingMB: n.AdmissionCeilingMB,
			LastHeartbeatAt:    n.LastHeartbeatAt,
			CreatedAt:          n.CreatedAt,
		}
		// Fold the live stats if any. Nodes with no live
		// instances get a zero-valued fold (no per-state
		// counters) — the operator UI renders "0 live" rather
		// than hiding the tile.
		if l, ok := liveByName[n.Name]; ok {
			row.InstancesLive = l.InstancesLive
			row.InstancesRunning = l.InstancesRunning
			row.InstancesWaking = l.InstancesWaking
			row.InstancesColdBooting = l.InstancesColdBooting
			row.RAMUsedMB = l.RAMUsedMB
			// §6.2 invariant #2 derivative: headroom until
			// the per-node admission ceiling fires.
			row.AdmissionMarginMB = int64(n.AdmissionCeilingMB) - l.RAMUsedMB
		}
		// Fold the latest heartbeat's CPU%/disk if any.
		// nil pointers → omitempty in the JSON tag renders
		// as "—" on the operator UI.
		if h, ok := hbByNameResolved[n.Name]; ok {
			row.CPUPct60s = h.CPUPct60s
			row.DiskUsedBytes = h.DiskUsedBytes
		}
		out = append(out, row)
	}
	if end < len(rows) {
		next = strconv.Itoa(end)
	}
	return out, next
}

// toHeartbeatRows projects state.ComputeNodeHeartbeat to the
// wire shape. GapToPreviousMs is computed against the prior
// row (the list is server-side ordered newest-first per the
// existing /v1/compute-nodes/{name}/heartbeats handler, so
// the gap is the millisecond delta to the next-newer row).
//
// state.ComputeNodeHeartbeat does not carry a Missed / Stale
// field today (the existing
// /v1/compute-nodes/{name}/heartbeats handler derives them
// from gap math against the watchdog threshold). The wire DTO
// keeps the booleans so the operator UI can render a "missed"
// chip without re-deriving client-side; the values are
// computed here against a 60s threshold matching schedd's
// watchdog.
func toHeartbeatRows(hbs []state.ComputeNodeHeartbeat) []api.ObsHeartbeatRow {
	out := make([]api.ObsHeartbeatRow, 0, len(hbs))
	for i, h := range hbs {
		gap := int64(0)
		if i+1 < len(hbs) {
			gap = h.ReceivedAt.Sub(hbs[i+1].ReceivedAt).Milliseconds()
		}
		missed := gap > int64(60*time.Second/time.Millisecond)
		stale := !h.LastHeartbeatAt.IsZero() && time.Since(h.LastHeartbeatAt) > 60*time.Second
		out = append(out, api.ObsHeartbeatRow{
			ReceivedAt:      h.ReceivedAt,
			LastHeartbeatAt: h.LastHeartbeatAt,
			Source:          h.Source,
			GapToPreviousMs: gap,
			Missed:          missed,
			Stale:           stale,
		})
	}
	return out
}
