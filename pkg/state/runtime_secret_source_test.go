// adr: 20261024140000
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestResolveRuntimeSecretSources(t *testing.T) {
	rows := []state.AppSecret{
		{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")},
		{Key: "REAL_TOKEN", Ciphertext: []byte("sealed-real")},
		{Key: "SHADOWED", Ciphertext: []byte("sealed-own")},
		{Key: "MIGRATE_URL", Ciphertext: []byte("sealed-migrate"), ManagedPostgresBindingID: "binding", ManagedPostgresAccess: "migration"},
	}
	inherited := []state.ProjectEnvironmentSecret{{Key: "SHARED", Ciphertext: []byte("sealed-shared")}}
	snapshot := func(grants string) state.RuntimeAppValuesSnapshot {
		s := state.RuntimeAppValuesSnapshot{Secrets: rows, InheritedSecrets: inherited}
		if grants != "" {
			s.SecretGrants.OverrideEnvSecrets = json.RawMessage(grants)
		}
		return s
	}
	aliases := state.AppEnvironmentSecretIntent{References: map[string]string{"ALIAS": "secret:REAL_TOKEN", "NPM_TOKEN": "secret:NPM_TOKEN"}, SuppressedKeys: []string{"SHADOWED"}}
	for _, tc := range []struct {
		name        string
		grants      string
		intent      state.AppEnvironmentSecretIntent
		keys        []string
		want        []state.RuntimeSecretSource
		wantMissing []string
		wantErr     error
	}{
		{"own secret by its name", "", state.AppEnvironmentSecretIntent{}, []string{"NPM_TOKEN", "NPM_TOKEN"},
			[]state.RuntimeSecretSource{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")}}, nil, nil},
		{"alias names its source", "", aliases, []string{"ALIAS"},
			[]state.RuntimeSecretSource{{Key: "ALIAS", SourceKey: "REAL_TOKEN", Ciphertext: []byte("sealed-real")}}, nil, nil},
		{"an environment reference binds even its own name", "", aliases, []string{"NPM_TOKEN"},
			[]state.RuntimeSecretSource{{Key: "NPM_TOKEN", SourceKey: "NPM_TOKEN", Ciphertext: []byte("sealed-npm")}}, nil, nil},
		{"project environment secret", "", state.AppEnvironmentSecretIntent{}, []string{"SHARED"},
			[]state.RuntimeSecretSource{{Key: "SHARED", SourceKey: "SHARED", Ciphertext: []byte("sealed-shared")}}, nil, nil},
		{"a suppressed key is not delivered", "", aliases, []string{"SHADOWED"}, nil, []string{"SHADOWED"}, nil},
		{"explicit grants inherit nothing", `{"GRANTED":"secret:REAL_TOKEN"}`, state.AppEnvironmentSecretIntent{}, []string{"GRANTED", "SHARED", "NPM_TOKEN"},
			[]state.RuntimeSecretSource{{Key: "GRANTED", SourceKey: "REAL_TOKEN", Ciphertext: []byte("sealed-real")}}, []string{"SHARED", "NPM_TOKEN"}, nil},
		{"unknown key", "", state.AppEnvironmentSecretIntent{}, []string{"NOPE"}, nil, []string{"NOPE"}, nil},
		{"release-only credential", "", state.AppEnvironmentSecretIntent{}, []string{"MIGRATE_URL"}, nil, nil, state.ErrRuntimeSecretReleaseOnly},
		{"corrupt grants", `{"bad`, state.AppEnvironmentSecretIntent{}, []string{"NPM_TOKEN"}, nil, nil, state.ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, missing, err := state.ResolveRuntimeSecretSources(snapshot(tc.grants), tc.intent, tc.keys)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(missing, tc.wantMissing) {
				t.Fatalf("got %+v missing %v err %v; want %+v missing %v", got, missing, err, tc.want, tc.wantMissing)
			}
		})
	}
	// Results never share the snapshot's ciphertext.
	got, _, _ := state.ResolveRuntimeSecretSources(snapshot(""), state.AppEnvironmentSecretIntent{}, []string{"NPM_TOKEN"})
	got[0].Ciphertext[0] = '!'
	if string(rows[0].Ciphertext) != "sealed-npm" {
		t.Fatal("resolved ciphertext aliases the snapshot")
	}
}
