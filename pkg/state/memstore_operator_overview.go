package state

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorOverview(ctx context.Context, now time.Time) (api.ObsOverviewResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.ObsOverviewResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now = now.UTC()
	out := api.ObsOverviewResponse{GeneratedAt: now, NodeHealth: []api.ObsOverviewNodeHealth{}}
	for _, account := range m.accounts {
		switch account.Status {
		case AccountActive:
			out.Totals.AccountsActive++
		case AccountPastDue:
			out.Totals.AccountsPastDue++
		case AccountSuspended:
			out.Totals.AccountsSuspended++
		}
	}
	for _, app := range m.apps {
		if app.Status != AppDeleted {
			out.Totals.AppsTotal++
		}
	}
	for _, instance := range m.instances {
		switch State(instance.State) {
		case StateRunning:
			out.Totals.InstancesLive++
		case StateWaking, StateColdBooting:
			out.Totals.InstancesWaking++
		}
	}
	for _, node := range m.computeNodes {
		if node.Active {
			out.Totals.NodesActive++
		} else {
			out.Totals.NodesInactive++
		}
		out.NodeHealth = append(out.NodeHealth, api.ObsOverviewNodeHealth{Name: node.Name, Active: node.Active, LastHeartbeatAt: node.LastHeartbeatAt,
			Stale: !node.LastHeartbeatAt.IsZero() && now.Sub(node.LastHeartbeatAt) > api.ObsOverviewNodeStaleAfter})
	}
	sort.Slice(out.NodeHealth, func(i, j int) bool { return out.NodeHealth[i].Name < out.NodeHealth[j].Name })
	out.Totals.OrgsTotal = len(m.orgs)
	for _, audit := range m.auditLog {
		if !audit.ReceivedAt.Before(now.Add(-api.ObsOverviewAuditWindow)) && !audit.ReceivedAt.After(now) {
			out.Totals.AuditEvents24h++
		}
	}
	out.BetaFunnel14d = m.operatorOverviewFunnelLocked(now)
	out.RecentFailures1h, out.TopRateLimitedAccounts24h = m.operatorOverviewEventsLocked(now)
	out.CodingAgents = m.operatorOverviewCodingAgentsLocked(now)
	return out, nil
}

// codingAgentDeployKinds are the deploy audit kinds apid stamps with
// actor_agent (mergeAgentAudit); OperatorOverviewCodingAgents lists the same.
var codingAgentDeployKinds = map[string]bool{
	"app.deployed": true, "deploy.local_tarball": true, "deploy.source_ref": true, "upload.session_committed": true,
}

