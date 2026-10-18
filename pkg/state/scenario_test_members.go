package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioTestMember is one app's logical service name in a single test run.
// Membership persists through soft deletion so no test app can fall back to
// a production service while its VM is being drained.
type ScenarioTestMember struct {
	SuspendResumeJSON string
	ColdStartJSON     string
	ManagedWorkJSON   string
	SimulationJSON    string
	AccountID         string
	RunID             string
	Workload          string
	AppID             string
	NetworkMode       string
	WebhookMode       string
}

// ScenarioTestDNSChaosPolicy is the verified caller identity and run lease
// used to fault one private service-name lookup.
type ScenarioTestDNSChaosPolicy struct {
	RunID       string
	CallerAppID string
	Lease       chaos.Lease
}

var scenarioTestRunPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var scenarioTestWorkloadPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)
var scenarioChaosRuleIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validScenarioTestMembers(accountID, runID string, members []ScenarioTestMember) error {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) || len(members) == 0 || len(members) > 16 {
		return ErrConflict
	}
	seenNames := make(map[string]bool, len(members))
	seenApps := make(map[string]bool, len(members))
	for _, member := range members {
		if !scenarioTestWorkloadPattern.MatchString(member.Workload) || member.AppID == "" || seenNames[member.Workload] || seenApps[member.AppID] ||
			(member.WebhookMode != "" && member.WebhookMode != ScenarioWebhookCaptureMode) || member.WebhookMode != members[0].WebhookMode ||
			(member.NetworkMode != "" && (member.NetworkMode != ScenarioNetworkContained || member.WebhookMode != ScenarioWebhookCaptureMode)) || member.NetworkMode != members[0].NetworkMode {
			return ErrConflict
		}
		if member.SuspendResumeJSON != members[0].SuspendResumeJSON || member.ColdStartJSON != members[0].ColdStartJSON || member.SimulationJSON != members[0].SimulationJSON || member.ManagedWorkJSON != members[0].ManagedWorkJSON {
			return ErrConflict
		}
		seenNames[member.Workload] = true
		seenApps[member.AppID] = true
	}
	if members[0].SimulationJSON != "" {
		if members[0].NetworkMode != ScenarioNetworkContained {
			return ErrConflict
		}
		plan, err := api.DecodeScenarioSimulationPlan(members[0].SimulationJSON)
		names := make([]string, 0, len(members))
		for _, member := range members {
			names = append(names, member.Workload)
		}
		body, _, err2 := api.NormalizeScenarioSimulationPlan(plan, names)
		if err != nil || err2 != nil || string(body) != members[0].SimulationJSON {
			return ErrConflict
		}
	}
	if members[0].ManagedWorkJSON != "" {
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Workload)
		}
		raw, _, err := rehearsalwork.Normalize(rehearsalwork.Decode(members[0].ManagedWorkJSON), names)
		if err != nil || string(raw) != members[0].ManagedWorkJSON || members[0].SimulationJSON == "" {
			return ErrConflict
		}
		simulation, decodeErr := api.DecodeScenarioSimulationPlan(members[0].SimulationJSON)
		if decodeErr != nil || api.ValidateScenarioWorkerTermination(rehearsalwork.Decode(members[0].ManagedWorkJSON), &simulation) != nil {
			return ErrConflict
		}
	}
	if members[0].ColdStartJSON != "" {
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Workload)
		}
		raw, _, err := rehearsalcold.Normalize(rehearsalcold.Decode(members[0].ColdStartJSON), names)
		if err != nil || string(raw) != members[0].ColdStartJSON || members[0].SimulationJSON == "" || rehearsalwork.Decode(members[0].ManagedWorkJSON).WorkerTermination != nil {
			return ErrConflict
		}
	}
	if members[0].SuspendResumeJSON != "" {
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Workload)
		}
		raw, _, err := rehearsalresume.Normalize(rehearsalresume.Decode(members[0].SuspendResumeJSON), names)
		if err != nil || string(raw) != members[0].SuspendResumeJSON || members[0].SimulationJSON == "" || members[0].ColdStartJSON != "" || rehearsalwork.Decode(members[0].ManagedWorkJSON).WorkerTermination != nil {
			return ErrConflict
		}
		var simulation api.ScenarioSimulationPlan
		plan := rehearsalwork.Decode(members[0].ManagedWorkJSON)
		if json.Unmarshal([]byte(members[0].SimulationJSON), &simulation) != nil || api.ValidateScenarioSuspendCheckpoint(rehearsalresume.Decode(members[0].SuspendResumeJSON), &plan, &simulation) != nil {
			return ErrConflict
		}

	}
	return nil
}

