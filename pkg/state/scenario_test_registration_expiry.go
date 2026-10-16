package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

// A run expires at the latest persisted member deadline. Unknown legacy
// deadlines prevent automatic removal; expiry alone never proves absence.
func scenarioTestRegistrationStatus(members []ScenarioTestMember, now time.Time) api.ScenarioTestRegistration {
	out := api.ScenarioTestRegistration{SchemaVersion: 1, Members: map[string]string{}, ExpiryState: "unknown"}
	known := len(members) > 0
	var latest time.Time
	for _, member := range members {
		out.Members[member.Workload] = member.AppID
		if member.RegistrationExpiresAt == nil || member.RegistrationExpiresAt.IsZero() {
			known = false
		} else if member.RegistrationExpiresAt.After(latest) {
			latest = *member.RegistrationExpiresAt
		}
	}
	if known {
		out.ExpiresAt = &latest
		out.ExpiryState = "pending"
		if !latest.After(now) {
			out.ExpiryState = "expired"
		}
	}
	return out
}

func (m *MemStore) ScenarioTestRegistrationStatus(_ context.Context, accountID, runID string) (api.ScenarioTestRegistration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	members := []ScenarioTestMember{}
	for _, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID {
			members = append(members, member)
		}
	}
	if len(members) == 0 {
		return api.ScenarioTestRegistration{}, ErrNotFound
	}
	return scenarioTestRegistrationStatus(members, time.Now()), nil
}

