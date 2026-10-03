package api

import "testing"

func TestProjectEnvironmentSecretRevisionHashIsStableAndRevisionBound(t *testing.T) {
	first, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{
		{Key: "Z_TOKEN", Version: 2},
		{Key: "A_TOKEN", Version: 7, ManagedBy: "managed_postgres", BindingID: "binding-a", CredentialGeneration: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{
		{Key: "A_TOKEN", Version: 7, ManagedBy: "managed_postgres", BindingID: "binding-a", CredentialGeneration: 3},
		{Key: "Z_TOKEN", Version: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != reordered || !ValidProjectEnvironmentConfigHash(first) {
		t.Fatalf("unordered secret revisions have unstable fingerprint: %q != %q", first, reordered)
	}
	baseline, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{{Key: "Z_TOKEN", Version: 2}})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{{Key: "Z_TOKEN", Version: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if baseline == rotated {
		t.Fatal("secret revision change did not change fingerprint")
	}
	managedBefore, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{{
		Key: "DATABASE_URL", ManagedBy: "managed_postgres", BindingID: "binding-a", CredentialGeneration: 4,
	}})
	if err != nil {
		t.Fatal(err)
	}
	managedAfter, err := ProjectEnvironmentSecretRevisionHash([]ProjectEnvironmentSecretRevision{{
		Key: "DATABASE_URL", ManagedBy: "managed_postgres", BindingID: "binding-a", CredentialGeneration: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if managedBefore == managedAfter {
		t.Fatal("managed credential generation change did not change fingerprint")
	}
}

func TestProjectEnvironmentSecretRevisionHashRejectsInvalidMetadata(t *testing.T) {
	for _, revisions := range [][]ProjectEnvironmentSecretRevision{
		{{Version: 1}},
		{{Key: "TOKEN", Version: -1}},
		{{Key: "TOKEN", ManagedBy: "unknown"}},
		{{Key: "TOKEN", Version: 1}, {Key: "TOKEN", Version: 2}},
	} {
		if _, err := ProjectEnvironmentSecretRevisionHash(revisions); err == nil {
			t.Errorf("invalid revisions accepted: %+v", revisions)
		}
	}
}