func eligibleScenarioTestApp(app App, accountID string) bool {
	return app.AccountID == accountID && app.PreviewOfSlug != "" && app.PreviewPrNumber == 0 &&
		app.Status != AppDeleted && app.PreviewPrState == PreviewPrStateOpen &&
		app.PreviewExpiresAt != nil && app.PreviewExpiresAt.After(time.Now())
}

func (s *PgStore) RegisterScenarioTestMembers(ctx context.Context, accountID, runID string, members []ScenarioTestMember) error {
	if err := validScenarioTestMembers(accountID, runID, members); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scenario test registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: accountID, RunID: runID}); err != nil {
		return fmt.Errorf("lock scenario run: %w", err)
	}
	allowed, err := q.ScenarioActionRunCanEnroll(ctx, tx, sqlc.ScenarioActionRunCanEnrollParams{AccountID: accountID, RunID: runID, Capture: members[0].WebhookMode == ScenarioWebhookCaptureMode})
	if err != nil {
		return fmt.Errorf("check scenario run enrollment: %w", err)
	}
	if !allowed.Valid || !allowed.Bool {
		return ErrConflict
	}
	members = append([]ScenarioTestMember(nil), members...)
	sort.Slice(members, func(i, j int) bool { return members[i].AppID < members[j].AppID })
	for _, member := range members {
		eligible, err := q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AppID: member.AppID, AccountID: accountID})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!eligible.Valid || !eligible.Bool)) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("validate scenario test app: %w", err)
		}
		if member.WebhookMode == ScenarioWebhookCaptureMode {
			unstarted, err := q.ScenarioActionAppUnstarted(ctx, tx, member.AppID)
			if err != nil {
				return err
			}
			if !unstarted.Valid || !unstarted.Bool {
				return ErrConflict
			}
			if err := q.InsertScenarioActionPolicy(ctx, tx, sqlc.InsertScenarioActionPolicyParams{AccountID: accountID, RunID: runID, AppID: member.AppID, Workload: member.Workload, NetworkMode: member.NetworkMode, SimulationPlan: scenarioSimulationJSON(member.SimulationJSON), ColdStartSpec: scenarioSimulationJSON(member.ColdStartJSON), SuspendResumeSpec: scenarioSimulationJSON(member.SuspendResumeJSON), ManagedWorkPlan: scenarioSimulationJSON(member.ManagedWorkJSON)}); err != nil {
				return mapErr(err)
			}
		}
		if err := q.InsertScenarioTestMember(ctx, tx, sqlc.InsertScenarioTestMemberParams{AccountID: accountID, RunID: runID, Workload: member.Workload, AppID: member.AppID}); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ScenarioTestMemberByApp(ctx context.Context, appID string) (ScenarioTestMember, error) {
	member, err := sqlc.New().ScenarioTestMemberWithPolicy(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScenarioTestMember{}, ErrNotFound
	}
	if err != nil {
		return ScenarioTestMember{}, fmt.Errorf("load scenario test member: %w", err)
	}
	return ScenarioTestMember{AccountID: member.AccountID, RunID: member.RunID, Workload: member.WorkloadName, AppID: member.AppID, WebhookMode: member.WebhookMode, NetworkMode: member.NetworkMode, SimulationJSON: scenarioSimulationText(member.SimulationPlan), ColdStartJSON: scenarioSimulationText(member.ColdStartSpec), SuspendResumeJSON: scenarioSimulationText(member.SuspendResumeSpec), ManagedWorkJSON: scenarioManagedWorkText(member.ManagedWorkPlan)}, nil
}

func (s *PgStore) ScenarioTestAppByWorkload(ctx context.Context, accountID, runID, workload string) (App, error) {
	row := s.pool.QueryRow(ctx, `select `+appsSelectColumns+` from apps
		where id = (select app_id from scenario_test_members
		where account_id = $1 and run_id = $2 and workload_name = $3)
		and status <> 'deleted' and preview_pr_state = 'open'
		and preview_expires_at > now()`, accountID, runID, workload)
	return scanApp(row)
}

