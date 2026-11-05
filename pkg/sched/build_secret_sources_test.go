// adr: 20261024140000
package sched_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Builds resolve their secrets with state.ResolveRuntimeSecretSources because
// builderd cannot link the scheduler. Every key must resolve exactly as the
// wake delivers it to the serving process: same ciphertext, same source key,
// and nothing the wake leaves out.
func TestBuildSecretSourcesMatchWakeDelivery(t *testing.T) {
	const account, app, scope = "acct", "app", "default"
	row := func(key, cipher string) state.AppSecret {
		return state.AppSecret{AccountID: account, AppID: app, Scope: scope, Key: key, Ciphertext: []byte(cipher)}
	}
	rows := []state.AppSecret{row("NPM_TOKEN", "sealed-npm"), row("REAL_TOKEN", "sealed-real"), row("OWN_SHARED", "sealed-own-shared"), row("HIDDEN", "sealed-hidden")}
	inherited := []state.ProjectEnvironmentSecret{{Key: "SHARED", Ciphertext: []byte("sealed-shared")}, {Key: "PROJECT_ONLY", Ciphertext: []byte("sealed-project")}}
	for _, tc := range []struct {
		name   string
		grants string
		intent state.AppEnvironmentSecretIntent
	}{
		{"every scoped secret", "", state.AppEnvironmentSecretIntent{}},
		{"aliases and suppressions", "", state.AppEnvironmentSecretIntent{
			References:     map[string]string{"ALIAS": "secret:REAL_TOKEN", "NPM_TOKEN": "secret:NPM_TOKEN", "SHARED": "secret:OWN_SHARED"},
			SuppressedKeys: []string{"HIDDEN"},
		}},
		{"explicit grants", `{"GRANTED":"secret:REAL_TOKEN","NPM_TOKEN":"secret:NPM_TOKEN"}`, state.AppEnvironmentSecretIntent{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := state.RuntimeAppValuesSnapshot{Secrets: rows, InheritedSecrets: inherited}
			snapshot.AccountID, snapshot.AppID, snapshot.Scope = account, app, scope
			if tc.grants != "" {
				snapshot.SecretGrants.OverrideEnvSecrets = json.RawMessage(tc.grants)
			}
			entries, err := sched.RuntimeMainSecretEntries(snapshot, tc.intent)
			if err != nil {
				t.Fatal(err)
			}
			delivered := map[string]bool{}
			for _, entry := range entries {
				delivered[entry.Key] = true
				got, missing, err := state.ResolveRuntimeSecretSources(snapshot, tc.intent, []string{entry.Key})
				if err != nil || len(missing) != 0 || len(got) != 1 {
					t.Fatalf("%s: resolved %+v missing %v err %v; the wake delivers it", entry.Key, got, missing, err)
				}
				if !bytes.Equal(got[0].Ciphertext, entry.Ciphertext) || got[0].SourceKey != entry.SourceKey {
					t.Fatalf("%s: build gets %q from %q; the wake delivers %q from %q",
						entry.Key, got[0].Ciphertext, got[0].SourceKey, entry.Ciphertext, entry.SourceKey)
				}
			}
			for _, key := range []string{"NPM_TOKEN", "REAL_TOKEN", "OWN_SHARED", "HIDDEN", "ALIAS", "GRANTED", "SHARED", "PROJECT_ONLY", "NOPE"} {
				if delivered[key] {
					continue
				}
				got, missing, err := state.ResolveRuntimeSecretSources(snapshot, tc.intent, []string{key})
				if err != nil || len(got) != 0 || len(missing) != 1 {
					t.Fatalf("%s: the wake does not deliver it, but the build resolves %+v (missing %v, err %v)", key, got, missing, err)
				}
			}
		})
	}
}
