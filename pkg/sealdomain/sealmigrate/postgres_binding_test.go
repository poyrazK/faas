package sealmigrate_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261023173624 — every document kind resolves to the account vmmd
// checks: the id prefix, the deployment's app, or the promotion.
func TestPostgresBindRuntime(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	domain := mustIdentity(t)
	dk := domain.Recipient().String()
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	seal := func(key string) []byte {
		t.Helper()
		blob, err := secretbox.SealOne(domain.Recipient(), key, "value-of-"+key, 0)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	envelope := func(key string) map[string]any {
		return map[string]any{"key": key, "kid": dk, "value_hash": "hash-of-" + key, "ciphertext": base64.StdEncoding.EncodeToString(seal(key))}
	}

	account, err := store.CreateAccount(ctx, "runtime-binding-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "runtime-binding-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, state.DefaultEnvScope, "OPENAI_API_KEY", dk, "", seal("OPENAI_API_KEY")); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "runtime-binding-" + suffix, ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "staging", Key: "DATABASE_URL",
		Ciphertext: seal("DATABASE_URL"), Kid: dk, ValueHash: "fp-" + suffix,
	}, 50); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:binding", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal([]any{envelope("SESSION_SECRET")})
	if _, err := pool.Exec(ctx, "INSERT INTO deployment_release_secret_snapshots(deployment_id,values_json) VALUES($1,$2)", dep.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	var promotionID string
	if err := pool.QueryRow(ctx, `INSERT INTO project_environment_promotions(account_id,project_id,project_slug,from_environment,to_environment,promotion_hash,idempotency_key)
		VALUES($1,$2,$3,'staging','production',$4,$5) RETURNING id`, account.ID, project.ID, project.Slug, strings.Repeat("a", 64), "binding-"+suffix).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	capture, _ := json.Marshal(map[string]any{"hash": strings.Repeat("b", 64), "secrets": map[string]any{}, "workloads": []any{map[string]any{"live_secrets": []any{envelope("STRIPE_KEY")}}}})
	if _, err := pool.Exec(ctx, "INSERT INTO project_environment_promotion_release_config(promotion_id,payload) VALUES($1,$2)", promotionID, capture); err != nil {
		t.Fatal(err)
	}
	ours := map[string]bool{
		"app|" + account.ID + "/" + app.ID + "/" + state.DefaultEnvScope + "/OPENAI_API_KEY": true,
		"project|" + account.ID + "/" + project.ID + "/staging/DATABASE_URL":                 true,
		"deployment|" + dep.ID:     true,
		"promotion|" + promotionID: true,
	}

	report, err := sealmigrate.BindRuntime(ctx, store, domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < len(ours) || report.Failed != 0 || report.Mismatched != 0 {
		t.Fatalf("report = %+v, want our %d documents bound", report, len(ours))
	}
	bound := map[string]bool{}
	for _, d := range listBindingDocuments(t, store) {
		if !ours[d.Cursor()] {
			continue
		}
		if d.AccountID != account.ID {
			t.Fatalf("%s: owner = %q, want %s", d.Cursor(), d.AccountID, account.ID)
		}
		if _, err := state.TransformSecretRekeyDocument(d.SecretRekeyDocument, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
			env, binding, err := secretbox.OpenMultiBound([]*age.X25519Identity{domain}, e.Ciphertext)
			if err != nil || env[e.Key] != "value-of-"+e.Key || binding.AccountID != account.ID || e.Kid != dk {
				t.Fatalf("%s %s: open = %v, %+v, %v (kid %s); want bound to %s", d.Cursor(), e.Key, env, binding, err, e.Kid, account.ID)
			}
			return e, nil
		}); err != nil {
			t.Fatal(err)
		}
		bound[d.Cursor()] = true
	}
	if len(bound) != len(ours) {
		t.Fatalf("bound documents = %v, want %v", bound, ours)
	}

	// Ciphertext copied into another account's row is reported, not rebound.
	other, err := store.CreateAccount(ctx, "runtime-binding-other-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "runtime-binding-other-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAppSecrets(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, other.ID, otherApp.ID, state.DefaultEnvScope, "OPENAI_API_KEY", dk, "", rows[0].Ciphertext); err != nil {
		t.Fatal(err)
	}
	again, err := sealmigrate.BindRuntime(ctx, store, domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	copied := "app|" + other.ID + "/" + otherApp.ID + "/" + state.DefaultEnvScope + "/OPENAI_API_KEY"
	if again.Mismatched != 1 || len(again.MismatchedDocuments) != 1 || again.MismatchedDocuments[0] != copied {
		t.Fatalf("second run = %+v, want only %s reported as bound to another account", again, copied)
	}
}

func listBindingDocuments(t *testing.T, store *state.PgStore) []state.SecretBindingDocument {
	t.Helper()
	var all []state.SecretBindingDocument
	after := ""
	for {
		page, err := store.ListSecretBindingDocuments(context.Background(), after, 500)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if len(page) < 500 {
			return all
		}
		after = page[len(page)-1].Cursor()
	}
}
