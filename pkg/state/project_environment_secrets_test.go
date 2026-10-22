// adr: 1139
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectSecretTestStore interface {
	state.ProjectEnvironmentSecretStore
	state.SecretRekeyStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateProject(context.Context, state.Project) (state.Project, error)
	CreateProjectEnvironment(context.Context, state.ProjectEnvironment) (state.ProjectEnvironment, error)
	CreateApp(context.Context, state.App) (state.App, error)
	DeleteProjectEnvironment(context.Context, string, string, string) error
	AppRuntimeConfigChangedAtInScope(context.Context, string, string) (time.Time, bool, error)
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
}

func TestProjectEnvironmentSecretStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var s projectSecretTestStore = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := s.CreateAccount(ctx, "project-secrets-"+backend+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shared-secrets"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shared-secrets-api",
				Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			oldKey, newKey := newAgeRecipient(t), newAgeRecipient(t)
			secret := func(key, hash string) state.ProjectEnvironmentSecret {
				return state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
					Key: key, Ciphertext: []byte("sealed-" + hash), Kid: oldKey, ValueHash: hash}
			}

			before := time.Now().Add(-time.Second)
			first, err := s.SetProjectEnvironmentSecret(ctx, secret("DATABASE_PASSWORD", "aa01"), 2)
			if err != nil {
				t.Fatal(err)
			}
			if first.SecretVersion != 1 || len(first.Ciphertext) != 0 || first.Kid != oldKey {
				t.Fatalf("first write = %+v, want version 1 and no ciphertext", first)
			}
			if at, ok, err := s.AppRuntimeConfigChangedAtInScope(ctx, app.ID, "staging"); err != nil || !ok || at.Before(before) {
				t.Fatalf("staging boundary = %v %v %v, want stamped by the write", at, ok, err)
			}
			second, err := s.SetProjectEnvironmentSecret(ctx, secret("DATABASE_PASSWORD", "aa02"), 2)
			if err != nil || second.SecretVersion != 2 || !second.CreatedAt.Equal(first.CreatedAt) {
				t.Fatalf("rewrite = %+v, %v; want version 2 and the original created_at", second, err)
			}
			if _, err := s.SetProjectEnvironmentSecret(ctx, secret("PAYMENTS_KEY", "bb01"), 2); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetProjectEnvironmentSecret(ctx, secret("SIGNING_KEY", "cc01"), 2); !errors.Is(err, state.ErrProjectEnvironmentSecretLimit) {
				t.Fatalf("third key at a limit of 2 = %v, want the limit", err)
			}
			if _, err := s.SetProjectEnvironmentSecret(ctx, secret("PAYMENTS_KEY", "bb02"), 2); err != nil {
				t.Fatalf("an existing key at the limit = %v, want a replace", err)
			}
			missing := secret("OTHER", "dd01")
			missing.EnvironmentSlug = "preview"
			if _, err := s.SetProjectEnvironmentSecret(ctx, missing, 2); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unknown environment = %v, want not found", err)
			}
			foreign := secret("OTHER", "dd01")
			foreign.AccountID = app.ID
			if _, err := s.SetProjectEnvironmentSecret(ctx, foreign, 2); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign account = %v, want not found", err)
			}

			listed, err := s.ListProjectEnvironmentSecrets(ctx, account.ID, project.ID, "staging")
			if err != nil || len(listed) != 2 || listed[0].Key != "DATABASE_PASSWORD" || listed[1].Key != "PAYMENTS_KEY" {
				t.Fatalf("list = %+v, %v", listed, err)
			}
			for _, row := range listed {
				if len(row.Ciphertext) != 0 {
					t.Fatalf("a listing carried ciphertext: %+v", row)
				}
			}

			// A master-key reseal swaps kid and ciphertext without a new version.
			docs, err := s.ListSecretRekeyDocuments(ctx, newKey, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			var doc *state.SecretRekeyDocument
			for i := range docs {
				if docs[i].Kind == "project" && docs[i].ID == account.ID+"/"+project.ID+"/staging/DATABASE_PASSWORD" {
					doc = &docs[i]
				}
			}
			if doc == nil {
				t.Fatalf("rekey documents %+v lack the project secret", docs)
			}
			if usage, err := s.SecretEncryptionKeyUsage(ctx, oldKey); err != nil || usage.LiveSecrets != 2 {
				t.Fatalf("usage of the old key = %+v, %v; want both project secrets", usage, err)
			}
			if err := s.RetireSecretEncryptionKey(ctx, oldKey); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("retiring a key project secrets use = %v, want a conflict", err)
			}
			resealed, err := state.TransformSecretRekeyDocument(*doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
				e.Kid, e.Ciphertext = newKey, []byte("resealed")
				return e, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CompareAndSwapSecretRekeyDocument(ctx, *doc, resealed); err != nil {
				t.Fatalf("reseal = %v", err)
			}
			if err := s.CompareAndSwapSecretRekeyDocument(ctx, *doc, resealed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("a stale reseal = %v, want a conflict", err)
			}
			listed, _ = s.ListProjectEnvironmentSecrets(ctx, account.ID, project.ID, "staging")
			if listed[0].Kid != newKey || listed[0].SecretVersion != 2 || listed[0].ValueHash != "aa02" {
				t.Fatalf("resealed row = %+v, want the new kid and the same version and value", listed[0])
			}
			var payload []map[string]json.RawMessage
			if json.Unmarshal(resealed, &payload) != nil || len(payload) != 1 {
				t.Fatalf("resealed document = %s", resealed)
			}

			if err := s.DeleteProjectEnvironmentSecret(ctx, account.ID, project.ID, "staging", "PAYMENTS_KEY"); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteProjectEnvironmentSecret(ctx, account.ID, project.ID, "staging", "PAYMENTS_KEY"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second delete = %v, want not found", err)
			}

			// A clone copies the shared secrets as they are: same value, kid
			// and version, under the new environment.
			if _, result, err := s.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
				AccountID: account.ID, ProjectID: project.ID, SourceSlug: "staging", TargetSlug: "qa",
			}, api.MustLimitsFor(api.PlanPro)); err != nil || result.ProjectSecretsCopied != 1 {
				t.Fatalf("clone = %+v, %v; want one project secret copied", result, err)
			}
			copied, err := s.ListProjectEnvironmentSecrets(ctx, account.ID, project.ID, "qa")
			if err != nil || len(copied) != 1 || copied[0].Key != "DATABASE_PASSWORD" || copied[0].ValueHash != "aa02" ||
				copied[0].SecretVersion != 2 || copied[0].Kid != newKey {
				t.Fatalf("cloned secrets = %+v, %v", copied, err)
			}

			// The environment's secrets leave with it: a new environment of the
			// same slug starts empty.
			if err := s.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			if listed, err := s.ListProjectEnvironmentSecrets(ctx, account.ID, project.ID, "staging"); err != nil || len(listed) != 0 {
				t.Fatalf("recreated environment = %+v, %v; want no inherited secrets", listed, err)
			}
		})
	}
}

func newAgeRecipient(t *testing.T) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return identity.Recipient().String()
}
