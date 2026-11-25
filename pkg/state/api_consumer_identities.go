package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// OAuth consumers (ADR-20261124094745): a caller signed in through an MCP
// server's oauth-proxy issuer becomes an API consumer of that app, found
// again by the token's issuer and subject.

// Bounds an identity row keeps (its CHECKs mirror them).
const (
	MaxAPIConsumerIdentityIssuerLen  = 2048
	MaxAPIConsumerIdentitySubjectLen = 255
	MaxAPIConsumerIdentityEmailLen   = 254
)

// ErrAPIConsumerOAuthOff reports that an app does not enroll OAuth callers
// as consumers: sign-up is off, or its OAuth consumers are.
var ErrAPIConsumerOAuthOff = errors.New("OAuth consumers are off for this app")

// APIConsumerIdentity ties an OAuth issuer and subject to one consumer of
// an app. Email is display only and never matches anything.
type APIConsumerIdentity struct {
	ID         string
	AccountID  string
	AppID      string
	ConsumerID string
	Issuer     string
	Subject    string
	Email      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// APIConsumerEnrollment is one OAuth caller to enroll. A new consumer joins
// the app's free plan from EffectiveFrom, a UTC minute; enrollments count
// with sign-ups toward the app's daily sign-up limit since DayStart.
type APIConsumerEnrollment struct {
	AccountID     string
	AppID         string
	Issuer        string
	Subject       string
	Email         string
	EffectiveFrom time.Time
	DayStart      time.Time
}

// APIConsumerIdentityStore enrolls and finds OAuth consumers. Optional, like
// the other pricing stores.
type APIConsumerIdentityStore interface {
	// EnrollAPIConsumerIdentity returns the consumer an issuer and subject
	// already map to, else creates the consumer, its identity and its free
	// plan assignment in one step and reports created. ErrAPIConsumerOAuthOff
	// when the app's sign-up or OAuth consumers are off, and
	// ErrAPIConsumerSignupLimit when it took its sign-ups for the day. The
	// consumer may be revoked; callers refuse it.
	EnrollAPIConsumerIdentity(ctx context.Context, in APIConsumerEnrollment) (APIConsumerIdentity, APIConsumer, bool, error)
}

var (
	_ APIConsumerIdentityStore = (*MemStore)(nil)
	_ APIConsumerIdentityStore = (*PgStore)(nil)
)

// ErrAPIConsumerIdentityInvalid wraps an enrollment or link that can never
// succeed as given (malformed IDs, issuer or subject).
var ErrAPIConsumerIdentityInvalid = errors.New("invalid consumer identity")

func validateAPIConsumerEnrollment(in APIConsumerEnrollment) error {
	for name, value := range map[string]string{"account_id": in.AccountID, "app_id": in.AppID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("consumer enrollment: %s must be a UUID: %w", name, ErrAPIConsumerIdentityInvalid)
		}
	}
	switch {
	case in.Issuer == "" || len(in.Issuer) > MaxAPIConsumerIdentityIssuerLen:
		return fmt.Errorf("consumer enrollment: issuer must be 1 to %d bytes: %w", MaxAPIConsumerIdentityIssuerLen, ErrAPIConsumerIdentityInvalid)
	case in.Subject == "" || len(in.Subject) > MaxAPIConsumerIdentitySubjectLen || !utf8.ValidString(in.Subject):
		return fmt.Errorf("consumer enrollment: subject must be 1 to %d bytes of UTF-8: %w", MaxAPIConsumerIdentitySubjectLen,
			ErrAPIConsumerIdentityInvalid)
	case in.EffectiveFrom.IsZero() || !in.EffectiveFrom.Equal(in.EffectiveFrom.UTC().Truncate(time.Minute)):
		return errors.New("consumer enrollment: effective_from must be a UTC minute")
	}
	return nil
}

// apiConsumerIdentityEmail keeps a display email only when it fits.
func apiConsumerIdentityEmail(email string) string {
	email = strings.TrimSpace(email)
	if len(email) < 3 || len(email) > MaxAPIConsumerIdentityEmailLen || !utf8.ValidString(email) || !strings.Contains(email, "@") {
		return ""
	}
	return email
}

// apiConsumerIdentityRef is the external ref of an enrolled consumer: a
// fixed-length digest of issuer and subject, so any subject fits.
func apiConsumerIdentityRef(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\n" + subject))
	return "oauth:" + hex.EncodeToString(sum[:])
}