func (m *MemStore) RegisterScenarioTestMembers(_ context.Context, accountID, runID string, members []ScenarioTestMember) error {
	if err := validScenarioTestMembers(accountID, runID, members); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, policy := range m.scenarioActionPolicies {
		if policy.Member.AccountID == accountID && policy.Member.RunID == runID {
			return ErrConflict
		}
	}
	if members[0].WebhookMode == ScenarioWebhookCaptureMode {
		for _, member := range m.scenarioTestMembers {
			if member.AccountID == accountID && member.RunID == runID {
				return ErrConflict
			}
		}
	}
	for _, member := range members {
		app, ok := m.apps[member.AppID]
		if !ok || !eligibleScenarioTestApp(app, accountID) {
			return ErrNotFound
		}
		if _, exists := m.scenarioTestMembers[member.AppID]; exists {
			return ErrConflict
		}
		if member.WebhookMode == ScenarioWebhookCaptureMode && !m.scenarioActionAppUnstartedLocked(member.AppID) {
			return ErrConflict
		}
		for _, existing := range m.scenarioTestMembers {
			if existing.AccountID == accountID && existing.RunID == runID && existing.Workload == member.Workload {
				return ErrConflict
			}
		}
	}
	if m.scenarioTestMembers == nil {
		m.scenarioTestMembers = make(map[string]ScenarioTestMember)
	}
	for _, member := range members {
		member.AccountID = accountID
		member.RunID = runID
		m.scenarioTestMembers[member.AppID] = member
		if member.WebhookMode == ScenarioWebhookCaptureMode {
			if m.scenarioActionPolicies == nil {
				m.scenarioActionPolicies = make(map[string]scenarioActionPolicy)
			}
			m.scenarioActionPolicies[member.AppID] = scenarioActionPolicy{Member: member}
		}
	}
	return nil
}

func (m *MemStore) ScenarioTestMemberByApp(_ context.Context, appID string) (ScenarioTestMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	member, ok := m.scenarioTestMembers[appID]
	if !ok {
		return ScenarioTestMember{}, ErrNotFound
	}
	return member, nil
}

func (m *MemStore) ScenarioTestAppByWorkload(_ context.Context, accountID, runID, workload string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID && member.Workload == workload {
			app, ok := m.apps[member.AppID]
			if ok && eligibleScenarioTestApp(app, accountID) {
				return app, nil
			}
			return App{}, ErrNotFound
		}
	}
	return App{}, ErrNotFound
}

