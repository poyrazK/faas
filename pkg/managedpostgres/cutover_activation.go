package managedpostgres

import (
	"context"
	"errors"
	"time"
)

// CutoverActivation asks to move an app from a verified cutover's source
// bindings to its target. Only a rollback database switch activates a
// cutover (ADR-20261023181931); plain cutovers keep ADR-1333's bar. FencedAt
// is the admission fence the switch installed, and LiveDeploymentID the
// release that must be the scope's only serving deployment.
type CutoverActivation struct {
	AccountID, CutoverID string
	FencedAt             time.Time
	LiveDeploymentID     string
	Now                  time.Time
}

// Activation refusal codes. ActivationVerificationStale and
// ActivationDrainIncomplete can clear on a later attempt; the others mean
// the switch must abort.
const (
	ActivationNotVerified       = "cutover_not_verified"
	ActivationVerificationStale = "verification_stale"
	ActivationFenceChanged      = "fence_changed"
	ActivationDrainIncomplete   = "drain_incomplete"
	ActivationReleaseChanged    = "release_changed"
	ActivationBindingsChanged   = "bindings_changed"
	ActivationDatabasesChanged  = "databases_changed"
	ActivationSourceFenced      = "source_fence_active"
)

// ActivationBlockedError refuses an activation with a stable code.
type ActivationBlockedError struct{ Code string }

func (e *ActivationBlockedError) Error() string {
	return "managed postgres cutover activation blocked: " + e.Code
}

// ActivationBlockedCode returns the refusal code of err, or "".
func ActivationBlockedCode(err error) string {
	var blocked *ActivationBlockedError
	if errors.As(err, &blocked) {
		return blocked.Code
	}
	return ""
}

func activationBlocked(code string) error { return &ActivationBlockedError{Code: code} }

// ActivatedCutover reports what an activation changed. SecretsPublished is
// true when the store published the target credentials and removed the
// source ones in the activation transaction; otherwise the service does it.
type ActivatedCutover struct {
	RetiredSources   []Binding
	Published        []Binding
	Sealed           []SealedCredential
	SecretsPublished bool
}

// CutoverActivationStore fences admission, activates and resumes. The
// PostgreSQL store does each in one transaction under the cutover locks.
type CutoverActivationStore interface {
	FenceCutoverAdmission(ctx context.Context, account, id string) (CutoverAdmissionFence, error)
	ActivateCutover(ctx context.Context, a CutoverActivation) (ActivatedCutover, error)
	ReleaseCutoverAdmission(ctx context.Context, account, id string) error
}

// SealedCredentialPublisher writes an already sealed credential to the app's
// secrets. Sinks implement it for stores that cannot publish in their own
// activation transaction.
type SealedCredentialPublisher interface {
	PublishSealed(ctx context.Context, b Binding, sealed SealedCredential) error
}

func (s *CutoverService) activationStore() (CutoverActivationStore, error) {
	store, ok := s.store.(CutoverActivationStore)
	if !ok {
		return nil, ErrUnavailable
	}
	return store, nil
}

// Fence installs the app-wide admission fence for a freshly verified
// cutover (ADR-466). It is idempotent for the same cutover.
func (s *CutoverService) Fence(ctx context.Context, account, id string) (CutoverAdmissionFence, error) {
	store, err := s.activationStore()
	if err != nil {
		return CutoverAdmissionFence{}, err
	}
	return store.FenceCutoverAdmission(ctx, account, id)
}

// Activate moves the app's bindings to the cutover's target. It is
// one-way: a cancelled or failed switch must abort before calling it.
func (s *CutoverService) Activate(ctx context.Context, a CutoverActivation) (ActivatedCutover, error) {
	store, err := s.activationStore()
	if err != nil {
		return ActivatedCutover{}, err
	}
	if a.Now.IsZero() {
		a.Now = s.bindings.now()
	}
	out, err := store.ActivateCutover(ctx, a)
	if err != nil || out.SecretsPublished {
		return out, err
	}
	publisher, ok := s.bindings.sink.(SealedCredentialPublisher)
	if !ok {
		return out, ErrUnavailable
	}
	// The source secret holds the key until it is deleted.
	for _, b := range out.RetiredSources {
		if err := s.bindings.sink.Delete(ctx, b); err != nil {
			return out, err
		}
	}
	for i, b := range out.Published {
		if err := publisher.PublishSealed(ctx, b, out.Sealed[i]); err != nil {
			return out, err
		}
	}
	out.SecretsPublished = true
	return out, nil
}

