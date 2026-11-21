package state

import (
	"errors"
	"testing"
	"time"
)

// A durable clone writes the shared secrets it captured, sealed and
// unchanged, with the cloned apps' references and suppressions; publication
// proves the stage holds exactly them (ADR-20261104120000 amendment
// 2026-11-21).
func TestMemCloneSharedSecretsMaterializeAndProve(t *testing.T) {
	const app, other = "00000000000000000000000000000002", "00000000000000000000000000000003"
	m := NewMemStore()
	m.apps[app] = App{ID: app, AccountID: "acct", ProjectID: "proj"}
	m.apps[other] = App{ID: other, AccountID: "acct", ProjectID: "proj"}
	m.projectEnvironments["stage-id"] = ProjectEnvironment{ID: "stage-id", AccountID: "acct", ProjectID: "proj", Slug: "stage"}
	captured, err := normalizeCloneSharedSecrets(projectCloneSharedSecrets{
		Secrets:      []projectCloneSharedSecret{{Key: "DATABASE_PASSWORD", Ciphertext: []byte("sealed"), Kid: "kid", ValueHash: "ab", SecretVersion: 3}},
		Refs:         []projectCloneSecretRef{{AppID: app, Key: "DB_PASS", SecretName: "DATABASE_PASSWORD"}, {AppID: other, Key: "DB_PASS", SecretName: "DATABASE_PASSWORD"}},
		Suppressions: []projectCloneSecretSuppression{{AppID: app, Key: "API_TOKEN"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scoped := captured.forApps(map[string]bool{canonicalMemUUID(app): true})
	if len(scoped.Refs) != 1 || len(scoped.Suppressions) != 1 {
		t.Fatalf("scoped to the cloned app = %+v", scoped)
	}
	clone := ProjectEnvironmentClone{AccountID: "acct", ProjectID: "proj", TargetSlug: "stage"}
	secrets, refs := m.materializeCloneSharedSecretsLocked(clone, m.projectEnvironments["stage-id"], scoped, time.Now().UTC())
	if secrets != 1 || refs != 1 {
		t.Fatalf("materialized %d secrets, %d refs", secrets, refs)
	}
	stored := m.projectEnvironmentSecrets[projectEnvironmentSecretKey("proj", "stage", "DATABASE_PASSWORD")]
	if string(stored.Ciphertext) != "sealed" || stored.Kid != "kid" || stored.SecretVersion != 3 {
		t.Fatalf("stored secret = %+v", stored)
	}
	target, err := m.cloneSharedSecretsLocked("acct", "proj", "stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCloneSharedSecretsProof(&scoped, target); err != nil {
		t.Fatalf("proof of the copy: %v", err)
	}
	stored.Ciphertext = []byte("other")
	m.projectEnvironmentSecrets[projectEnvironmentSecretKey("proj", "stage", "DATABASE_PASSWORD")] = stored
	if target, err = m.cloneSharedSecretsLocked("acct", "proj", "stage"); err != nil {
		t.Fatal(err)
	}
	if err := validateCloneSharedSecretsProof(&scoped, target); !errors.Is(err, ErrConflict) {
		t.Fatalf("proof accepted a changed envelope: %v", err)
	}
	if err := validateCloneSharedSecretsProof(nil, target); err != nil {
		t.Fatalf("a capture without shared secrets needs no proof: %v", err)
	}
}

func TestNormalizeCloneSharedSecretsRefusesDuplicatesAndBadRows(t *testing.T) {
	secret := projectCloneSharedSecret{Key: "K", Ciphertext: []byte("c"), Kid: "kid", ValueHash: "h", SecretVersion: 1}
	for name, in := range map[string]projectCloneSharedSecrets{
		"duplicate secret": {Secrets: []projectCloneSharedSecret{secret, secret}},
		"empty envelope":   {Secrets: []projectCloneSharedSecret{{Key: "K", Kid: "kid", ValueHash: "h", SecretVersion: 1}}},
		"bad key":          {Secrets: []projectCloneSharedSecret{{Key: "1K", Ciphertext: []byte("c"), Kid: "kid", ValueHash: "h", SecretVersion: 1}}},
		"duplicate ref":    {Refs: []projectCloneSecretRef{{AppID: "a", Key: "K", SecretName: "S"}, {AppID: "a", Key: "K", SecretName: "T"}}},
	} {
		if _, err := normalizeCloneSharedSecrets(in); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}
