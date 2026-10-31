package projectsetup

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type envKeyFake struct {
	env     []state.AppEnv
	secrets []state.AppSecret
	err     error
	scopes  []string
}

func (f *envKeyFake) ListAppEnvInScope(_ context.Context, _, _, scope string) ([]state.AppEnv, error) {
	f.scopes = append(f.scopes, scope)
	return f.env, f.err
}

func (f *envKeyFake) ListAppSecretsInScope(_ context.Context, _, _, scope string) ([]state.AppSecret, error) {
	f.scopes = append(f.scopes, scope)
	return f.secrets, f.err
}

func TestUnsetEnvKeys(t *testing.T) {
	ctx := context.Background()
	detected := []string{"LOG_LEVEL", "PORT", "GREGALE_SERVICE_API_URL", "FAAS_APP_ID", "STRIPE_KEY"}
	required := []string{"STRIPE_KEY", "SENTRY_DSN"}

	fresh := &envKeyFake{}
	got, err := UnsetEnvKeys(ctx, fresh, "acct", "default", nil, detected, required)
	if err != nil || !slices.Equal(got, []string{"LOG_LEVEL", "SENTRY_DSN", "STRIPE_KEY"}) {
		t.Fatalf("new workload = %v, %v; want every customer key, platform keys dropped, sorted and deduplicated", got, err)
	}
	if len(fresh.scopes) != 0 {
		t.Fatalf("a new workload read configuration in %v", fresh.scopes)
	}

	configured := &envKeyFake{
		env:     []state.AppEnv{{Key: "LOG_LEVEL", Value: "info"}},
		secrets: []state.AppSecret{{Key: "STRIPE_KEY"}},
	}
	got, err = UnsetEnvKeys(ctx, configured, "acct", "staging", &state.App{ID: "app"}, detected, required)
	if err != nil || !slices.Equal(got, []string{"SENTRY_DSN"}) {
		t.Fatalf("existing app = %v, %v; want only the unconfigured key", got, err)
	}
	if !slices.Equal(configured.scopes, []string{"staging", "staging"}) {
		t.Fatalf("configuration read in scopes %v, want the selected environment", configured.scopes)
	}

	failure := errors.New("store unavailable")
	if _, err := UnsetEnvKeys(ctx, &envKeyFake{err: failure}, "acct", "default", &state.App{ID: "app"}, detected); !errors.Is(err, failure) {
		t.Fatalf("store failure = %v, want it wrapped", err)
	}
	if got, err := UnsetEnvKeys(ctx, &envKeyFake{err: failure}, "acct", "default", &state.App{ID: "app"}, []string{"PORT"}); err != nil || got != nil {
		t.Fatalf("platform-only keys = %v, %v; want no store read and no keys", got, err)
	}
}
