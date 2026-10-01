package managedpostgres

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ CutoverStore = (*MemoryStore)(nil)

func cloneCutover(c Cutover) Cutover {
	c.Credentials = append([]CutoverCredential(nil), c.Credentials...)
	for i := range c.Credentials {
		c.Credentials[i].Sealed.Ciphertext = append([]byte(nil), c.Credentials[i].Sealed.Ciphertext...)
	}
	c.Source = cloneDatabase(c.Source)
	c.Target = cloneDatabase(c.Target)
	return c
}
func (s *MemoryStore) databaseCutoverPinned(id string) bool {
	for _, c := range s.cutovers {
		if c.State != CutoverCancelled && (c.Source.ID == id || c.Target.ID == id) {
			return true
		}
	}
	return false
}
func (s *MemoryStore) bindingCutoverPinned(id string) bool {
	for _, c := range s.cutovers {
		if c.State == CutoverCancelled {
			continue
		}
		for _, m := range c.Credentials {
			if m.SourceBindingID == id {
				return true
			}
		}
	}
	return false
}
func (s *MemoryStore) ReserveCutover(_ context.Context, r PrepareCutoverRequest, now time.Time) (Cutover, bool, error) {
	if !validPrepareCutover(r, now) {
		return Cutover{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, sourceOK := s.databases[r.SourceDatabaseID]
	target, targetOK := s.databases[r.TargetDatabaseID]
	if !sourceOK || !targetOK || source.AccountID != r.AccountID || target.AccountID != r.AccountID {
		return Cutover{}, false, ErrNotFound
	}
	for _, c := range s.cutovers {
		if c.ID == r.ID || (c.State != CutoverCancelled && c.AppID == r.AppID && c.Scope == r.Scope) {
			if !sameCutoverRequest(c, r) {
				return Cutover{}, false, ErrConflict
			}
			return cloneCutover(c), false, nil
		}
	}
	if source.State != StateReady || target.State != StateReady || source.ProviderResourceID == "" || target.ProviderResourceID == "" || target.RestoreSourceDatabaseID != source.ID || target.RestoreSourceResourceID != source.ProviderResourceID || s.databaseCutoverPinned(source.ID) || s.databaseCutoverPinned(target.ID) {
		return Cutover{}, false, ErrConflict
	}
	c := Cutover{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, Scope: r.Scope, Source: source, Target: target, State: CutoverPreparing, RetryAt: now, CreatedAt: now, UpdatedAt: now}
	for _, b := range s.bindings {
		if b.DatabaseID == target.ID && b.State != BindingStateDeleted {
			return Cutover{}, false, ErrConflict
		}
		if b.DatabaseID != source.ID || b.AppID != r.AppID || b.Scope != r.Scope || b.State == BindingStateDeleted {
			continue
		}
		if b.AccountID != r.AccountID || b.State != BindingStateReady || b.RotationPreviousGeneration != 0 || b.LeaseToken != "" || s.bindingCutoverPinned(b.ID) {
			return Cutover{}, false, ErrConflict
		}
		c.Credentials = append(c.Credentials, CutoverCredential{ID: uuid.NewString(), SourceBindingID: b.ID, SourceCredentialGeneration: b.CredentialGeneration, EnvironmentKey: b.EnvironmentKey, Access: b.Access, State: "pending"})
	}
	if len(c.Credentials) == 0 {
		return Cutover{}, false, ErrConflict
	}
	sort.Slice(c.Credentials, func(i, j int) bool { return c.Credentials[i].EnvironmentKey < c.Credentials[j].EnvironmentKey })
	s.cutovers[c.ID] = c
	return cloneCutover(c), true, nil
}
func (s *MemoryStore) GetCutover(_ context.Context, account, id string) (Cutover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return Cutover{}, ErrNotFound
	}
	return cloneCutover(c), nil
}
func (s *MemoryStore) ClaimCutover(_ context.Context, account, id, token string, now, until time.Time) (Cutover, error) {
	if token == "" || now.IsZero() || !until.After(now) {
		return Cutover{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return Cutover{}, ErrNotFound
	}
	if (c.State != CutoverPreparing && c.State != CutoverCancelling) || c.LeaseUntil.After(now) || c.RetryAt.After(now) {
		return Cutover{}, ErrConflict
	}
	c.LeaseToken = token
	c.LeaseUntil = until
	c.AttemptCount = min(c.AttemptCount+1, 30)
	c.UpdatedAt = now
	s.cutovers[id] = c
	return cloneCutover(c), nil
}
func (s *MemoryStore) SaveCutoverCredential(_ context.Context, c Cutover, m CutoverCredential, sealed SealedCredential, now time.Time) error {
	if !validSealedCredential(sealed) {
		return ErrInvalid
	}
	return s.finishCutoverStep(c, m, sealed, false, now)
}
func (s *MemoryStore) RevokeCutoverCredential(_ context.Context, c Cutover, m CutoverCredential, now time.Time) error {
	return s.finishCutoverStep(c, m, SealedCredential{}, true, now)
}
func (s *MemoryStore) finishCutoverStep(claim Cutover, member CutoverCredential, sealed SealedCredential, revoke bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[claim.ID]
	expected := CutoverPreparing
	if revoke {
		expected = CutoverCancelling
	}
	if !ok || c.AccountID != claim.AccountID || c.State != expected || claim.LeaseToken == "" || c.LeaseToken != claim.LeaseToken || !c.LeaseUntil.After(now) {
		return ErrConflict
	}
	matched := false
	for i, m := range c.Credentials {
		if m.ID != member.ID {
			continue
		}
		if (!revoke && m.State != "pending") || (revoke && m.State == "revoked") {
			return ErrConflict
		}
		m.Sealed = sealed
		m.Sealed.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
		m.State = "sealed"
		if revoke {
			m.State = "revoked"
		}
		c.Credentials[i] = m
		matched = true
	}
	if !matched {
		return ErrConflict
	}
	done := true
	want := "sealed"
	if revoke {
		want = "revoked"
	}
	for _, m := range c.Credentials {
		done = done && m.State == want
	}
	if done {
		c.State = CutoverPrepared
		if revoke {
			c.State = CutoverCancelled
		}
	}
	c.LeaseToken = ""
	c.LeaseUntil = time.Time{}
	c.AttemptCount = 0
	c.LastErrorCode = ""
	c.RetryAt = now
	c.UpdatedAt = now
	s.cutovers[c.ID] = c
	return nil
}
func (s *MemoryStore) ReleaseCutover(_ context.Context, claim Cutover, code string, now, retry time.Time) error {
	if !validErrorCode(code) || code == "" || now.IsZero() || retry.Before(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[claim.ID]
	if !ok || c.AccountID != claim.AccountID || claim.LeaseToken == "" || c.LeaseToken != claim.LeaseToken || !c.LeaseUntil.After(now) {
		return ErrConflict
	}
	c.LeaseToken = ""
	c.LeaseUntil = time.Time{}
	c.LastErrorCode = code
	c.RetryAt = retry
	c.UpdatedAt = now
	s.cutovers[c.ID] = c
	return nil
}
func (s *MemoryStore) CancelCutover(_ context.Context, account, id string, now time.Time) (Cutover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return Cutover{}, ErrNotFound
	}
	if c.State != CutoverCancelled {
		c.State = CutoverCancelling
	}
	c.RetryAt = now
	c.UpdatedAt = now
	s.cutovers[id] = c
	return cloneCutover(c), nil
}
func (s *MemoryStore) DueCutovers(_ context.Context, include bool, limit int, now time.Time) ([]Cutover, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Cutover{}
	for _, c := range s.cutovers {
		if (c.State == CutoverCancelling || (include && c.State == CutoverPreparing)) && !c.RetryAt.After(now) && !c.LeaseUntil.After(now) {
			out = append(out, cloneCutover(c))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RetryAt.Equal(out[j].RetryAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RetryAt.Before(out[j].RetryAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
