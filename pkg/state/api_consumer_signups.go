package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Self-service sign-up for an app's API customers (ADR-20261021143000).

// APIConsumerSignupProviderStripe is the only sign-up provider: Stripe
// Checkout saves a card without charging it.
const APIConsumerSignupProviderStripe = BillingProviderStripe

// Bounds the settings and sign-up rows keep (their CHECKs mirror them).
const (
	MaxAPIConsumerSignupPlans     = 20
	MaxAPIConsumerSignupScopes    = 4
	MaxAPIConsumerSignupNameLen   = 80
	MaxAPIConsumerSignupURLLen    = 2048
	MaxAPIConsumerSignupTrialDays = 365
	MaxAPIConsumerSignupDaily     = 1000
)

// ErrAPIConsumerSignupLimit reports that an app took its daily sign-ups.
var ErrAPIConsumerSignupLimit = errors.New("the app took its sign-ups for today")

// APIConsumerSignupSettings turn sign-up on for one app. An empty PlanIDs
// offers only the app's default plan.
type APIConsumerSignupSettings struct {
	AppID       string
	AccountID   string
	Enabled     bool
	Provider    string
	PlanIDs     []string
	KeyScopes   []string
	DisplayName string
	TermsURL    string
	ReturnURL   string
	TrialDays   int
	DailyLimit  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// APIConsumerSignupStatus is a sign-up's state: pending until its consumer
// exists, then completed.
type APIConsumerSignupStatus string

const (
	APIConsumerSignupPending   APIConsumerSignupStatus = "pending"
	APIConsumerSignupCompleted APIConsumerSignupStatus = "completed"
)

// APIConsumerSignup is one customer's sign-up. ClaimHash is the SHA-256 of
// the claim token only the customer holds; KeyID is the key the last
// completion issued.
type APIConsumerSignup struct {
	ID                 string
	AccountID          string
	AppID              string
	Email              string
	Name               string
	PlanID             string
	Provider           string
	Livemode           bool
	ProviderCustomerID string
	CheckoutSessionID  string
	ClaimHash          []byte
	Status             APIConsumerSignupStatus
	ConsumerID         string
	KeyID              string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	CompletedAt        *time.Time
}

// Expired reports whether a pending sign-up can no longer complete.
func (s APIConsumerSignup) Expired(at time.Time) bool {
	return s.Status == APIConsumerSignupPending && !at.Before(s.ExpiresAt)
}

// APIConsumerSignupCompletion creates a sign-up's consumer: its plan
// assignment starts at EffectiveFrom, a UTC minute, and so does its trial
// when TrialDays is set.
type APIConsumerSignupCompletion struct {
	AccountID     string
	SignupID      string
	EffectiveFrom time.Time
	TrialDays     int
}

// APIConsumerSignupStore persists sign-up settings and sign-ups. Optional,
// like the other pricing stores.
type APIConsumerSignupStore interface {
	PutAPIConsumerSignupSettings(ctx context.Context, s APIConsumerSignupSettings) (APIConsumerSignupSettings, error)
	GetAPIConsumerSignupSettings(ctx context.Context, accountID, appID string) (APIConsumerSignupSettings, error)
	DeleteAPIConsumerSignupSettings(ctx context.Context, accountID, appID string) error
	// CreateAPIConsumerSignup records a pending sign-up unless the app's
	// settings are gone (ErrNotFound) or it already took dailyLimit
	// sign-ups since dayStart (ErrAPIConsumerSignupLimit).
	CreateAPIConsumerSignup(ctx context.Context, s APIConsumerSignup, dayStart time.Time, dailyLimit int) (APIConsumerSignup, error)
	// SetAPIConsumerSignupCheckout records the provider customer and
	// checkout of a pending sign-up.
	SetAPIConsumerSignupCheckout(ctx context.Context, accountID, signupID, customerID, sessionID string) error
	GetAPIConsumerSignup(ctx context.Context, accountID, signupID string) (APIConsumerSignup, error)
	// ListAPIConsumerSignups returns an app's sign-ups, newest first.
	ListAPIConsumerSignups(ctx context.Context, accountID, appID string, limit int) ([]APIConsumerSignup, error)
	// CompleteAPIConsumerSignup creates the sign-up's consumer, named after
	// the customer with the provider customer as its external ref, with its
	// plan assignment and trial, in one step. A completed sign-up returns
	// its consumer and created=false. ErrConflict when the checkout has not
	// started or the external ref is taken.
	CompleteAPIConsumerSignup(ctx context.Context, in APIConsumerSignupCompletion) (APIConsumer, bool, error)
	// SetAPIConsumerSignupKey records the key a completion issued and
	// returns the one it replaces, or "".
	SetAPIConsumerSignupKey(ctx context.Context, accountID, signupID, keyID string) (string, error)
	// DeleteStaleAPIConsumerSignups deletes pending sign-ups that expired
	// before before.
	DeleteStaleAPIConsumerSignups(ctx context.Context, before time.Time) (int, error)
}

var (
	_ APIConsumerSignupStore = (*MemStore)(nil)
	_ APIConsumerSignupStore = (*PgStore)(nil)
)

func validateAPIConsumerSignupSettings(s APIConsumerSignupSettings) error {
	for name, value := range map[string]string{"account_id": s.AccountID, "app_id": s.AppID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("consumer sign-up settings: %s must be a UUID: %w", name, err)
		}
	}
	for _, id := range s.PlanIDs {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("consumer sign-up settings: plan IDs must be UUIDs: %w", err)
		}
	}
	switch {
	case s.Provider != APIConsumerSignupProviderStripe:
		return errors.New("consumer sign-up settings: provider must be stripe")
	case len(s.PlanIDs) > MaxAPIConsumerSignupPlans:
		return fmt.Errorf("consumer sign-up settings: at most %d plans", MaxAPIConsumerSignupPlans)
	case len(s.KeyScopes) == 0 || len(s.KeyScopes) > MaxAPIConsumerSignupScopes:
		return fmt.Errorf("consumer sign-up settings: 1 to %d key scopes", MaxAPIConsumerSignupScopes)
	case s.DisplayName == "" || len([]rune(s.DisplayName)) > MaxAPIConsumerSignupNameLen:
		return fmt.Errorf("consumer sign-up settings: display name must be 1 to %d characters", MaxAPIConsumerSignupNameLen)
	case len(s.TermsURL) > MaxAPIConsumerSignupURLLen || len(s.ReturnURL) > MaxAPIConsumerSignupURLLen:
		return fmt.Errorf("consumer sign-up settings: URLs must be at most %d characters", MaxAPIConsumerSignupURLLen)
	case s.TrialDays < 0 || s.TrialDays > MaxAPIConsumerSignupTrialDays:
		return fmt.Errorf("consumer sign-up settings: trial days must be 0 to %d", MaxAPIConsumerSignupTrialDays)
	case s.DailyLimit < 1 || s.DailyLimit > MaxAPIConsumerSignupDaily:
		return fmt.Errorf("consumer sign-up settings: daily limit must be 1 to %d", MaxAPIConsumerSignupDaily)
	}
	return nil
}

