package managedpostgres

import "context"

type ownershipKey struct{}
type ownershipScope struct{ orgID, policyID string }

// WithOwnership scopes customer inventory and reservation policy to a persisted
// organization selected by authorization, never an active-org client hint.
func WithOwnership(ctx context.Context, orgID, policyID string) context.Context {
	return context.WithValue(ctx, ownershipKey{}, ownershipScope{orgID, policyID})
}
func Organization(ctx context.Context) string {
	s, _ := ctx.Value(ownershipKey{}).(ownershipScope)
	return s.orgID
}
func PolicySubject(ctx context.Context, namespace string) string {
	if s, ok := ctx.Value(ownershipKey{}).(ownershipScope); ok && s.policyID != "" {
		return s.policyID
	}
	return namespace
}
func (b Binding) DatabaseNamespace() string {
	if b.DatabaseAccountID != "" {
		return b.DatabaseAccountID
	}
	return b.AccountID
}

// OwnershipReader locates an immutable root before permission evaluation. It
// does not grant customer access or expose a provider or credential identifier.
type OwnershipReader interface {
	DatabaseOwnership(context.Context, string) (Database, error)
	BindingOwnership(context.Context, string) (Binding, error)
}

func (s *Service) DatabaseOwnership(ctx context.Context, id string) (Database, error) {
	reader, ok := s.store.(OwnershipReader)
	if !ok {
		return Database{}, ErrNotFound
	}
	return reader.DatabaseOwnership(ctx, id)
}
func (s *Service) BindingOwnership(ctx context.Context, id string) (Binding, error) {
	reader, ok := s.store.(OwnershipReader)
	if !ok {
		return Binding{}, ErrNotFound
	}
	return reader.BindingOwnership(ctx, id)
}
func (s *MemoryStore) DatabaseOwnership(_ context.Context, id string) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.databases[id]
	if !ok {
		return Database{}, ErrNotFound
	}
	return cloneDatabase(d), nil
}
func (s *MemoryStore) BindingOwnership(_ context.Context, id string) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bindings[id]
	if !ok {
		return Binding{}, ErrNotFound
	}
	return cloneBinding(b), nil
}

func inOwnershipScope(ctx context.Context, d Database, accountID string) bool {
	org := Organization(ctx)
	return org == "" || d.OrgID == org || d.OrgID == "" && d.AccountID == accountID && PolicySubject(ctx, accountID) != org
}
func databaseNameKey(d Database) string {
	if d.OrgID != "" {
		return d.OrgID + "\x00" + d.Name
	}
	return d.AccountID + "\x00" + d.Name
}
func (s *MemoryStore) sameQuotaOwner(ctx context.Context, a, b Database) bool {
	if b.OrgID != "" && Organization(ctx) == PolicySubject(ctx, b.AccountID) {
		return a.OrgID == b.OrgID
	}
	return s.billingSubject(a) == PolicySubject(ctx, b.AccountID)
}

func (s *MemoryStore) billingSubject(d Database) string {
	if owner := s.billingSubjects[d.ID]; owner != "" {
		return owner
	}
	return d.AccountID
}