// operatorOverviewCodingAgentsLocked mirrors OperatorOverviewCodingAgents.
func (m *MemStore) operatorOverviewCodingAgentsLocked(now time.Time) []api.ObsCodingAgent {
	auditSince, cohortSince := now.Add(-api.ObsOverviewAuditWindow), now.Add(-api.ObsOverviewCohortWindow)
	type agentRow struct{ deploys, accounts map[string]bool }
	rows := map[string]*agentRow{}
	row := func(agent string) *agentRow {
		if rows[agent] == nil {
			rows[agent] = &agentRow{deploys: map[string]bool{}, accounts: map[string]bool{}}
		}
		return rows[agent]
	}
	for _, entry := range m.auditLog {
		if !codingAgentDeployKinds[entry.Kind] || entry.ReceivedAt.After(now) {
			continue
		}
		var data struct {
			Agent        *string `json:"actor_agent"`
			DeploymentID string  `json:"deployment_id"`
		}
		if json.Unmarshal(entry.Data, &data) != nil || data.Agent == nil {
			continue
		}
		if !entry.ReceivedAt.Before(auditSince) {
			r := row(*data.Agent)
			if data.DeploymentID != "" {
				r.deploys[data.DeploymentID] = true
			}
		}
		if entry.AccountID == nil {
			continue
		}
		// MemStore account IDs are bare hex; audit rows carry the UUID form.
		account, found := m.accounts[entry.AccountID.String()]
		if !found {
			account, found = m.accounts[strings.ReplaceAll(entry.AccountID.String(), "-", "")]
		}
		if !found || account.CreatedAt.Before(cohortSince) || account.CreatedAt.After(now) || entry.ReceivedAt.Before(account.CreatedAt) {
			continue
		}
		row(*data.Agent).accounts[account.ID] = true
	}
	served := map[string]bool{}
	for _, instance := range m.instances {
		app, found := m.apps[instance.AppID]
		if !found {
			continue
		}
		account, found := m.accounts[app.AccountID]
		seen := instance.LastRequestAt
		if found && !seen.IsZero() && !seen.Before(account.CreatedAt) && !seen.After(now) {
			served[account.ID] = true
		}
	}
	out := make([]api.ObsCodingAgent, 0, len(rows))
	for agent, r := range rows {
		item := api.ObsCodingAgent{Agent: agent, Deploys24h: len(r.deploys), NewAccounts14d: len(r.accounts)}
		for id := range r.accounts {
			if served[id] {
				item.NewAccountsServing14d++
			}
			if m.accounts[id].Plan != api.PlanFree {
				item.NewAccountsPaid14d++
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NewAccounts14d != out[j].NewAccounts14d {
			return out[i].NewAccounts14d > out[j].NewAccounts14d
		}
		if out[i].Deploys24h != out[j].Deploys24h {
			return out[i].Deploys24h > out[j].Deploys24h
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

func (m *MemStore) operatorOverviewFunnelLocked(now time.Time) api.ObsBetaFunnel {
	out := api.ObsBetaFunnel{WindowStartedAt: now.Add(-api.ObsOverviewCohortWindow)}
	cohort := map[string]Account{}
	verified := map[string]bool{}
	for _, account := range m.accounts {
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
	withApp, live := map[string]bool{}, map[string]bool{}
	for _, app := range m.apps {
		if verified[app.AccountID] && app.Status != AppDeleted {
			withApp[app.AccountID] = true
		}
	}
	for _, deployment := range m.deployments {
		app, found := m.apps[deployment.AppID]
		if found && app.Status != AppDeleted && withApp[app.AccountID] && deployment.Status == DeployLive && deployment.DeletedAt == nil {
			live[app.AccountID] = true
		}
	}
	out.WithApp, out.WithLiveDeployment = len(withApp), len(live)
	first := map[string]time.Time{}
	for _, instance := range m.instances {
		app, found := m.apps[instance.AppID]
		if !found || !live[app.AccountID] {
			continue
		}
		seen := instance.LastRequestAt
		if seen.IsZero() || seen.Before(cohort[app.AccountID].CreatedAt) || seen.After(now) {
			continue
		}
		if previous, found := first[app.AccountID]; !found || seen.Before(previous) {
			first[app.AccountID] = seen
		}
	}
	latencies := make([]int64, 0, len(first))
	for id, seen := range first {
		latencies = append(latencies, int64(seen.Sub(cohort[id].CreatedAt).Seconds()))
	}
	out.WithSuccessfulRequest, out.SignupToFirstSuccessSamples = len(first), len(first)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		out.SignupToFirstSuccessP50Seconds = latencies[int(math.Ceil(float64(len(latencies))*0.50))-1]
		out.SignupToFirstSuccessP95Seconds = latencies[int(math.Ceil(float64(len(latencies))*0.95))-1]
	}
	return out
}

func (m *MemStore) operatorOverviewEventsLocked(now time.Time) ([]api.ObsOverviewFailureKind, []api.ObsOverviewRateLimited) {
	failures := map[string]int{}
	type accountHits struct {
		api.ObsOverviewRateLimited
		last time.Time
	}
	accounts := map[string]*accountHits{}
	for _, event := range m.events {
		if event.At.After(now) {
			continue
		}
		if !event.At.Before(now.Add(-api.ObsOverviewFailureWindow)) &&
			(strings.HasSuffix(event.Kind, ".failed") || strings.HasSuffix(event.Kind, ".failure") || strings.HasSuffix(event.Kind, ".error")) {
			failures[event.Kind]++
		}
		if event.Kind != "auth.rate_limited" || event.At.Before(now.Add(-api.ObsOverviewAuditWindow)) {
			continue
		}
		id := uuid.Nil.String()
		if event.Subject != nil {
			id = event.Subject.String()
		}
		row := accounts[id]
		if row == nil {
			row = &accountHits{ObsOverviewRateLimited: api.ObsOverviewRateLimited{AccountID: id}}
			accounts[id] = row
		}
		row.Hits++
		if event.At.After(row.last) {
			row.last = event.At
		}
	}
	failureRows := make([]api.ObsOverviewFailureKind, 0, len(failures))
	for kind, count := range failures {
		failureRows = append(failureRows, api.ObsOverviewFailureKind{Kind: kind, Count: count})
	}
	sort.Slice(failureRows, func(i, j int) bool {
		if failureRows[i].Count != failureRows[j].Count {
			return failureRows[i].Count > failureRows[j].Count
		}
		return failureRows[i].Kind < failureRows[j].Kind
	})
	if len(failureRows) > api.ObsOverviewTopN {
		other := 0
		for _, row := range failureRows[api.ObsOverviewTopN:] {
			other += row.Count
		}
		failureRows = append(failureRows[:api.ObsOverviewTopN], api.ObsOverviewFailureKind{Kind: "other", Count: other})
	}
	ranked := make([]accountHits, 0, len(accounts))
	for _, row := range accounts {
		ranked = append(ranked, *row)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Hits != ranked[j].Hits {
			return ranked[i].Hits > ranked[j].Hits
		}
		if !ranked[i].last.Equal(ranked[j].last) {
			return ranked[i].last.After(ranked[j].last)
		}
		return ranked[i].AccountID < ranked[j].AccountID
	})
	if len(ranked) > api.ObsOverviewTopN {
		ranked = ranked[:api.ObsOverviewTopN]
	}
	rateRows := make([]api.ObsOverviewRateLimited, 0, len(ranked))
	for _, row := range ranked {
		rateRows = append(rateRows, row.ObsOverviewRateLimited)
	}
	return failureRows, rateRows
}