// Resume lifts the admission fence of an activated cutover. It is
// idempotent.
func (s *CutoverService) Resume(ctx context.Context, account, id string) error {
	store, err := s.activationStore()
	if err != nil {
		return err
	}
	return store.ReleaseCutoverAdmission(ctx, account, id)
}

// Fenced reports the cutover whose fence holds an app, for the in-memory
// store only; the PostgreSQL fence lives on the app row.
func (s *MemoryStore) Fenced(appID string) (CutoverAdmissionFence, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fences[appID]
	return f, ok
}

func (s *MemoryStore) FenceCutoverAdmission(_ context.Context, account, id string) (CutoverAdmissionFence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return CutoverAdmissionFence{}, ErrNotFound
	}
	if existing, fenced := s.fences[c.AppID]; fenced {
		if existing.CutoverID != id {
			return CutoverAdmissionFence{}, ErrConflict
		}
		return existing, nil
	}
	now := time.Now().UTC()
	if !c.VerificationFresh(now) {
		return CutoverAdmissionFence{}, ErrConflict
	}
	f := CutoverAdmissionFence{CutoverID: id, AppID: c.AppID, FencedAt: now}
	s.fences[c.AppID] = f
	return f, nil
}

func (s *MemoryStore) ActivateCutover(_ context.Context, a CutoverActivation) (ActivatedCutover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[a.CutoverID]
	if !ok || c.AccountID != a.AccountID {
		return ActivatedCutover{}, ErrNotFound
	}
	if c.State != CutoverVerified {
		return ActivatedCutover{}, activationBlocked(ActivationNotVerified)
	}
	if f, fenced := s.fences[c.AppID]; !fenced || f.CutoverID != c.ID || !f.FencedAt.Equal(a.FencedAt) {
		return ActivatedCutover{}, activationBlocked(ActivationFenceChanged)
	}
	if !c.VerificationFresh(a.Now) {
		return ActivatedCutover{}, activationBlocked(ActivationVerificationStale)
	}
	target, ok := s.databases[c.Target.ID]
	if !ok || target.State != StateReady || target.ProviderResourceID != c.Target.ProviderResourceID {
		return ActivatedCutover{}, activationBlocked(ActivationDatabasesChanged)
	}
	var out ActivatedCutover
	for _, m := range c.Credentials {
		source, ok := s.bindings[m.SourceBindingID]
		if !ok || source.State != BindingStateReady || source.CredentialGeneration != m.SourceCredentialGeneration {
			return ActivatedCutover{}, activationBlocked(ActivationBindingsChanged)
		}
		out.RetiredSources = append(out.RetiredSources, source)
	}
	for i, m := range c.Credentials {
		source := out.RetiredSources[i]
		source.State, source.UpdatedAt, source.RetryAt = BindingStateDeleting, a.Now, a.Now
		s.bindings[source.ID] = source
		published := cutoverBinding(c, m)
		published.DatabaseAccountID = target.AccountID
		published.State, published.ProviderIdentityID, published.CredentialRef = BindingStateReady, m.Sealed.ProviderIdentityID, m.Sealed.Ref
		published.CreatedAt, published.UpdatedAt, published.RetryAt = a.Now, a.Now, a.Now
		s.bindings[published.ID] = published
		out.Published = append(out.Published, published)
		out.Sealed = append(out.Sealed, m.Sealed)
		c.Credentials[i].State, c.Credentials[i].Sealed, c.Credentials[i].VerifiedAt = "published", SealedCredential{}, time.Time{}
	}
	c.State, c.UpdatedAt = CutoverActivated, a.Now
	s.cutovers[c.ID] = c
	return out, nil
}

func (s *MemoryStore) ReleaseCutoverAdmission(_ context.Context, account, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return ErrNotFound
	}
	if c.State != CutoverActivated {
		return ErrConflict
	}
	if f, fenced := s.fences[c.AppID]; fenced && f.CutoverID == id {
		delete(s.fences, c.AppID)
	}
	return nil
}
