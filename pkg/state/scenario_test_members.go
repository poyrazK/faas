package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
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
	return nil
}

func (s *PgStore) DeleteScenarioTestMembers(ctx context.Context, accountID, runID string) error {
	// A failed teardown keeps the namespace fenced until its apps are reaped.
	var active bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from scenario_test_members m
		join apps a on a.id = m.app_id where m.account_id = $1 and m.run_id = $2
		and a.status <> 'deleted')`, accountID, runID).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	result, err := s.pool.Exec(ctx, `delete from scenario_test_members where account_id = $1 and run_id = $2`, accountID, runID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
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
			}
		}
	}
	return deleted, nil
}