func validateAPIConsumerSignup(s APIConsumerSignup) error {
	for name, value := range map[string]string{"account_id": s.AccountID, "app_id": s.AppID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("consumer sign-up: %s must be a UUID: %w", name, err)
		}
	}
	if s.PlanID != "" {
		if _, err := uuid.Parse(s.PlanID); err != nil {
			return fmt.Errorf("consumer sign-up: plan_id must be a UUID: %w", err)
		}
	}
	switch {
	case s.Provider != APIConsumerSignupProviderStripe:
		return errors.New("consumer sign-up: provider must be stripe")
	case len(s.Email) < 3 || len(s.Email) > 254:
		return errors.New("consumer sign-up: email must be 3 to 254 characters")
	case s.Name == "" || len([]rune(s.Name)) > 128:
		return errors.New("consumer sign-up: name must be 1 to 128 characters")
	case len(s.ClaimHash) != 32:
		return errors.New("consumer sign-up: claim hash must be 32 bytes")
	case !s.ExpiresAt.After(time.Now()):
		return errors.New("consumer sign-up: expires_at must be in the future")
	}
	return nil
}

func cloneAPIConsumerSignupSettings(s APIConsumerSignupSettings) APIConsumerSignupSettings {
	s.PlanIDs, s.KeyScopes = slices.Clone(s.PlanIDs), slices.Clone(s.KeyScopes)
	if s.PlanIDs == nil {
		s.PlanIDs = []string{}
	}
	return s
}

