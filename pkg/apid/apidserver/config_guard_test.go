package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/configguard"
	"github.com/onebox-faas/faas/pkg/state"
)

// configGuardTelemetry stands in for Postgres request telemetry.
type configGuardTelemetry struct {
	sample state.ConfigGuardSample
	reads  int
}

func (f *configGuardTelemetry) ConfigGuardTelemetry(context.Context, state.ConfigGuardTelemetryQuery) (state.ConfigGuardSample, error) {
	f.reads++
	return f.sample, nil
}

type configGuardFixture struct {
	t         *testing.T
	e         apidserver.TestEnv
	app       state.App
	telemetry *configGuardTelemetry
}

func newConfigGuardFixture(t *testing.T, slug string, live bool) configGuardFixture {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	t.Cleanup(apidserver.WithTestRecipient(t))
	ctx := context.Background()
	dep := apidserver.MustSeedDeployment(t, e, slug)
	if live {
		if err := (*e.Store()).MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
	}
	app, err := (*e.Store()).AppBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	telemetry := &configGuardTelemetry{}
	(*e.S()).SetConfigGuardTelemetry(telemetry)
	return configGuardFixture{t: t, e: e, app: app, telemetry: telemetry}
}

func (f configGuardFixture) write(method, path string, body any, wantStatus int) *api.ConfigChangeReceipt {
	f.t.Helper()
	rec := f.e.Do(f.t, method, "/v1/apps/"+f.app.Slug+path, body, nil)
	if rec.Code != wantStatus {
		f.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), wantStatus)
	}
	if rec.Code == http.StatusNoContent {
		return nil
	}
	var out struct {
		ConfigChange *api.ConfigChangeReceipt `json:"config_change"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out.ConfigChange
}

func (f configGuardFixture) sweep(now time.Time) {
	f.t.Helper()
	if err := (*f.e.S()).ConfigGuardSweep(context.Background(), now); err != nil {
		f.t.Fatal(err)
	}
}

func (f configGuardFixture) guard() api.ConfigGuard {
	f.t.Helper()
	rec := f.e.Do(f.t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/config/guard", nil, nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("GET config/guard = %d %s", rec.Code, rec.Body.String())
	}
	var out api.ConfigGuard
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f configGuardFixture) envValue(key string) (string, bool) {
	f.t.Helper()
	rows, err := (*f.e.Store()).ListAppEnvInScope(context.Background(), f.app.AccountID, f.app.ID, "default")
	if err != nil {
		f.t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key == key {
			return row.Value, true
		}
	}
	return "", false
}

func (f configGuardFixture) secretExists(key string) bool {
	f.t.Helper()
	_, err := (*f.e.Store()).GetAppSecretInScope(context.Background(), f.app.AccountID, f.app.ID, "default", key)
	return err == nil
}

// A wrong DATABASE_URL made every new instance answer 500: the guard judges
// the first new-config traffic against the old config and restores both the
// variable and the secret the same change added, then records why.
func TestConfigGuardRestoresAFailingChange(t *testing.T) {
	f := newConfigGuardFixture(t, "cfg-restore", true)
	if r := f.write(http.MethodPut, "/env/DATABASE_URL?guard=false", api.PutAppEnvRequest{Value: "postgres://good"}, http.StatusOK); r == nil || r.Guard != nil {
		t.Fatalf("unguarded write receipt = %+v, want a revision and no guard", r)
	}
	bad := f.write(http.MethodPut, "/env/DATABASE_URL", api.PutAppEnvRequest{Value: "postgres://bad"}, http.StatusOK)
	if bad == nil || bad.Guard == nil || bad.Guard.Status != state.ConfigGuardWatching || bad.Guard.WindowMinutes != api.ConfigGuardWindowMinutes {
		t.Fatalf("guarded write receipt = %+v, want a watching guard", bad)
	}
	added := f.write(http.MethodPut, "/secrets/API_TOKEN", api.PutAppSecretRequest{Value: "tok_new"}, http.StatusOK)
	if added == nil || added.Guard == nil || added.Guard.ID != bad.Guard.ID {
		t.Fatalf("second write receipt = %+v, want it to join guard %s", added, bad.Guard.ID)
	}

	now := time.Now().UTC()
	f.sweep(now) // no new-config traffic yet
	if g := f.guard(); g.Status != state.ConfigGuardWatching || g.WindowEndsAt != "" {
		t.Fatalf("guard before traffic = %+v, want watching with no window", g)
	}
	f.telemetry.sample = state.ConfigGuardSample{NewRequests: 8, NewServerErrors: 8, OldRequests: 120, OldServerErrors: 1}
	f.sweep(now.Add(time.Minute))

	if v, _ := f.envValue("DATABASE_URL"); v != "postgres://good" {
		t.Fatalf("DATABASE_URL after restore = %q, want the previous value", v)
	}
	if f.secretExists("API_TOKEN") {
		t.Fatal("the secret the failed change added was not removed")
	}
	g := f.guard()
	if g.Status != state.ConfigGuardRestored || g.Decision == nil || g.Decision.Rule != configguard.RuleFirstWake5xx ||
		g.Decision.NewServerErrors != 8 || g.Decision.RestoredRevision <= g.HeadRevision {
		t.Fatalf("guard after failing traffic = %+v decision %+v, want restored on the first-wake rule", g, g.Decision)
	}
	rec := f.e.Do(t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/config/revisions", nil, nil)
	var history api.ListConfigRevisionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET config/revisions = %d %s", rec.Code, rec.Body.String())
	}
	newest := history.Revisions[0]
	if newest.Source != state.ConfigSourceGuardRestore || newest.RestoredRevision != g.BaseRevision || newest.Actor != "gregale:config-guard" {
		t.Fatalf("newest revision = %+v, want the guard's restore of base %d", newest, g.BaseRevision)
	}
	if history.Revisions[1].GuardStatus != state.ConfigGuardRestored || history.Guard == nil || history.Guard.ID != g.ID {
		t.Fatalf("history = %+v, want guarded revisions marked restored", history)
	}
	reads := f.telemetry.reads
	f.sweep(now.Add(2 * time.Minute))
	if f.telemetry.reads != reads {
		t.Fatal("a closed guard was evaluated again")
	}
}

// A change that serves cleanly through the window closes clean, and a
// change meant to fix a broken app is never reverted to the broken config.
func TestConfigGuardKeepsHealthyChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample state.ConfigGuardSample
	}{
		{"healthy new config", state.ConfigGuardSample{NewRequests: 60, NewServerErrors: 0, OldRequests: 300, OldServerErrors: 1}},
		{"fix for a broken app", state.ConfigGuardSample{NewRequests: 10, NewServerErrors: 6, OldRequests: 80, OldServerErrors: 80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConfigGuardFixture(t, "cfg-healthy", true)
			f.write(http.MethodPut, "/env/MODE", api.PutAppEnvRequest{Value: "fast"}, http.StatusOK)
			f.telemetry.sample = tc.sample
			now := time.Now().UTC()
			f.sweep(now)
			if g := f.guard(); g.Status != state.ConfigGuardWatching || g.WindowEndsAt == "" {
				t.Fatalf("guard after first traffic = %+v, want watching with an open window", g)
			}
			f.sweep(now.Add(time.Duration(api.ConfigGuardWindowMinutes)*time.Minute + api.RollbackOn5xxTelemetryGrace + time.Minute))
			if g := f.guard(); g.Status != state.ConfigGuardClean || g.Decision == nil || g.Decision.Rule != configguard.RuleClean {
				t.Fatalf("guard after the window = %+v, want clean", g)
			}
			if v, _ := f.envValue("MODE"); v != "fast" {
				t.Fatalf("MODE = %q, want the change kept", v)
			}
		})
	}
}

// Deleting a secret is revocation: when the app then fails, the guard records
// the regression and restores nothing.
func TestConfigGuardNeverRestoresADeletedSecret(t *testing.T) {
	f := newConfigGuardFixture(t, "cfg-revoke", true)
	f.write(http.MethodPut, "/secrets/LEAKED?guard=false", api.PutAppSecretRequest{Value: "tok_leaked"}, http.StatusOK)
	f.write(http.MethodPut, "/env/MODE", api.PutAppEnvRequest{Value: "strict"}, http.StatusOK)
	f.write(http.MethodDelete, "/secrets/LEAKED", nil, http.StatusNoContent)
	f.telemetry.sample = state.ConfigGuardSample{NewRequests: 5, NewServerErrors: 5}
	f.sweep(time.Now().UTC())
	g := f.guard()
	if g.Status != state.ConfigGuardRegressed || g.Restorable || g.Decision == nil {
		t.Fatalf("guard = %+v, want regressed and not restorable", g)
	}
	if f.secretExists("LEAKED") {
		t.Fatal("a deleted secret was restored")
	}
	if v, _ := f.envValue("MODE"); v != "strict" {
		t.Fatalf("MODE = %q; a regressed guard must not restore part of its change", v)
	}
}

// Rollback sets and deletes variables back to a revision as one guarded
// change, and reports the secrets it cannot restore.
func TestConfigRollbackRestoresVariablesAndReportsSecrets(t *testing.T) {
	f := newConfigGuardFixture(t, "cfg-rollback", true)
	f.write(http.MethodPut, "/env/A?guard=false", api.PutAppEnvRequest{Value: "1"}, http.StatusOK)
	target := f.write(http.MethodPut, "/env/B?guard=false", api.PutAppEnvRequest{Value: "2"}, http.StatusOK)
	f.write(http.MethodPut, "/env/A?guard=false", api.PutAppEnvRequest{Value: "9"}, http.StatusOK)
	f.write(http.MethodDelete, "/env/B?guard=false", nil, http.StatusNoContent)
	f.write(http.MethodPut, "/env/C?guard=false", api.PutAppEnvRequest{Value: "3"}, http.StatusOK)
	f.write(http.MethodPut, "/secrets/TOKEN?guard=false", api.PutAppSecretRequest{Value: "tok"}, http.StatusOK)

	rec := f.e.Do(t, http.MethodPost, "/v1/apps/"+f.app.Slug+"/config/rollback", api.ConfigRollbackRequest{Revision: target.Revision}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback = %d %s", rec.Code, rec.Body.String())
	}
	var out api.ConfigRollbackResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Set) != 2 || out.Set[0] != "A" || out.Set[1] != "B" || len(out.Deleted) != 1 || out.Deleted[0] != "C" {
		t.Fatalf("rollback wrote set=%v deleted=%v, want set [A B] deleted [C]", out.Set, out.Deleted)
	}
	if len(out.SecretsNotRestored) != 1 || out.SecretsNotRestored[0].Key != "TOKEN" || out.SecretsNotRestored[0].TargetVersion != 0 {
		t.Fatalf("secrets not restored = %+v, want TOKEN (absent at the target)", out.SecretsNotRestored)
	}
	if out.Change == nil || out.Change.Guard == nil {
		t.Fatalf("rollback change = %+v, want a guarded revision", out.Change)
	}
	for key, want := range map[string]string{"A": "1", "B": "2"} {
		if v, _ := f.envValue(key); v != want {
			t.Fatalf("%s = %q after rollback, want %q", key, v, want)
		}
	}
	if _, ok := f.envValue("C"); ok {
		t.Fatal("C survived the rollback")
	}
	missing := f.e.Do(t, http.MethodPost, "/v1/apps/"+f.app.Slug+"/config/rollback", api.ConfigRollbackRequest{Revision: 999}, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("rollback to a missing revision = %d, want 404", missing.Code)
	}
}

// Without a live deployment there is no traffic to judge a change by, so no
// guard opens; the revision is still recorded.
func TestConfigChangeOpensNoGuardWithoutALiveDeployment(t *testing.T) {
	f := newConfigGuardFixture(t, "cfg-new", false)
	r := f.write(http.MethodPut, "/env/MODE", api.PutAppEnvRequest{Value: "x"}, http.StatusOK)
	if r == nil || r.Guard != nil || r.Revision < 1 {
		t.Fatalf("receipt = %+v, want a revision without a guard", r)
	}
	if rec := f.e.Do(t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/config/guard", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET config/guard = %d, want 404", rec.Code)
	}
}
