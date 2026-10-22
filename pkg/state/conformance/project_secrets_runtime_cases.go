// adr: 1139
package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type projectSecretStores interface {
	state.ProjectEnvironmentSecretStore
	state.SecretRekeyStore
}

func projectSecretStore(t *testing.T, fx *Fixture) projectSecretStores {
	t.Helper()
	store, ok := fx.Store.(projectSecretStores)
	if !ok {
		t.Fatalf("%T does not keep project environment secrets", fx.Store)
	}
	return store
}

func ageRecipient(t *testing.T) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return identity.Recipient().String()
}

func (p projectConfigFixture) setSecret(t *testing.T, fx *Fixture, environment, key, hash, kid string) {
	t.Helper()
	if _, err := projectSecretStore(t, fx).SetProjectEnvironmentSecret(fx.Ctx, state.ProjectEnvironmentSecret{
		AccountID: fx.Account.ID, ProjectID: p.project.ID, EnvironmentSlug: environment, Key: key,
		Ciphertext: []byte("sealed:" + key + ":" + hash), Kid: kid, ValueHash: hash,
	}, 50); err != nil {
		t.Fatalf("SetProjectEnvironmentSecret(%s/%s): %v", environment, key, err)
	}
}

func inheritedSecretVersions(t *testing.T, fx *Fixture, appID, deploymentID string) map[string]string {
	t.Helper()
	snapshot, err := fx.Store.RuntimeAppValuesForDeployment(fx.Ctx, fx.Account.ID, appID, deploymentID)
	if err != nil {
		t.Fatalf("RuntimeAppValuesForDeployment: %v", err)
	}
	got := map[string]string{}
	for i, row := range snapshot.InheritedSecrets {
		if i > 0 && snapshot.InheritedSecrets[i-1].Key >= row.Key {
			t.Fatalf("inherited secrets are not sorted: %+v", snapshot.InheritedSecrets)
		}
		if row.AccountID != fx.Account.ID || len(row.Ciphertext) == 0 {
			t.Fatalf("inherited secret = %+v", row)
		}
		got[row.Key] = row.ValueHash
	}
	return got
}

