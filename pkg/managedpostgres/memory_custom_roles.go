package managedpostgres

import (
	"context"
	"slices"
	"sort"
	"time"
)

var _ CustomRoleStore = (*MemoryStore)(nil)

func (s *MemoryStore) customRolesOf(databaseID string) map[string]CustomRole {
	if s.customRoles == nil {
		s.customRoles = map[string]map[string]CustomRole{}
	}
	roles := s.customRoles[databaseID]
	if roles == nil {
		roles = map[string]CustomRole{}
		s.customRoles[databaseID] = roles
	}
	return roles
}

func cloneCustomRole(role CustomRole) CustomRole {
	role.GrantedTo = slices.Clone(role.GrantedTo)
	role.MemberOf = slices.Clone(role.MemberOf)
	role.DefaultFor = slices.Clone(role.DefaultFor)
	return role
}

// customRoleDatabase finds the customer database a declaration belongs to.
func (s *MemoryStore) customRoleDatabase(accountID, databaseID string) (Database, error) {
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID || database.EnvironmentCloneOperationID != "" {
		return Database{}, ErrNotFound
	}
	return database, nil
}

// bypassRLSFenced reports a live Data API or Auth binding on the database.
func (s *MemoryStore) bypassRLSFenced(databaseID string) bool {
	for _, binding := range s.bindings {
		if binding.DatabaseID == databaseID && binding.State != BindingStateDeleted &&
			(binding.Access == CredentialDataAPI || binding.Access == CredentialAuth) {
			return true
		}
	}
	return false
}

// hasBypassRLSRoleLocked reports a declared BYPASSRLS role; mu is held.
func (s *MemoryStore) hasBypassRLSRoleLocked(databaseID string) bool {
	for _, role := range s.customRoles[databaseID] {
		if role.BypassRLS {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ListCustomRoles(_ context.Context, accountID, databaseID string) ([]CustomRole, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.customRoleDatabase(accountID, databaseID); err != nil {
		return nil, err
	}
	out := make([]CustomRole, 0, len(s.customRoles[databaseID]))
	for _, role := range s.customRoles[databaseID] {
		out = append(out, cloneCustomRole(role))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryStore) SaveCustomRole(_ context.Context, role CustomRole, limit int, now time.Time) (CustomRole, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, err := s.customRoleDatabase(role.AccountID, role.DatabaseID)
	if err != nil {
		return CustomRole{}, err
	}
	if database.State != StateReady {
		return CustomRole{}, ErrConflict
	}
	roles := s.customRolesOf(role.DatabaseID)
	existing, exists := roles[role.Name]
	if exists && existing.State == CustomRoleDeleting {
		return CustomRole{}, ErrConflict
	}
	if !exists && limit > 0 && len(roles) >= limit {
		return CustomRole{}, ErrQuotaExceeded
	}
	if role.BypassRLS && s.bypassRLSFenced(role.DatabaseID) {
		return CustomRole{}, ErrBypassRLSConflict
	}
	current := make([]CustomRole, 0, len(roles))
	for _, other := range roles {
		current = append(current, other)
	}
	for _, parent := range role.MemberOf {
		other, ok := roles[parent]
		if !ok || other.State == CustomRoleDeleting || parent == role.Name {
			return CustomRole{}, ErrInvalid
		}
	}
	if customRoleCycle(current, role.Name, role.MemberOf) {
		return CustomRole{}, ErrInvalid
	}
	if customRoleDefaultTaken(current, role) != "" {
		return CustomRole{}, ErrCustomRoleDefaultTaken
	}
	role.State = CustomRolePending
	role.Revision = existing.Revision + 1
	role.LastError, role.AttemptCount, role.RetryAt, role.UpdatedAt = "", 0, now, now
	role.CreatedAt = now
	if exists {
		role.CreatedAt = existing.CreatedAt
	}
	roles[role.Name] = cloneCustomRole(role)
	return cloneCustomRole(role), nil
}

func (s *MemoryStore) DeleteCustomRole(_ context.Context, accountID, databaseID, name string, now time.Time) (CustomRole, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.customRoleDatabase(accountID, databaseID); err != nil {
		return CustomRole{}, err
	}
	roles := s.customRolesOf(databaseID)
	role, ok := roles[name]
	if !ok {
		return CustomRole{}, ErrNotFound
	}
	for _, other := range roles {
		if other.State != CustomRoleDeleting && slices.Contains(other.MemberOf, name) {
			return CustomRole{}, ErrConflict
		}
	}
	if role.State != CustomRoleDeleting {
		role.State = CustomRoleDeleting
		role.Revision++
		role.LastError, role.AttemptCount, role.RetryAt, role.UpdatedAt = "", 0, now, now
		roles[name] = role
	}
	return cloneCustomRole(role), nil
}

func (s *MemoryStore) FinishCustomRoleApply(_ context.Context, accountID, databaseID string, applied map[string]int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.customRoleDatabase(accountID, databaseID); err != nil {
		return err
	}
	roles := s.customRolesOf(databaseID)
	for name, revision := range applied {
		role, ok := roles[name]
		if !ok || role.Revision != revision {
			continue
		}
		if role.State == CustomRoleDeleting {
			delete(roles, name)
			continue
		}
		role.State, role.LastError, role.AttemptCount, role.UpdatedAt = CustomRoleReady, "", 0, now
		roles[name] = role
	}
	return nil
}

func (s *MemoryStore) FailCustomRoleApply(_ context.Context, accountID, databaseID, code string, retryAt, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.customRoleDatabase(accountID, databaseID); err != nil {
		return err
	}
	roles := s.customRolesOf(databaseID)
	for name, role := range roles {
		if role.State == CustomRoleReady {
			continue
		}
		role.LastError, role.RetryAt, role.UpdatedAt = code, retryAt, now
		role.AttemptCount++
		roles[name] = role
	}
	return nil
}

func (s *MemoryStore) DueCustomRoleDatabases(_ context.Context, limit int, now time.Time) ([]CustomRoleDatabase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []CustomRoleDatabase
	for databaseID, roles := range s.customRoles {
		for _, role := range roles {
			if role.State != CustomRoleReady && !role.RetryAt.After(now) {
				out = append(out, CustomRoleDatabase{AccountID: role.AccountID, DatabaseID: databaseID})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DatabaseID < out[j].DatabaseID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) HasBypassRLSCustomRole(_ context.Context, accountID, databaseID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.customRoleDatabase(accountID, databaseID); err != nil {
		return false, err
	}
	return s.hasBypassRLSRoleLocked(databaseID), nil
}
