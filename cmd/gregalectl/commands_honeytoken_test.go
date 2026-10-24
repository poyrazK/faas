package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHoneytokenPlantRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--app-id", "x"},   // no label
		{"--label", "ht-a"}, // neither destination
		{"--label", "ht-a", "--print", "--app-id", "x"}, // both
	} {
		if err := runHoneytokenPlant(args); err == nil {
			t.Errorf("plant %v succeeded", args)
		}
	}
	if err := runHoneytokenRetire(nil); err == nil {
		t.Error("retire without --id succeeded")
	}
}

// TestHoneytokenPostgres plants a decoy in a never-deployed app and prints
// another, then checks that each is found by its keyed digest and that a
// deployed app is refused (ADR 20261024014500).
func TestHoneytokenPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	if sp := pool.Config().ConnConfig.RuntimeParams["search_path"]; sp != "" {
		q := u.Query()
		q.Set("search_path", sp)
		u.RawQuery = q.Encode()
	}
	dsn := u.String()
	store := state.NewPgStore(pool)
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })

	dir := t.TempDir()
	hmacKey := make([]byte, 32)
	if _, err := rand.Read(hmacKey); err != nil {
		t.Fatal(err)
	}
	hmacPath := filepath.Join(dir, "host.hmac.key")
	if err := os.WriteFile(hmacPath, hmacKey, 0o400); err != nil {
		t.Fatal(err)
	}
	reader, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipientPath := filepath.Join(dir, "fleet.age.pub")
	if err := secretbox.WriteRecipientFile(recipientPath, reader); err != nil {
		t.Fatal(err)
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<40))
	suffix := n.Text(36)
	acct, err := store.CreateAccount(ctx, "honeytoken-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	decoyApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "decoy-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	plant := func(args ...string) map[string]any {
		t.Helper()
		out.Reset()
		base := []string{"--hmac-key", hmacPath, "--recipient-file", recipientPath, "--pg-dsn", dsn, "--json"}
		if err := runHoneytokenPlant(append(base, args...)); err != nil {
			t.Fatalf("plant %v: %v", args, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		return got
	}
	match := func(token string) state.Honeytoken {
		t.Helper()
		digest, err := state.HoneytokenDigest(hmacKey, token)
		if err != nil {
			t.Fatal(err)
		}
		h, err := store.MatchHoneytoken(ctx, digest)
		if err != nil {
			t.Fatalf("the planted token is not in the registry: %v", err)
		}
		return h
	}

	planted := plant("--label", "ht-"+suffix+"-a", "--app-id", decoyApp.ID)
	if _, leaked := planted["token"]; leaked {
		t.Fatal("plant into an app printed the token")
	}
	secrets, err := store.ListAppSecrets(ctx, acct.ID, decoyApp.ID)
	if err != nil || len(secrets) != 1 || secrets[0].Key != defaultHoneytokenVar || secrets[0].Kid != reader.Recipient().String() {
		t.Fatalf("decoy app secrets = %+v, %v", secrets, err)
	}
	env, err := secretbox.Open(reader, secrets[0].Ciphertext)
	if err != nil || !strings.HasPrefix(env[defaultHoneytokenVar], api.APIKeyPrefix) {
		t.Fatalf("planted secret = %v, %v; want a real-format API key", env, err)
	}
	if want, _ := secretbox.ValueFingerprint([]byte(env[defaultHoneytokenVar]), hmacKey); secrets[0].ValueHash != want {
		t.Fatalf("value_hash = %q, want the host fingerprint %q like any other secret", secrets[0].ValueHash, want)
	}
	if h := match(env[defaultHoneytokenVar]); h.ID != planted["id"] {
		t.Fatalf("matched %s, want %v", h.ID, planted["id"])
	}

	printed := plant("--label", "ht-"+suffix+"-b", "--print")
	token, _ := printed["token"].(string)
	if h := match(token); h.ID != printed["id"] {
		t.Fatalf("matched %s, want %v", h.ID, printed["id"])
	}

	deployedApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "live-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: deployedApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:honeytoken", Status: state.DeployPending}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runHoneytokenPlant([]string{"--label", "ht-" + suffix + "-c", "--app-id", deployedApp.ID, "--hmac-key", hmacPath, "--recipient-file", recipientPath, "--pg-dsn", dsn}); err == nil {
		t.Fatal("planted into an app with deployments")
	}

	id, _ := printed["id"].(string)
	if err := runHoneytokenRetire([]string{"--id", id, "--pg-dsn", dsn}); err != nil {
		t.Fatal(err)
	}
	if h := match(token); h.RetiredAt == nil {
		t.Fatal("a retired honeytoken lost its retired_at")
	}
}