// ADR-1139: a deployment inherits its project environment's secrets, the
// default scope reads production, and anything the app sets itself in its
// scope (a variable or a secret) wins. The fence moves with a value change,
// not with a master-key reseal.
func testProjectSecretsReachRuntimeValues(t *testing.T, fx *Fixture) {
	p := newProjectConfigFixture(t, fx)
	kid := ageRecipient(t)
	if err := fx.Store.UpsertAppEnvInScope(fx.Ctx, fx.Account.ID, p.app.ID, "production", "SHARED_VAR", "own"); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpsertAppSecretWithClassInScope(fx.Ctx, fx.Account.ID, p.app.ID, "production", "APP_OWN", kid, "ab", state.SecretClassPersistent, []byte("own-envelope")); err != nil {
		t.Fatal(err)
	}
	defaultDep, productionDep, stagingDep := p.deploy(t, fx, "default"), p.deploy(t, fx, "production"), p.deploy(t, fx, "staging")
	if got := inheritedSecretVersions(t, fx, p.app.ID, productionDep.ID); len(got) != 0 {
		t.Fatalf("inherited before any secret = %v", got)
	}
	for _, key := range []string{"DATABASE_PASSWORD", "SHARED_VAR", "APP_OWN"} {
		p.setSecret(t, fx, "production", key, "p1", kid)
	}
	p.setSecret(t, fx, "staging", "DATABASE_PASSWORD", "s1", kid)

	if got := inheritedSecretVersions(t, fx, p.app.ID, productionDep.ID); !sameStrings(got, map[string]string{"DATABASE_PASSWORD": "p1"}) {
		t.Fatalf("production inherited %v, want only DATABASE_PASSWORD (the app's own variable and secret win)", got)
	}
	if got := inheritedSecretVersions(t, fx, p.app.ID, defaultDep.ID); !sameStrings(got, map[string]string{"DATABASE_PASSWORD": "p1", "SHARED_VAR": "p1", "APP_OWN": "p1"}) {
		t.Fatalf("default scope inherited %v, want production's secrets", got)
	}
	if got := inheritedSecretVersions(t, fx, p.app.ID, stagingDep.ID); !sameStrings(got, map[string]string{"DATABASE_PASSWORD": "s1"}) {
		t.Fatalf("staging inherited %v, want staging's own", got)
	}
	if got := inheritedSecretVersions(t, fx, fx.App.ID, fx.Deployment.ID); len(got) != 0 {
		t.Fatalf("an app outside any project inherited %v", got)
	}

	fence := func() string {
		t.Helper()
		snapshot, err := fx.Store.RuntimeAppValuesForDeployment(fx.Ctx, fx.Account.ID, p.app.ID, stagingDep.ID)
		if err != nil {
			t.Fatal(err)
		}
		value, err := state.NewRuntimeAppConfigFence(snapshot)
		if err != nil {
			t.Fatalf("NewRuntimeAppConfigFence: %v", err)
		}
		return value.Fingerprint
	}
	before := fence()
	rekey := projectSecretStore(t, fx)
	next := ageRecipient(t)
	docs, err := rekey.ListSecretRekeyDocuments(fx.Ctx, next, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	resealed := 0
	for _, doc := range docs {
		if doc.Kind != "project" || !strings.Contains(doc.ID, "/"+p.project.ID+"/") {
			continue
		}
		after, err := state.TransformSecretRekeyDocument(doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
			e.Kid, e.Ciphertext = next, append([]byte("resealed:"), e.Ciphertext...)
			return e, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rekey.CompareAndSwapSecretRekeyDocument(fx.Ctx, doc, after); err != nil {
			t.Fatalf("reseal %s: %v", doc.ID, err)
		}
		resealed++
	}
	if resealed != 4 {
		t.Fatalf("resealed %d project secrets, want 4", resealed)
	}
	if after := fence(); after != before {
		t.Fatal("a master-key reseal moved the configuration fence")
	}
	p.setSecret(t, fx, "staging", "DATABASE_PASSWORD", "s2", next)
	if after := fence(); after == before {
		t.Fatal("a value change kept the configuration fence")
	}
}

// ADR-1139: a project secret change stamps the member apps in the scopes that
// read the environment and marks their older snapshots stale; production also
// covers the default scope, and staging is untouched by it.
func testProjectSecretChangeInvalidatesMemberSnapshots(t *testing.T, fx *Fixture) {
	p := newProjectConfigFixture(t, fx)
	kid := ageRecipient(t)
	type capture struct {
		deployment state.Deployment
		snapshot   state.Snapshot
	}
	capturePublished := func(scope string) capture {
		t.Helper()
		dep := p.deploy(t, fx, scope)
		ins, err := fx.Store.CreateInstance(fx.Ctx, p.app.ID, dep.ID, string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		snap, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, state.Snapshot{
			DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0",
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "initial"),
		}, ins.ID, ins.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		return capture{dep, snap}
	}
	usable := func(c capture) bool {
		t.Helper()
		got, err := fx.Store.LatestSnapshot(fx.Ctx, c.deployment.ID)
		if errors.Is(err, state.ErrNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return got.ID == c.snapshot.ID
	}
	defaultScope, staging := capturePublished("default"), capturePublished("staging")
	time.Sleep(2 * time.Millisecond)

	p.setSecret(t, fx, "production", "DATABASE_PASSWORD", "p1", kid)
	if usable(defaultScope) {
		t.Fatal("a production secret left the default-scope snapshot usable")
	}
	if !usable(staging) {
		t.Fatal("a production secret invalidated the staging snapshot")
	}
	time.Sleep(2 * time.Millisecond)
	p.setSecret(t, fx, "staging", "DATABASE_PASSWORD", "s1", kid)
	if usable(staging) {
		t.Fatal("a staging secret left the staging snapshot usable")
	}
}