func (s *PgStore) ScenarioTestRegistrationStatus(ctx context.Context, accountID, runID string) (api.ScenarioTestRegistration, error) {
	rows, err := s.pool.Query(ctx, `select workload_name, app_id::text, registration_expires_at
		from scenario_test_members where account_id=$1 and run_id=$2`, accountID, runID)
	if err != nil {
		return api.ScenarioTestRegistration{}, err
	}
	defer rows.Close()
	members := []ScenarioTestMember{}
	for rows.Next() {
		var member ScenarioTestMember
		var expires pgtype.Timestamptz
		if err := rows.Scan(&member.Workload, &member.AppID, &expires); err != nil {
			return api.ScenarioTestRegistration{}, err
		}
		if expires.Valid && expires.InfinityModifier == pgtype.Finite {
			member.RegistrationExpiresAt = &expires.Time
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return api.ScenarioTestRegistration{}, err
	}
	if len(members) == 0 {
		return api.ScenarioTestRegistration{}, ErrNotFound
	}
	return scenarioTestRegistrationStatus(members, time.Now()), nil
}

func (m *MemStore) ScenarioTestRegistration(ctx context.Context, accountID, runID string) (map[string]string, error) {
	out, err := m.ScenarioTestRegistrationStatus(ctx, accountID, runID)
	return out.Members, err
}

func (s *PgStore) ScenarioTestRegistration(ctx context.Context, accountID, runID string) (map[string]string, error) {
	out, err := s.ScenarioTestRegistrationStatus(ctx, accountID, runID)
	return out.Members, err
}

type scenarioTestExpiryCandidate struct {
	accountID, runID string
	members          map[string]string
	expiresAt        time.Time
}

func (s *PgStore) PruneScenarioTestMembers(ctx context.Context, maxRuns int) (int, error) {
	if maxRuns < 1 {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `select m.account_id::text, m.run_id,
		jsonb_object_agg(m.workload_name, m.app_id::text), max(m.registration_expires_at)
		from scenario_test_members m join apps a on a.id=m.app_id
		group by m.account_id, m.run_id
		having bool_and(a.status='deleted') and bool_and(m.registration_expires_at is not null)
		and max(m.registration_expires_at) <= now()
		order by max(m.registration_expires_at), m.account_id, m.run_id limit $1`, maxRuns)
	if err != nil {
		return 0, fmt.Errorf("discover expired scenario registrations: %w", err)
	}
	candidates := []scenarioTestExpiryCandidate{}
	for rows.Next() {
		var candidate scenarioTestExpiryCandidate
		var raw []byte
		if err := rows.Scan(&candidate.accountID, &candidate.runID, &raw, &candidate.expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		if err := json.Unmarshal(raw, &candidate.members); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	deleted := 0
	for _, candidate := range candidates {
		n, err := s.pruneScenarioTestRegistration(ctx, candidate)
		deleted += n
		if err != nil {
			return deleted, fmt.Errorf("prune expired scenario registration: %w", err)
		}
	}
	return deleted, nil
}

// Selection grants no authority. Serialize with registration/manual deletion,
// then compare the selected inventory and lock member apps before removal.
func (s *PgStore) pruneScenarioTestRegistration(ctx context.Context, candidate scenarioTestExpiryCandidate) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1, 0))`, candidate.accountID+"/"+candidate.runID).Scan(&locked); err != nil || !locked {
		return 0, err
	}
	rows, err := tx.Query(ctx, `select m.workload_name, m.app_id::text, m.registration_expires_at, a.status
		from scenario_test_members m join apps a on a.id=m.app_id
		where m.account_id=$1 and m.run_id=$2 order by m.app_id for update of m, a skip locked`, candidate.accountID, candidate.runID)
	if err != nil {
		return 0, err
	}
	members := []ScenarioTestMember{}
	allDeleted := true
	for rows.Next() {
		var member ScenarioTestMember
		var expiry pgtype.Timestamptz
		var status string
		if err := rows.Scan(&member.Workload, &member.AppID, &expiry, &status); err != nil {
			rows.Close()
			return 0, err
		}
		if expiry.Valid && expiry.InfinityModifier == pgtype.Finite {
			member.RegistrationExpiresAt = &expiry.Time
		}
		allDeleted = allDeleted && status == "deleted"
		members = append(members, member)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	current := scenarioTestRegistrationStatus(members, time.Now())
	if !allDeleted || current.ExpiryState != "expired" || !current.ExpiresAt.Equal(candidate.expiresAt) ||
		api.ScenarioTestRegistrationSHA256(current.Members) != api.ScenarioTestRegistrationSHA256(candidate.members) {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `delete from scenario_test_chaos_matches where account_id=$1 and run_id=$2`, candidate.accountID, candidate.runID); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `delete from scenario_test_members where account_id=$1 and run_id=$2`, candidate.accountID, candidate.runID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(result.RowsAffected()), nil
}

func (m *MemStore) PruneScenarioTestMembers(_ context.Context, maxRuns int) (int, error) {
	if maxRuns < 1 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[string][]ScenarioTestMember{}
	for _, member := range m.scenarioTestMembers {
		key := member.AccountID + "\x00" + member.RunID
		groups[key] = append(groups[key], member)
	}
	keys := []string{}
	now := time.Now()
	for key, members := range groups {
		if scenarioTestRegistrationStatus(members, now).ExpiryState != "expired" {
			continue
		}
		allDeleted := true
		for _, member := range members {
			app, ok := m.apps[member.AppID]
			allDeleted = allDeleted && ok && app.Status == AppDeleted
		}
		if allDeleted {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a := scenarioTestRegistrationStatus(groups[keys[i]], now).ExpiresAt
		b := scenarioTestRegistrationStatus(groups[keys[j]], now).ExpiresAt
		if a.Equal(*b) {
			return keys[i] < keys[j]
		}
		return a.Before(*b)
	})
	deleted := 0
	for i, key := range keys {
		if i >= maxRuns {
			break
		}
		for _, member := range groups[key] {
			delete(m.scenarioTestMembers, member.AppID)
			deleted++
		}
		delete(m.scenarioTestChaosPlans, key)
		first := groups[key][0]
		m.deleteScenarioChaosMatchesLocked(first.AccountID, first.RunID)
	}
	return deleted, nil
}
