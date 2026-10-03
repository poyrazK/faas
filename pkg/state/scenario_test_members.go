package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/chaos"
)

// ScenarioTestMember is one app's logical service name in a single test run.
// Membership persists through soft deletion so no test app can fall back to
// a production service while its VM is being drained.
type ScenarioTestMember struct {
	AccountID string
	RunID     string
	Workload  string
	AppID     string
}

var scenarioTestRunPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var scenarioTestWorkloadPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func validScenarioTestMembers(accountID, runID string, members []ScenarioTestMember) error {
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) || len(members) == 0 || len(members) > 16 {
		return ErrConflict
	}
	seenNames := make(map[string]bool, len(members))
	seenApps := make(map[string]bool, len(members))
	for _, member := range members {
		if !scenarioTestWorkloadPattern.MatchString(member.Workload) || member.AppID == "" || seenNames[member.Workload] || seenApps[member.AppID] {
			return ErrConflict
		}
		seenNames[member.Workload] = true
		seenApps[member.AppID] = true
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
	for _, member := range members {
		var eligible bool
		err := tx.QueryRow(ctx, `select account_id = $2 and preview_of_slug is not null and preview_pr_number = 0
			and status <> 'deleted' and preview_pr_state = 'open' and preview_expires_at > now()
			from apps where id = $1`, member.AppID, accountID).Scan(&eligible)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !eligible) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("validate scenario test app: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into scenario_test_members (account_id, run_id, workload_name, app_id)
			values ($1, $2, $3, $4)`, accountID, runID, member.Workload, member.AppID); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ScenarioTestMemberByApp(ctx context.Context, appID string) (ScenarioTestMember, error) {
	var member ScenarioTestMember
	err := s.pool.QueryRow(ctx, `select account_id, run_id, workload_name, app_id
		from scenario_test_members where app_id = $1`, appID).Scan(&member.AccountID, &member.RunID, &member.Workload, &member.AppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScenarioTestMember{}, ErrNotFound
	}
	if err != nil {
		return ScenarioTestMember{}, fmt.Errorf("load scenario test member: %w", err)
	}
	return member, nil
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
	for _, member := range members {
		app, ok := m.apps[member.AppID]
		if !ok || !eligibleScenarioTestApp(app, accountID) {
			return ErrNotFound
		}
		if _, exists := m.scenarioTestMembers[member.AppID]; exists {
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
	expiresAt := time.Now().UTC().Add(time.Duration(plan.DurationMS) * time.Millisecond)
	if _, err := tx.Exec(ctx, `update scenario_test_members
		set chaos_rules = $3::jsonb, chaos_expires_at = $4
		where account_id = $1 and run_id = $2`, accountID, runID, rawRules, expiresAt); err != nil {
		return chaos.Lease{}, fmt.Errorf("store scenario chaos plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return chaos.Lease{}, fmt.Errorf("commit scenario chaos plan: %w", err)
	}
	return chaos.Lease{Rules: append([]chaos.Rule(nil), plan.Rules...), ExpiresAt: expiresAt}, nil
}

func (s *PgStore) ScenarioTestChaosForCall(ctx context.Context, runID, callerAppID, targetWorkload string) (chaos.Lease, error) {
	var lease chaos.Lease
	var rawRules []byte
	var expiresAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `select member_caller.workload_name, member_caller.chaos_rules, member_caller.chaos_expires_at
		from scenario_test_members member_caller
		join scenario_test_members member_target on member_target.account_id = member_caller.account_id
			and member_target.run_id = member_caller.run_id and member_target.workload_name = $3
		where member_caller.run_id = $1 and member_caller.app_id = $2`, runID, callerAppID, targetWorkload).
		Scan(&lease.CallerWorkload, &rawRules, &expiresAt)
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
	lease := chaos.Lease{Rules: append([]chaos.Rule(nil), plan.Rules...), ExpiresAt: expiresAt}
	m.scenarioTestChaosPlans[key] = lease
	return lease, nil
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
			}
		}
	}
	return deleted, nil
}