func (s *PgStore) SetScenarioTestChaosPlan(ctx context.Context, accountID, runID string, plan chaos.Plan) (chaos.Lease, error) {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return chaos.Lease{}, ErrConflict
	}
	if err := plan.Validate(); err != nil {
		return chaos.Lease{}, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return chaos.Lease{}, fmt.Errorf("begin scenario chaos plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `select m.workload_name
		from scenario_test_members m
		join apps a on a.id = m.app_id
		where m.account_id = $1 and m.run_id = $2
		and a.status <> 'deleted' and a.preview_pr_state = 'open' and a.preview_expires_at > now()
		order by m.workload_name
		for update of m`, accountID, runID)
	if err != nil {
		return chaos.Lease{}, fmt.Errorf("list scenario chaos workloads: %w", err)
	}
	workloads := make(map[string]struct{}, 16)
	for rows.Next() {
		var workload string
		if err := rows.Scan(&workload); err != nil {
			rows.Close()
			return chaos.Lease{}, fmt.Errorf("scan scenario chaos workload: %w", err)
		}
		workloads[workload] = struct{}{}
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return chaos.Lease{}, fmt.Errorf("read scenario chaos workloads: %w", rowsErr)
	}
	if len(workloads) == 0 {
		return chaos.Lease{}, ErrNotFound
	}
	if err := plan.ValidateWorkloads(workloads); err != nil {
		return chaos.Lease{}, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	var live bool
	if err := tx.QueryRow(ctx, `select exists(
		select 1 from scenario_test_members m join apps a on a.id = m.app_id
		where m.account_id = $1 and m.run_id = $2 and a.status <> 'deleted'
		and a.preview_pr_state = 'open' and a.preview_expires_at > now())`, accountID, runID).Scan(&live); err != nil {
		return chaos.Lease{}, fmt.Errorf("check scenario chaos run: %w", err)
	}
	if !live {
		return chaos.Lease{}, ErrNotFound
	}
	rawRules, err := json.Marshal(plan.Rules)
	if err != nil {
		return chaos.Lease{}, fmt.Errorf("encode scenario chaos rules: %w", err)
	}
	generation := uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Duration(plan.DurationMS) * time.Millisecond)
	if _, err := tx.Exec(ctx, `delete from scenario_test_chaos_matches where account_id = $1 and run_id = $2`, accountID, runID); err != nil {
		return chaos.Lease{}, fmt.Errorf("clear prior scenario chaos matches: %w", err)
	}
	if _, err := tx.Exec(ctx, `update scenario_test_members
		set chaos_rules = $3::jsonb, chaos_expires_at = $4, chaos_generation = $5::uuid
		where account_id = $1 and run_id = $2`, accountID, runID, rawRules, expiresAt, generation); err != nil {
		return chaos.Lease{}, fmt.Errorf("store scenario chaos plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return chaos.Lease{}, fmt.Errorf("commit scenario chaos plan: %w", err)
	}
	return chaos.Lease{Generation: generation, Rules: append([]chaos.Rule(nil), plan.Rules...), ExpiresAt: expiresAt}, nil
}

func (s *PgStore) ClearScenarioTestChaosPlan(ctx context.Context, accountID, runID string) error {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return ErrConflict
	}
	result, err := s.pool.Exec(ctx, `update scenario_test_members m
		set chaos_rules = '[]'::jsonb, chaos_expires_at = now()
		from apps a
		where m.account_id = $1 and m.run_id = $2 and a.id = m.app_id
		and a.status <> 'deleted' and a.preview_pr_state = 'open' and a.preview_expires_at > now()`, accountID, runID)
	if err != nil {
		return fmt.Errorf("clear scenario chaos plan: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ScenarioTestChaosForCall(ctx context.Context, runID, callerAppID, targetWorkload string) (chaos.Lease, error) {
	var lease chaos.Lease
	var rawRules []byte
	var expiresAt pgtype.Timestamptz
	var generation pgtype.Text
	err := s.pool.QueryRow(ctx, `select member_caller.workload_name, member_caller.chaos_rules, member_caller.chaos_expires_at,
		member_caller.chaos_generation::text
		from scenario_test_members member_caller
		join scenario_test_members member_target on member_target.account_id = member_caller.account_id
			and member_target.run_id = member_caller.run_id and member_target.workload_name = $3
		where member_caller.run_id = $1 and member_caller.app_id = $2`, runID, callerAppID, targetWorkload).
		Scan(&lease.CallerWorkload, &rawRules, &expiresAt, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return chaos.Lease{}, nil
	}
	if err != nil {
		return chaos.Lease{}, fmt.Errorf("load scenario chaos plan: %w", err)
	}
	if !expiresAt.Valid || !expiresAt.Time.After(time.Now()) {
		return chaos.Lease{}, nil
	}
	if err := json.Unmarshal(rawRules, &lease.Rules); err != nil {
		return chaos.Lease{}, fmt.Errorf("decode scenario chaos plan: %w", err)
	}
	lease.ExpiresAt = expiresAt.Time
	if generation.Valid {
		lease.Generation = generation.String
	}
	filtered := lease.Rules[:0]
	for _, rule := range lease.Rules {
		if rule.To == targetWorkload && (rule.From == "" || rule.From == lease.CallerWorkload) {
			filtered = append(filtered, rule)
		}
	}
	lease.Rules = filtered
	if len(lease.Rules) == 0 {
		return chaos.Lease{}, nil
	}
	return lease, nil
}

// ScenarioTestDNSChaosByHostIP resolves the live guest identity and its
// same-run service fault plan in one query. Production callers and targets
// outside the run return an empty policy without looking up an app through a
// cached source IP.
func (s *PgStore) ScenarioTestDNSChaosByHostIP(ctx context.Context, nodeName, hostIP, targetWorkload string) (ScenarioTestDNSChaosPolicy, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(hostIP))
	if err != nil || !address.Is4() || !scenarioTestWorkloadPattern.MatchString(targetWorkload) {
		return ScenarioTestDNSChaosPolicy{}, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `with live_callers as (
		select distinct i.app_id, i.deployment_id
		from instances i join compute_nodes n on n.id = i.node_id
		where i.host_ip = $1::inet and i.state in ('running', 'draining')
		and ($2 = '' or n.name = $2)
		limit 2
	), source as (
		select app_id from live_callers where (select count(*) from live_callers) = 1
	)
	select distinct caller.app_id::text, caller.run_id, caller.workload_name,
		caller.chaos_rules, caller.chaos_expires_at, caller.chaos_generation::text
		from source
		join instances i on i.app_id = source.app_id
		join compute_nodes n on n.id = i.node_id
		join apps caller_app on caller_app.id = i.app_id
		join scenario_test_members caller on caller.app_id = i.app_id
		join scenario_test_members target on target.account_id = caller.account_id
			and target.run_id = caller.run_id and target.workload_name = $3
		join apps target_app on target_app.id = target.app_id
		where i.host_ip = $1::inet and i.state in ('running', 'draining')
		and ($2 = '' or n.name = $2)
		and caller_app.status <> 'deleted' and caller_app.preview_pr_state = 'open'
		and caller_app.preview_expires_at > now()
		and target_app.status <> 'deleted' and target_app.preview_pr_state = 'open'
		and target_app.preview_expires_at > now()
		limit 2`, address.String(), nodeName, targetWorkload)
	if err != nil {
		return ScenarioTestDNSChaosPolicy{}, fmt.Errorf("load scenario DNS chaos policy: %w", err)
	}
	defer rows.Close()
	var policy ScenarioTestDNSChaosPolicy
	var callerWorkload string
	var rawRules []byte
	var expiresAt pgtype.Timestamptz
	var generation pgtype.Text
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&policy.CallerAppID, &policy.RunID, &callerWorkload, &rawRules, &expiresAt, &generation); err != nil {
			return ScenarioTestDNSChaosPolicy{}, fmt.Errorf("scan scenario DNS chaos policy: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return ScenarioTestDNSChaosPolicy{}, fmt.Errorf("read scenario DNS chaos policy: %w", err)
	}
	if count == 0 {
		return ScenarioTestDNSChaosPolicy{}, nil
	}
	if count != 1 {
		return ScenarioTestDNSChaosPolicy{}, ErrConflict
	}
	if !expiresAt.Valid || !expiresAt.Time.After(time.Now()) {
		return policy, nil
	}
	if err := json.Unmarshal(rawRules, &policy.Lease.Rules); err != nil {
		return ScenarioTestDNSChaosPolicy{}, fmt.Errorf("decode scenario DNS chaos policy: %w", err)
	}
	policy.Lease.CallerWorkload = callerWorkload
	policy.Lease.ExpiresAt = expiresAt.Time
	if generation.Valid {
		policy.Lease.Generation = generation.String
	}
	filtered := make([]chaos.Rule, 0, len(policy.Lease.Rules))
	for _, rule := range policy.Lease.Rules {
		if rule.To == targetWorkload && (rule.From == "" || rule.From == callerWorkload) {
			filtered = append(filtered, rule)
		}
	}
	policy.Lease.Rules = filtered
	return policy, nil
}

func (s *PgStore) RecordScenarioTestChaosInjections(ctx context.Context, injections []chaos.InjectionBatch) error {
	for _, injection := range injections {
		if err := validateScenarioChaosInjection(injection); err != nil {
			return err
		}
		_, err := s.pool.Exec(ctx, `insert into scenario_test_chaos_matches
			(account_id, run_id, caller_app_id, generation, rule_id, matches)
			select m.account_id, m.run_id, m.app_id, $3::uuid, $4, $5
			from scenario_test_members m
			where m.run_id = $1 and m.app_id = $2 and m.chaos_generation = $3::uuid
			on conflict (account_id, run_id, caller_app_id, generation, rule_id)
			do update set matches = scenario_test_chaos_matches.matches + excluded.matches, updated_at = now()`,
			injection.RunID, injection.CallerApp, injection.Generation, injection.RuleID, injection.Count)
		if err != nil {
			return fmt.Errorf("record scenario chaos matches: %w", err)
		}
	}
	return nil
}

func (s *PgStore) ScenarioTestChaosMatchEvidence(ctx context.Context, accountID, runID string) (chaos.MatchEvidence, error) {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return chaos.MatchEvidence{}, ErrConflict
	}
	var evidence chaos.MatchEvidence
	err := s.pool.QueryRow(ctx, `select coalesce(m.chaos_generation::text, '')
		from scenario_test_members m join apps a on a.id = m.app_id
		where m.account_id = $1 and m.run_id = $2 and a.status <> 'deleted'
		and a.preview_pr_state = 'open' and a.preview_expires_at > now()
		order by m.workload_name limit 1`, accountID, runID).Scan(&evidence.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return chaos.MatchEvidence{}, ErrNotFound
	}
	if err != nil {
		return chaos.MatchEvidence{}, fmt.Errorf("load scenario chaos generation: %w", err)
	}
	if evidence.Generation == "" {
		return evidence, nil
	}
	rows, err := s.pool.Query(ctx, `select rule_id, sum(matches)::bigint
		from scenario_test_chaos_matches
		where account_id = $1 and run_id = $2 and generation = $3::uuid
		group by rule_id order by rule_id`, accountID, runID, evidence.Generation)
	if err != nil {
		return chaos.MatchEvidence{}, fmt.Errorf("list scenario chaos matches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var match chaos.RuleMatchCount
		if err := rows.Scan(&match.RuleID, &match.Count); err != nil {
			return chaos.MatchEvidence{}, fmt.Errorf("scan scenario chaos matches: %w", err)
		}
		evidence.Matches = append(evidence.Matches, match)
	}
	if err := rows.Err(); err != nil {
		return chaos.MatchEvidence{}, fmt.Errorf("read scenario chaos matches: %w", err)
	}
	if evidence.Matches == nil {
		evidence.Matches = []chaos.RuleMatchCount{}
	}
	return evidence, nil
}

func validateScenarioChaosInjection(injection chaos.InjectionBatch) error {
	if !scenarioTestRunPattern.MatchString(injection.RunID) || injection.CallerApp == "" ||
		!scenarioChaosRuleIDPattern.MatchString(injection.RuleID) || injection.Count < 1 {
		return ErrConflict
	}
	if _, err := uuid.Parse(injection.CallerApp); err != nil {
		return ErrConflict
	}
	if _, err := uuid.Parse(injection.Generation); err != nil {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) SetScenarioTestChaosPlan(_ context.Context, accountID, runID string, plan chaos.Plan) (chaos.Lease, error) {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return chaos.Lease{}, ErrConflict
	}
	if err := plan.Validate(); err != nil {
		return chaos.Lease{}, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	workloads := make(map[string]struct{}, 16)
	for _, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID {
			workloads[member.Workload] = struct{}{}
		}
	}
	if len(workloads) == 0 {
		return chaos.Lease{}, ErrNotFound
	}
	if err := plan.ValidateWorkloads(workloads); err != nil {
		return chaos.Lease{}, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	active := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID != accountID || member.RunID != runID {
			continue
		}
		if app, ok := m.apps[member.AppID]; ok && eligibleScenarioTestApp(app, accountID) {
			active = true
			break
		}
	}
	if !active {
		return chaos.Lease{}, ErrNotFound
	}
	expiresAt := time.Now().UTC().Add(time.Duration(plan.DurationMS) * time.Millisecond)
	if m.scenarioTestChaosPlans == nil {
		m.scenarioTestChaosPlans = make(map[string]chaos.Lease)
	}
	key := accountID + "\x00" + runID
	generation := uuid.NewString()
	lease := chaos.Lease{Generation: generation, Rules: append([]chaos.Rule(nil), plan.Rules...), ExpiresAt: expiresAt}
	m.deleteScenarioChaosMatchesLocked(accountID, runID)
	m.scenarioTestChaosPlans[key] = lease
	return lease, nil
}

func (m *MemStore) ClearScenarioTestChaosPlan(_ context.Context, accountID, runID string) error {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	active := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID != accountID || member.RunID != runID {
			continue
		}
		if app, ok := m.apps[member.AppID]; ok && eligibleScenarioTestApp(app, accountID) {
			active = true
			break
		}
	}
	if !active {
		return ErrNotFound
	}
	key := accountID + "\x00" + runID
	lease, exists := m.scenarioTestChaosPlans[key]
	if exists {
		lease.Rules = nil
		lease.ExpiresAt = time.Now().UTC()
		m.scenarioTestChaosPlans[key] = lease
	}
	return nil
}

func (m *MemStore) ScenarioTestChaosForCall(_ context.Context, runID, callerAppID, targetWorkload string) (chaos.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	caller, ok := m.scenarioTestMembers[callerAppID]
	if !ok || caller.RunID != runID {
		return chaos.Lease{}, nil
	}
	targetFound := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID == caller.AccountID && member.RunID == runID && member.Workload == targetWorkload {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return chaos.Lease{}, nil
	}
	lease, ok := m.scenarioTestChaosPlans[caller.AccountID+"\x00"+runID]
	if !ok || !lease.ExpiresAt.After(time.Now()) {
		return chaos.Lease{}, nil
	}
	lease.CallerWorkload = caller.Workload
	filtered := make([]chaos.Rule, 0, len(lease.Rules))
	for _, rule := range lease.Rules {
		if rule.To == targetWorkload && (rule.From == "" || rule.From == caller.Workload) {
			filtered = append(filtered, rule)
		}
	}
	lease.Rules = filtered
	if len(lease.Rules) == 0 {
		return chaos.Lease{}, nil
	}
	return lease, nil
}

func (m *MemStore) ScenarioTestDNSChaosByHostIP(_ context.Context, nodeName, hostIP, targetWorkload string) (ScenarioTestDNSChaosPolicy, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(hostIP))
	if err != nil || !address.Is4() || !scenarioTestWorkloadPattern.MatchString(targetWorkload) {
		return ScenarioTestDNSChaosPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	callerAppID, callerDeploymentID := "", ""
	for _, instance := range m.instances {
		instanceAddress, parseErr := netip.ParseAddr(strings.TrimSpace(instance.HostIP))
		if parseErr != nil || instanceAddress != address ||
			(instance.State != string(StateRunning) && instance.State != string(StateDraining)) {
			continue
		}
		if nodeName != "" {
			node, ok := m.computeNodes[instance.NodeID]
			if !ok || node.Name != nodeName {
				continue
			}
		}
		if callerAppID != "" && (callerAppID != instance.AppID || callerDeploymentID != instance.DeploymentID) {
			return ScenarioTestDNSChaosPolicy{}, nil
		}
		callerAppID, callerDeploymentID = instance.AppID, instance.DeploymentID
	}
	app, appOK := m.apps[callerAppID]
	if !appOK || !eligibleScenarioTestApp(app, app.AccountID) {
		return ScenarioTestDNSChaosPolicy{}, nil
	}
	caller, ok := m.scenarioTestMembers[callerAppID]
	if !ok || callerAppID == "" {
		return ScenarioTestDNSChaosPolicy{}, nil
	}
	targetFound := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID != caller.AccountID || member.RunID != caller.RunID || member.Workload != targetWorkload {
			continue
		}
		app, ok := m.apps[member.AppID]
		if ok && eligibleScenarioTestApp(app, caller.AccountID) {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return ScenarioTestDNSChaosPolicy{}, nil
	}
	policy := ScenarioTestDNSChaosPolicy{RunID: caller.RunID, CallerAppID: callerAppID}
	lease, ok := m.scenarioTestChaosPlans[caller.AccountID+"\x00"+caller.RunID]
	if !ok || !lease.ExpiresAt.After(time.Now()) {
		return policy, nil
	}
	lease.CallerWorkload = caller.Workload
	filtered := make([]chaos.Rule, 0, len(lease.Rules))
	for _, rule := range lease.Rules {
		if rule.To == targetWorkload && (rule.From == "" || rule.From == caller.Workload) {
			filtered = append(filtered, rule)
		}
	}
	lease.Rules = filtered
	policy.Lease = lease
	return policy, nil
}

func (m *MemStore) RecordScenarioTestChaosInjections(_ context.Context, injections []chaos.InjectionBatch) error {
	for _, injection := range injections {
		if err := validateScenarioChaosInjection(injection); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scenarioTestChaosMatches == nil {
		m.scenarioTestChaosMatches = make(map[string]map[string]int64)
	}
	for _, injection := range injections {
		caller, ok := m.scenarioTestMembers[injection.CallerApp]
		if !ok || caller.RunID != injection.RunID {
			continue
		}
		planKey := caller.AccountID + "\x00" + injection.RunID
		lease, ok := m.scenarioTestChaosPlans[planKey]
		if !ok || lease.Generation != injection.Generation {
			continue
		}
		evidenceKey := scenarioChaosEvidenceKey(caller.AccountID, injection.RunID, injection.Generation)
		if m.scenarioTestChaosMatches[evidenceKey] == nil {
			m.scenarioTestChaosMatches[evidenceKey] = make(map[string]int64)
		}
		m.scenarioTestChaosMatches[evidenceKey][injection.RuleID] += injection.Count
	}
	return nil
}

func (m *MemStore) ScenarioTestChaosMatchEvidence(_ context.Context, accountID, runID string) (chaos.MatchEvidence, error) {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return chaos.MatchEvidence{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	active := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID != accountID || member.RunID != runID {
			continue
		}
		if app, ok := m.apps[member.AppID]; ok && eligibleScenarioTestApp(app, accountID) {
			active = true
			break
		}
	}
	if !active {
		return chaos.MatchEvidence{}, ErrNotFound
	}
	lease, ok := m.scenarioTestChaosPlans[accountID+"\x00"+runID]
	if !ok || lease.Generation == "" {
		return chaos.MatchEvidence{Matches: []chaos.RuleMatchCount{}}, nil
	}
	evidence := chaos.MatchEvidence{Generation: lease.Generation, Matches: []chaos.RuleMatchCount{}}
	for ruleID, count := range m.scenarioTestChaosMatches[scenarioChaosEvidenceKey(accountID, runID, lease.Generation)] {
		evidence.Matches = append(evidence.Matches, chaos.RuleMatchCount{RuleID: ruleID, Count: count})
	}
	sort.Slice(evidence.Matches, func(i, j int) bool { return evidence.Matches[i].RuleID < evidence.Matches[j].RuleID })
	return evidence, nil
}

func scenarioChaosEvidenceKey(accountID, runID, generation string) string {
	return accountID + "\x00" + runID + "\x00" + generation
}

func (m *MemStore) deleteScenarioChaosMatchesLocked(accountID, runID string) {
	prefix := accountID + "\x00" + runID + "\x00"
	for key := range m.scenarioTestChaosMatches {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(m.scenarioTestChaosMatches, key)
		}
	}
}

func (m *MemStore) DeleteScenarioTestMembers(_ context.Context, accountID, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID {
			found = true
			if m.apps[member.AppID].Status != AppDeleted {
				return ErrConflict
			}
		}
	}
	if !found {
		return ErrNotFound
	}
	for appID, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID {
			delete(m.scenarioTestMembers, appID)
		}
	}
	delete(m.scenarioTestChaosPlans, accountID+"\x00"+runID)
	m.deleteScenarioChaosMatchesLocked(accountID, runID)
	return nil
}

func (s *PgStore) DeleteScenarioTestMembers(ctx context.Context, accountID, runID string) error {
	// Check and delete in one statement snapshot. A separate active-member
	// check followed by an unconditional DELETE could erase a member that was
	// registered between those statements while its app was still running.
	result, err := s.pool.Exec(ctx, `delete from scenario_test_members m
		where m.account_id = $1 and m.run_id = $2
		and not exists (
			select 1 from scenario_test_members member
			join apps a on a.id = member.app_id
			where member.account_id = $1 and member.run_id = $2
			and a.status <> 'deleted'
		)`, accountID, runID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var active bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from scenario_test_members m
			join apps a on a.id = m.app_id where m.account_id = $1 and m.run_id = $2
			and a.status <> 'deleted')`, accountID, runID).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
		return ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `delete from scenario_test_chaos_matches where account_id = $1 and run_id = $2`, accountID, runID); err != nil {
		return fmt.Errorf("delete scenario chaos matches: %w", err)
	}
	return nil
}

