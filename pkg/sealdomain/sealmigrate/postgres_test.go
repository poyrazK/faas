package sealmigrate_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func openStore(t *testing.T) *state.PgStore {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool)
}

func TestPostgresSealDomainProbe(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	domain := mustIdentity(t)
	if _, err := store.LoadSealDomainProbe(ctx, "outbound"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing probe error = %v, want ErrNotFound", err)
	}
	blob, err := sealdomain.SealProbe(sealdomain.Outbound, domain.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, "outbound", domain.Recipient().String(), blob); err != nil {
		t.Fatal(err)
	}
	probe, err := store.LoadSealDomainProbe(ctx, "outbound")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Recipient != domain.Recipient().String() {
		t.Fatalf("probe recipient = %s", probe.Recipient)
	}
	if err := sealdomain.OpenProbe(sealdomain.Outbound, []*age.X25519Identity{domain}, probe.SealedBlob); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, "Outbound!", domain.Recipient().String(), blob); err == nil {
		t.Fatal("the domain CHECK accepted an invalid name")
	}
}

func TestPostgresGithubWebhookSecretSealing(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet := mustIdentity(t)
	recipients := []*age.X25519Recipient{fleet.Recipient()}
	base := rand.Int64N(1<<40) + 1<<41
	legacyID, sealedID := base, base+1

	// A row written by the previous release: plaintext only.
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, legacyID, state.GithubWebhookSecret{Value: []byte("legacy-secret-0123456789")}, "test"); err != nil {
		t.Fatal(err)
	}
	// A row written by this release: both columns.
	secret := []byte("current-secret-0123456789")
	sealed, err := sealdomain.SealGithubWebhookSecret(recipients, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, sealedID, state.GithubWebhookSecret{Value: secret, Sealed: sealed}, "test"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGithubWebhookSecret(ctx, sealedID)
	if err != nil || !bytes.Equal(got.Sealed, sealed) || !bytes.Equal(got.Value, secret) {
		t.Fatalf("GetGithubWebhookSecret = %+v, %v", got, err)
	}

	unsealed, err := store.ListUnsealedGithubWebhookSecrets(ctx, base-1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInstallation(unsealed, legacyID) || containsInstallation(unsealed, sealedID) {
		t.Fatalf("unsealed rows = %+v; want the legacy row and not the sealed one", unsealed)
	}
	if updated, err := store.SealGithubWebhookSecret(ctx, legacyID, []byte("rotated-meanwhile-012345"), sealed); err != nil || updated {
		t.Fatalf("sealing against a changed plaintext = %v, %v; want no write", updated, err)
	}

	report, err := sealmigrate.SealGithubWebhookSecrets(ctx, store, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 1 {
		t.Fatalf("backfill report = %+v", report)
	}
	legacy, err := store.GetGithubWebhookSecret(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, legacy.Sealed)
	if err != nil || string(opened) != "legacy-secret-0123456789" {
		t.Fatalf("backfilled copy opens to %q, %v", opened, err)
	}
}

// TestPostgresMigrateRuntime re-seals an app secret and a project environment
// secret to the runtime domain through the rekey documents' compare-and-swap
// writes, and leaves each row's version and fingerprint as they were.
func TestPostgresMigrateRuntime(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "runtime-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "runtime-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealOne(fleet.Recipient(), "OPENAI_API_KEY", "sk-live", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, state.DefaultEnvScope, "OPENAI_API_KEY", fleet.Recipient().String(), "", sealed); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "runtime-domain-" + suffix, ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	projectSealed, err := secretbox.SealOne(fleet.Recipient(), "DATABASE_URL", "postgres://staging", 0)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "staging", Key: "DATABASE_URL",
		Ciphertext: projectSealed, Kid: fleet.Recipient().String(), ValueHash: "fp-" + suffix,
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	report, err := sealmigrate.MigrateRuntime(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed != 2 || report.Failed != 0 || report.Changed != 0 {
		t.Fatalf("report = %+v, want the app and the project secret re-sealed", report)
	}
	again, err := sealmigrate.MigrateRuntime(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Scanned != 0 {
		t.Fatalf("second run = %+v, %v; want nothing left to re-seal", again, err)
	}
	projectRows, err := store.ListProjectEnvironmentSecrets(ctx, account.ID, project.ID, "staging")
	if err != nil || len(projectRows) != 1 {
		t.Fatalf("project rows = %d, %v", len(projectRows), err)
	}
	if got := projectRows[0]; got.Kid != domain.Recipient().String() || got.SecretVersion != before.SecretVersion || got.ValueHash != before.ValueHash {
		t.Fatalf("project secret = kid %s version %d hash %s, want the domain kid and version %d hash %s unchanged", got.Kid, got.SecretVersion, got.ValueHash, before.SecretVersion, before.ValueHash)
	}
	// Listings carry no ciphertext; read the stored envelope through the rekey
	// documents, which every row with a kid other than the fleet's appears in.
	projectCiphertext := storedProjectCiphertext(t, store, fleet.Recipient().String(), account.ID+"/"+project.ID+"/staging/DATABASE_URL")
	if env, err := secretbox.Open(domain, projectCiphertext); err != nil || env["DATABASE_URL"] != "postgres://staging" {
		t.Fatalf("project secret domain open = %v, %v", env, err)
	}
	if _, err := secretbox.Open(fleet, projectCiphertext); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed project secret")
	}
	rows, err := store.ListAppSecrets(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	if rows[0].Kid != domain.Recipient().String() {
		t.Fatalf("kid = %s, want the runtime domain", rows[0].Kid)
	}
	if env, err := secretbox.Open(domain, rows[0].Ciphertext); err != nil || env["OPENAI_API_KEY"] != "sk-live" {
		t.Fatalf("domain open = %v, %v", env, err)
	}
	if _, err := secretbox.Open(fleet, rows[0].Ciphertext); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed runtime secret")
	}
}

// storedProjectCiphertext returns the stored ciphertext of the project
// environment secret document id, listed as needing a key other than kid.
func storedProjectCiphertext(t *testing.T, store *state.PgStore, kid, id string) []byte {
	t.Helper()
	docs, err := store.ListSecretRekeyDocuments(context.Background(), kid, "project|", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.Kind != "project" || d.ID != id {
			continue
		}
		var ciphertext []byte
		if _, err := state.TransformSecretRekeyDocument(d, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
			ciphertext = e.Ciphertext
			return e, nil
		}); err != nil {
			t.Fatal(err)
		}
		return ciphertext
	}
	t.Fatalf("project secret document %s not listed", id)
	return nil
}

func containsInstallation(rows []state.GithubWebhookSecretRow, id int64) bool {
	for _, r := range rows {
		if r.InstallationID == id {
			return true
		}
	}
	return false
}

// TestPostgresMigrateSource moves webhook secret copies to the source domain
// through the compare-and-swap writes: a fleet-sealed copy is re-sealed and a
// row without a copy gets a domain-sealed one.
func TestPostgresMigrateSource(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	base := rand.Int64N(1<<40) + 1<<42
	sealedID, plainID := base, base+1
	sealedSecret, plainSecret := []byte("fleet-sealed-secret-0123"), []byte("plaintext-only-secret-01")
	fleetCopy, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{fleet.Recipient()}, sealedSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, sealedID, state.GithubWebhookSecret{Value: sealedSecret, Sealed: fleetCopy}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, plainID, state.GithubWebhookSecret{Value: plainSecret}, "test"); err != nil {
		t.Fatal(err)
	}
	if updated, err := store.ResealGithubWebhookSecret(ctx, sealedID, []byte("not-the-stored-copy"), fleetCopy); err != nil || updated {
		t.Fatalf("re-sealing against a changed copy = %v, %v; want no write", updated, err)
	}
	report, err := sealmigrate.MigrateSource(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 2 {
		t.Fatalf("report = %+v, want both rows sealed to the domain", report)
	}
	for id, want := range map[int64][]byte{sealedID: sealedSecret, plainID: plainSecret} {
		got, err := store.GetGithubWebhookSecret(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{domain}, got.Sealed); err != nil || !bytes.Equal(opened, want) {
			t.Fatalf("installation %d: domain open = %q, %v", id, opened, err)
		}
		if _, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, got.Sealed); err == nil {
			t.Fatalf("installation %d: the fleet identity still opens the copy", id)
		}
	}
	again, err := sealmigrate.MigrateSource(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 || again.Current < 2 {
		t.Fatalf("second run = %+v, %v; want both rows current", again, err)
	}
}

// TestPostgresMigrateBuild re-seals an app's private-registry password to the
// build domain through the compare-and-swap write.
func TestPostgresMigrateBuild(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "build-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "build-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(fleet.Recipient(), "registry_creds", []byte("ghp_registry"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppRegistryCredential(ctx, account.ID, app.ID, "ghcr.io", "octocat", sealed); err != nil {
		t.Fatal(err)
	}
	report, err := sealmigrate.MigrateBuild(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 1 {
		t.Fatalf("report = %+v, want the registry password re-sealed", report)
	}
	got, err := store.GetAppRegistryCredential(ctx, account.ID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatal(err)
	}
	if ns, plain, err := secretbox.OpenBytes(domain, got.PasswordEncrypted); err != nil || ns != "registry_creds" || string(plain) != "ghp_registry" {
		t.Fatalf("domain open = %q %q, %v", ns, plain, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, got.PasswordEncrypted); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed registry password")
	}
	again, err := sealmigrate.MigrateBuild(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed", again, err)
	}
}

// TestPostgresGithubWebhookSecretWithoutPlaintext pins the step that retires
// the plaintext column (ADR 20261022111812): a secret stored as its sealed
// copy alone round-trips and migrates, and a row with neither copy is refused.
func TestPostgresGithubWebhookSecretWithoutPlaintext(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	fleet, domain := mustIdentity(t), mustIdentity(t)
	id := rand.Int64N(1<<40) + 1<<43
	secret := []byte("sealed-only-secret-0123456")
	sealed, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{fleet.Recipient()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, id, state.GithubWebhookSecret{Sealed: sealed}, "test"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGithubWebhookSecret(ctx, id)
	if err != nil || got.Value != nil || !bytes.Equal(got.Sealed, sealed) {
		t.Fatalf("GetGithubWebhookSecret = %+v, %v; want the sealed copy and no plaintext", got, err)
	}
	if _, err := sealmigrate.MigrateSource(ctx, store, domain, []*age.X25519Identity{fleet}); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetGithubWebhookSecret(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{domain}, got.Sealed); err != nil || !bytes.Equal(opened, secret) {
		t.Fatalf("migrated copy opens to %q, %v", opened, err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, id+1, state.GithubWebhookSecret{}, "test"); err == nil {
		t.Fatal("a secret with neither copy was stored")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_webhook_secrets (installation_id, upgraded_by) VALUES ($1, 'test')`, id+2); err == nil {
		t.Fatal("the database accepted a row with neither copy of the secret")
	}
}

// TestPostgresMigrateEdge runs the edge re-seal against PostgreSQL (ADR
// 20261022111812): an app's basic-auth credential and a customer log drain's
// header move to the domain alone, through the apps and app_log_drains
// guards, and a second run finds them current.
func TestPostgresMigrateEdge(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "edge-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "edge-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	basic, err := secretbox.SealBytes(fleet.Recipient(), api.AppPublicAuthBasicSealNamespace, []byte("editor\npass"), api.AppPublicAuthBasicMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{SetPublicAuth: true, PublicAuth: &state.AppPublicAuthUpdate{Mode: api.AppPublicAuthModeBasic, Sealed: basic}}); err != nil {
		t.Fatal(err)
	}
	header, err := secretbox.SealBytes(fleet.Recipient(), "APP_LOG_DRAIN_AUTH_HEADER", []byte("Authorization: Bearer d"), api.AppLogDrainAuthHeaderMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	drain, err := store.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKind("http_json"), TargetURL: "https://logs.example.com/" + suffix, AuthHeaderSealed: header, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	report, err := sealmigrate.MigrateEdge(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned < 2 || report.Resealed < 2 {
		t.Fatalf("report = %+v, want the basic-auth credential and the drain header re-sealed", report)
	}
	gotApp, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ns, plain, err := secretbox.OpenBytes(domain, gotApp.PublicAuthBasicSealed); err != nil || ns != api.AppPublicAuthBasicSealNamespace || string(plain) != "editor\npass" {
		t.Fatalf("basic auth domain open = %q %q, %v", ns, plain, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, gotApp.PublicAuthBasicSealed); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed basic-auth credential")
	}
	drains, err := store.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var gotHeader []byte
	for _, d := range drains {
		if d.ID == drain.ID {
			gotHeader = d.AuthHeaderSealed
		}
	}
	if ns, plain, err := secretbox.OpenBytes(domain, gotHeader); err != nil || ns != "APP_LOG_DRAIN_AUTH_HEADER" || string(plain) != "Authorization: Bearer d" {
		t.Fatalf("drain header domain open = %q %q, %v", ns, plain, err)
	}
	again, err := sealmigrate.MigrateEdge(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed", again, err)
	}
}

// TestPostgresMigrateAlerting runs the alerting re-seal against PostgreSQL
// (ADR 20261022111812): an alert rule's webhook secret and a Slack channel's
// destination move to the domain alone, keeping their namespaces, and a
// second run finds them current.
func TestPostgresMigrateAlerting(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "alerting-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "alerting-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := secretbox.SealBytes(fleet.Recipient(), "alert_rule_secret", []byte("whsec-alerting"), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{AccountID: account.ID, AppID: app.ID, Name: "alerting-" + suffix, Enabled: true,
		Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow5m,
		WebhookURL: "https://example.com/hook", WebhookSecretSealed: secret, CooldownMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	target, err := secretbox.SealBytes(fleet.Recipient(), "alert_channel_target", []byte("https://hooks.slack.com/services/T/B/x"), 512)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: account.ID, Name: "ops-" + suffix, Kind: "slack", TargetSealed: target, TargetHint: "hooks.slack.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}

	report, err := sealmigrate.MigrateAlerting(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned < 2 || report.Resealed < 2 {
		t.Fatalf("report = %+v, want the webhook secret and the channel destination re-sealed", report)
	}
	gotRule, err := store.AlertRuleByID(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ns, plain, err := secretbox.OpenBytes(domain, gotRule.WebhookSecretSealed); err != nil || ns != "alert_rule_secret" || string(plain) != "whsec-alerting" {
		t.Fatalf("rule secret domain open = %q %q, %v", ns, plain, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, gotRule.WebhookSecretSealed); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed webhook secret")
	}
	gotChannel, err := store.GetNotificationChannel(ctx, account.ID, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ns, _, err := secretbox.OpenBytes(domain, gotChannel.TargetSealed); err != nil || ns != "alert_channel_target" {
		t.Fatalf("channel destination domain open = %q, %v", ns, err)
	}
	again, err := sealmigrate.MigrateAlerting(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed", again, err)
	}
}