// apiConsumerIdentityName names an enrolled consumer after its email, else
// its subject.
func apiConsumerIdentityName(email, subject string) string {
	name := email
	if name == "" {
		name = "OAuth " + subject
	}
	if utf8.RuneCountInString(name) > 128 {
		name = string([]rune(name)[:128])
	}
	return name
}

// apiConsumerOAuthEnrollable reports whether settings enroll OAuth callers.
func apiConsumerOAuthEnrollable(s APIConsumerSignupSettings) bool {
	return s.Enabled && s.OAuthConsumers && s.FreePlanID != ""
}

func (m *MemStore) EnrollAPIConsumerIdentity(_ context.Context, in APIConsumerEnrollment) (APIConsumerIdentity, APIConsumer, bool, error) {
	if err := validateAPIConsumerEnrollment(in); err != nil {
		return APIConsumerIdentity{}, APIConsumer{}, false, err
	}
	in.Email = apiConsumerIdentityEmail(in.Email)
	m.mu.Lock()
	defer m.mu.Unlock()
	// Turning sign-up or OAuth consumers off stops new enrollments only:
	// callers who already joined stay metered and limited.
	for _, id := range m.apiConsumerIdentities {
		if id.AccountID == in.AccountID && id.AppID == in.AppID && id.Issuer == in.Issuer && id.Subject == in.Subject {
			c, ok := m.apiConsumers[id.ConsumerID]
			if !ok {
				return APIConsumerIdentity{}, APIConsumer{}, false, ErrNotFound
			}
			return id, c, false, nil
		}
	}
	settings, ok := m.apiConsumerSignupSettings[in.AppID]
	if !ok || settings.AccountID != in.AccountID || !apiConsumerOAuthEnrollable(settings) {
		return APIConsumerIdentity{}, APIConsumer{}, false, ErrAPIConsumerOAuthOff
	}
	now := time.Now().UTC()
	ref := apiConsumerIdentityRef(in.Issuer, in.Subject)
	var c APIConsumer
	for _, existing := range m.apiConsumers {
		// A consumer whose identity row is gone keeps its ref unless a link
		// closed it; re-attaching it is not a new sign-up.
		if existing.AppID == in.AppID && existing.ExternalRef == ref {
			c = existing
		}
	}
	created := c.ID == ""
	if created {
		taken := 0
		for _, s := range m.apiConsumerSignups {
			if s.AppID == in.AppID && !s.CreatedAt.Before(in.DayStart) {
				taken++
			}
		}
		for _, id := range m.apiConsumerIdentities {
			if id.AppID == in.AppID && !id.CreatedAt.Before(in.DayStart) {
				taken++
			}
		}
		if taken >= settings.DailyLimit {
			return APIConsumerIdentity{}, APIConsumer{}, false, ErrAPIConsumerSignupLimit
		}
		if plan, ok := m.apiConsumerPlans[settings.FreePlanID]; !ok || plan.AccountID != in.AccountID || plan.AppID != in.AppID {
			return APIConsumerIdentity{}, APIConsumer{}, false, ErrNotFound
		}
		c = APIConsumer{ID: uuid.NewString(), AccountID: in.AccountID, AppID: in.AppID, ExternalRef: ref,
			Name: apiConsumerIdentityName(in.Email, in.Subject), Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now}
		m.apiConsumers[c.ID] = c
		a := APIConsumerPlanAssignment{ID: uuid.NewString(), AccountID: in.AccountID, AppID: in.AppID, ConsumerID: c.ID,
			PlanID: settings.FreePlanID, EffectiveFrom: in.EffectiveFrom.UTC(), CreatedAt: now}
		m.apiConsumerPlanAssignments[a.ID] = a
	}
	if m.apiConsumerIdentities == nil {
		m.apiConsumerIdentities = map[string]APIConsumerIdentity{}
	}
	id := APIConsumerIdentity{ID: uuid.NewString(), AccountID: in.AccountID, AppID: in.AppID, ConsumerID: c.ID, Issuer: in.Issuer,
		Subject: in.Subject, Email: in.Email, CreatedAt: now, UpdatedAt: now}
	m.apiConsumerIdentities[id.ID] = id
	return id, c, created, nil
}
