package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// environmentConsumerAuthStore answers environment lookups from its own key,
// as the durable store answers a stage host (ADR-20261104120000 amendment
// 2026-11-22).
type environmentConsumerAuthStore struct {
	fakeConsumerAuthStore
	environmentKey ConsumerAuthKey
	environmentErr error
	environments   []string
	appLookups     int
}

func (s *environmentConsumerAuthStore) ConsumerKeyByAppAndPrefix(context.Context, string, string, string) (ConsumerAuthKey, error) {
	s.appLookups++
	return s.key, s.keyErr
}

func (s *environmentConsumerAuthStore) ConsumerKeyForEnvironment(_ context.Context, _, _, environmentID, _ string) (ConsumerAuthKey, error) {
	s.environments = append(s.environments, environmentID)
	return s.environmentKey, s.environmentErr
}

// adr: 20261104120000 — a stage host reads its own consumer keys; a production
// host reads production's.
func TestEnforceConsumerAuthEnvironmentHostUsesEnvironmentKeys(t *testing.T) {
	for _, tc := range []struct {
		name            string
		environmentID   string
		environmentErr  error
		wantOK          bool
		wantEnvironment bool
	}{
		{"production host reads production keys", "", nil, true, false},
		{"stage host reads the stage's keys", "env-1", nil, true, true},
		{"stage host refuses a key it does not hold", "env-1", ErrConsumerAuthNotFound, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, token := consumerAuthFixture()
			store := &environmentConsumerAuthStore{fakeConsumerAuthStore: *base, environmentKey: base.key, environmentErr: tc.environmentErr}
			h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
			app := App{ID: "app-1", AccountID: "acct-1", PublicEnvironmentID: tc.environmentID, ConsumerAuthMode: api.ConsumerAuthModeRequired}
			rr, _, ok := runConsumerAuthGate(t, h, app, http.MethodGet, token)
			if ok != tc.wantOK || (!ok && rr.Code != http.StatusUnauthorized) {
				t.Fatalf("ok=%v status=%d, want ok=%v", ok, rr.Code, tc.wantOK)
			}
			if got := len(store.environments) == 1 && store.environments[0] == "env-1"; got != tc.wantEnvironment {
				t.Fatalf("environment lookups = %v, want environment lookup %v", store.environments, tc.wantEnvironment)
			}
			if tc.wantEnvironment && store.appLookups != 0 {
				t.Fatalf("stage host also read production keys %d times", store.appLookups)
			}
		})
	}
}

// adr: 20261104120000 — a stage's consumers are not billed: a request with a
// stage key carries no consumer in its usage and skips plan admission, while
// a production key keeps both.
func TestEnforceConsumerAuthStageKeyIsUnbilled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		environment string
		wantStage   bool
	}{
		{"production key", "", false},
		{"stage key", "env-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, token := consumerAuthFixture()
			consumerID := uuid.NewString()
			base.key.ConsumerID, base.consumer.ID = consumerID, consumerID
			key := base.key
			key.EnvironmentID = tc.environment
			store := &environmentConsumerAuthStore{fakeConsumerAuthStore: *base, environmentKey: key}
			store.key = key
			h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
			app := App{ID: "app-1", AccountID: "acct-1", PublicEnvironmentID: tc.environment, ConsumerAuthMode: api.ConsumerAuthModeRequired}
			_, r, ok := runConsumerAuthGate(t, h, app, http.MethodGet, token)
			if !ok {
				t.Fatal("key refused")
			}
			if got := authenticatedFrom(r.Context()).StageConsumer; got != tc.wantStage {
				t.Fatalf("StageConsumer = %v, want %v", got, tc.wantStage)
			}
			attributed := requestUsageAttribution(r).ConsumerID
			if tc.wantStage && attributed != "" {
				t.Fatalf("stage usage attributed to consumer %q", attributed)
			}
			if !tc.wantStage && attributed != consumerID {
				t.Fatalf("production usage consumer = %q, want %s", attributed, consumerID)
			}
		})
	}
}