func (s *PgStore) PruneScenarioTestMembers(ctx context.Context, maxRuns int) (int, error) {
	if maxRuns < 1 {
		return 0, nil
	}
	result, err := s.pool.Exec(ctx, `with finished as (
		select m.account_id, m.run_id from scenario_test_members m
		join apps a on a.id = m.app_id
		group by m.account_id, m.run_id
		having bool_and(a.status = 'deleted')
		limit $1
	), removed_matches as (
		delete from scenario_test_chaos_matches c using finished f
		where c.account_id = f.account_id and c.run_id = f.run_id
		returning c.run_id
	)
	delete from scenario_test_members m using finished f
	where m.account_id = f.account_id and m.run_id = f.run_id`, maxRuns)
	if err != nil {
		return 0, fmt.Errorf("prune scenario test members: %w", err)
	}
	return int(result.RowsAffected()), nil
}

func (m *MemStore) PruneScenarioTestMembers(_ context.Context, maxRuns int) (int, error) {
	if maxRuns < 1 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := make(map[string]bool)
	for _, member := range m.scenarioTestMembers {
		key := member.AccountID + "\x00" + member.RunID
		if _, seen := groups[key]; !seen {
			groups[key] = true
		}
		if app, ok := m.apps[member.AppID]; ok && app.Status != AppDeleted {
			groups[key] = false
		}
	}
	deleted := 0
	prunedGroups := 0
	for appID, member := range m.scenarioTestMembers {
		if prunedGroups >= maxRuns {
			break
		}
		key := member.AccountID + "\x00" + member.RunID
		if groups[key] {
			delete(m.scenarioTestMembers, appID)
			deleted++
			// Count the group once, after its final member is removed.
			remaining := false
			for _, other := range m.scenarioTestMembers {
				if other.AccountID == member.AccountID && other.RunID == member.RunID {
					remaining = true
					break
				}
			}
			if !remaining {
				prunedGroups++
				delete(m.scenarioTestChaosPlans, key)
				m.deleteScenarioChaosMatchesLocked(member.AccountID, member.RunID)
			}
		}
	}
	return deleted, nil
}
