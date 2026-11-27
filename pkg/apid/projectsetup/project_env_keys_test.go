package projectsetup

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeProjectEnvKeys struct {
	environments []string
	secrets      []state.ProjectEnvironmentSecret
	config       json.RawMessage
	configErr    error
}

func (f *fakeProjectEnvKeys) ListProjectEnvironmentSecrets(_ context.Context, _, _, environment string) ([]state.ProjectEnvironmentSecret, error) {
	f.environments = append(f.environments, environment)
	return f.secrets, nil
}

func (f *fakeProjectEnvKeys) ProjectEnvironmentConfigLatest(context.Context, string, string, string) (state.ProjectEnvironmentConfig, error) {
	if f.configErr != nil {
		return state.ProjectEnvironmentConfig{}, f.configErr
	}
	return state.ProjectEnvironmentConfig{Values: f.config}, nil
}

// boa multi-service deploy (2026-11-21): JWT_PUBLIC_KEY set with
// `environment secrets set` was still listed under "Needs a value".
func TestProjectEnvironmentKeysCountSharedValues(t *testing.T) {
	store := &fakeProjectEnvKeys{
		secrets:   []state.ProjectEnvironmentSecret{{Key: "JWT_PUBLIC_KEY"}},
		configErr: state.ErrNotFound,
	}
	keys, err := ProjectEnvironmentKeys(context.Background(), store, "acct", "proj", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !keys["JWT_PUBLIC_KEY"] {
		t.Fatalf("keys = %v", keys)
	}
	if !reflect.DeepEqual(store.environments, []string{"production"}) {
		t.Fatalf("production scope read environments %v", store.environments)
	}
	got := WithoutKeys([]string{"DATABASE_URL", "JWT_PUBLIC_KEY", "LOG_LEVEL"}, keys)
	if !reflect.DeepEqual(got, []string{"DATABASE_URL", "LOG_LEVEL"}) {
		t.Fatalf("WithoutKeys = %v", got)
	}

	store = &fakeProjectEnvKeys{}
	if keys, err := ProjectEnvironmentKeys(context.Background(), store, "acct", "", "staging"); err != nil || len(keys) != 0 || len(store.environments) != 0 {
		t.Fatalf("new project: keys=%v err=%v reads=%v", keys, err, store.environments)
	}
	store = &fakeProjectEnvKeys{configErr: errors.New("db down")}
	if _, err := ProjectEnvironmentKeys(context.Background(), store, "acct", "proj", "staging"); err == nil {
		t.Fatal("config read failure was hidden")
	}
	if !reflect.DeepEqual(store.environments, []string{"staging"}) {
		t.Fatalf("named environment read %v", store.environments)
	}
}