func (m *MemStore) PutAPIConsumerSignupSettings(_ context.Context, s APIConsumerSignupSettings) (APIConsumerSignupSettings, error) {
	if err := validateAPIConsumerSignupSettings(s); err != nil {
		return APIConsumerSignupSettings{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[s.AppID]
	if !ok || app.AccountID != s.AccountID {
		return APIConsumerSignupSettings{}, ErrNotFound
	}
	if m.apiConsumerSignupSettings == nil {
		m.apiConsumerSignupSettings = map[string]APIConsumerSignupSettings{}
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now
	if prior, ok := m.apiConsumerSignupSettings[s.AppID]; ok {
		s.CreatedAt = prior.CreatedAt
	}
	s = cloneAPIConsumerSignupSettings(s)
	m.apiConsumerSignupSettings[s.AppID] = s
	return cloneAPIConsumerSignupSettings(s), nil
}

func (m *MemStore) GetAPIConsumerSignupSettings(_ context.Context, accountID, appID string) (APIConsumerSignupSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignupSettings[appID]
	if !ok || s.AccountID != accountID {
		return APIConsumerSignupSettings{}, ErrNotFound
	}
	return cloneAPIConsumerSignupSettings(s), nil
}

func (m *MemStore) DeleteAPIConsumerSignupSettings(_ context.Context, accountID, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignupSettings[appID]
	if !ok || s.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.apiConsumerSignupSettings, appID)
	return nil
}

func (m *MemStore) CreateAPIConsumerSignup(_ context.Context, s APIConsumerSignup, dayStart time.Time, dailyLimit int) (APIConsumerSignup, error) {
	if err := validateAPIConsumerSignup(s); err != nil {
		return APIConsumerSignup{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if settings, ok := m.apiConsumerSignupSettings[s.AppID]; !ok || settings.AccountID != s.AccountID {
		return APIConsumerSignup{}, ErrNotFound
	}
	taken := 0
	for _, other := range m.apiConsumerSignups {
		if other.AppID == s.AppID && !other.CreatedAt.Before(dayStart) {
			taken++
		}
	}
	if taken >= dailyLimit {
		return APIConsumerSignup{}, ErrAPIConsumerSignupLimit
	}
	if m.apiConsumerSignups == nil {
		m.apiConsumerSignups = map[string]APIConsumerSignup{}
	}
	s.ID, s.Status, s.CreatedAt = uuid.NewString(), APIConsumerSignupPending, time.Now().UTC()
	s.ConsumerID, s.KeyID, s.CompletedAt, s.ProviderCustomerID, s.CheckoutSessionID = "", "", nil, "", ""
	s.ClaimHash = slices.Clone(s.ClaimHash)
	m.apiConsumerSignups[s.ID] = s
	return s, nil
}

func (m *MemStore) SetAPIConsumerSignupCheckout(_ context.Context, accountID, signupID, customerID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignups[signupID]
	if !ok || s.AccountID != accountID {
		return ErrNotFound
	}
	if s.Status != APIConsumerSignupPending {
		return ErrConflict
	}
	s.ProviderCustomerID, s.CheckoutSessionID = customerID, sessionID
	m.apiConsumerSignups[signupID] = s
	return nil
}

func (m *MemStore) GetAPIConsumerSignup(_ context.Context, accountID, signupID string) (APIConsumerSignup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignups[signupID]
	if !ok || s.AccountID != accountID {
		return APIConsumerSignup{}, ErrNotFound
	}
	return s, nil
}

func (m *MemStore) ListAPIConsumerSignups(_ context.Context, accountID, appID string, limit int) ([]APIConsumerSignup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumerSignup
	for _, s := range m.apiConsumerSignups {
		if s.AccountID == accountID && s.AppID == appID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) CompleteAPIConsumerSignup(_ context.Context, in APIConsumerSignupCompletion) (APIConsumer, bool, error) {
	if in.EffectiveFrom.IsZero() || !in.EffectiveFrom.Equal(in.EffectiveFrom.UTC().Truncate(time.Minute)) || in.TrialDays < 0 {
		return APIConsumer{}, false, errors.New("consumer sign-up completion: effective_from must be a UTC minute")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignups[in.SignupID]
	if !ok || s.AccountID != in.AccountID {
		return APIConsumer{}, false, ErrNotFound
	}
	if s.Status == APIConsumerSignupCompleted {
		c, ok := m.apiConsumers[s.ConsumerID]
		if !ok {
			return APIConsumer{}, false, ErrNotFound
		}
		return c, false, nil
	}
	if s.ProviderCustomerID == "" {
		return APIConsumer{}, false, ErrConflict
	}
	for _, c := range m.apiConsumers {
		if c.AppID == s.AppID && c.ExternalRef == s.ProviderCustomerID {
			return APIConsumer{}, false, ErrConflict
		}
	}
	if s.PlanID != "" {
		if plan, ok := m.apiConsumerPlans[s.PlanID]; !ok || plan.AccountID != s.AccountID || plan.AppID != s.AppID {
			return APIConsumer{}, false, ErrNotFound
		}
	}
	now := time.Now().UTC()
	c := APIConsumer{ID: uuid.NewString(), AccountID: s.AccountID, AppID: s.AppID, ExternalRef: s.ProviderCustomerID, Name: s.Name,
		Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now}
	m.apiConsumers[c.ID] = c
	if s.PlanID != "" {
		a := APIConsumerPlanAssignment{ID: uuid.NewString(), AccountID: s.AccountID, AppID: s.AppID, ConsumerID: c.ID, PlanID: s.PlanID,
			EffectiveFrom: in.EffectiveFrom.UTC(), CreatedAt: now}
		m.apiConsumerPlanAssignments[a.ID] = a
	}
	if in.TrialDays > 0 {
		if m.apiConsumerTrials == nil {
			m.apiConsumerTrials = map[string]APIConsumerTrial{}
		}
		start := in.EffectiveFrom.UTC()
		m.apiConsumerTrials[apiConsumerTrialKey(s.AccountID, s.AppID, c.ID)] = APIConsumerTrial{AccountID: s.AccountID, AppID: s.AppID,
			ConsumerID: c.ID, StartsAt: start, EndsAt: start.AddDate(0, 0, in.TrialDays), CreatedAt: now, UpdatedAt: now}
	}
	s.Status, s.ConsumerID, s.CompletedAt = APIConsumerSignupCompleted, c.ID, &now
	m.apiConsumerSignups[s.ID] = s
	return c, true, nil
}

func (m *MemStore) SetAPIConsumerSignupKey(_ context.Context, accountID, signupID, keyID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.apiConsumerSignups[signupID]
	if !ok || s.AccountID != accountID {
		return "", ErrNotFound
	}
	previous := s.KeyID
	s.KeyID = keyID
	m.apiConsumerSignups[signupID] = s
	return previous, nil
}

func (m *MemStore) DeleteStaleAPIConsumerSignups(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for id, s := range m.apiConsumerSignups {
		if s.Status == APIConsumerSignupPending && s.ExpiresAt.Before(before) {
			delete(m.apiConsumerSignups, id)
			deleted++
		}
	}
	return deleted, nil
}
