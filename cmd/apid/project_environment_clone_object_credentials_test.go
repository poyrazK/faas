// adr: 568
package main

import (
	"context"
	"errors"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func capturedCloneObjectWorkerCredentialContract(t *testing.T, srv *server, store *cloneObjectCheckpointFailureStore, account state.Account, project state.Project, app state.App, lease state.ProjectEnvironmentCloneLease, sources []state.ObjectS3Credential, identity *age.X25519Identity) state.ProjectEnvironmentCloneLease {
	t.Helper()
	ctx := context.Background()
	stamp, stamped, err := store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	store.failCredential = true
	lease, ids, count, err := srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease)
	if err == nil || len(ids) != 0 || count != 0 || store.credentialWrites != 1 {
		t.Fatalf("missing credential commit boundary: %v, writes %d", err, store.credentialWrites)
	}
	lease, ids, count, err = srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease)
	if err != nil || len(ids) != 1 || count != 6 || store.credentialWrites != 2 {
		t.Fatalf("credential retry did not reuse committed identity: %v, writes %d", err, store.credentialWrites)
	}
	if after, ok, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || ok != stamped || !after.Equal(stamp) {
		t.Fatalf("private preparation changed production runtime stamp: %v", err)
	}
	for _, source := range sources {
		prepared, err := store.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		credential := prepared.Credential
		if credential.ID == source.ID || credential.AccessKeyID == source.AccessKeyID || credential.BucketID == source.BucketID ||
			credential.Label != source.Label || credential.Permission != source.Permission || credential.KID != identity.Recipient().String() {
			t.Fatal("fresh credential lost identity separation or captured rights")
		}
		namespace, signingKey, err := secretbox.OpenBytes(identity, credential.SecretSealed)
		if err != nil || namespace != s3gateway.CredentialSecretNamespace || len(signingKey) != 40 || string(signingKey) == "production-only-key" {
			t.Fatalf("target signing key did not decrypt with target identity: %v", err)
		}
		if _, _, err := store.ResolveObjectS3Credential(ctx, credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("prepared key usable before publication: %v", err)
		}
		if source.ManagedAppID == "" {
			if len(prepared.Secrets) != 0 || credential.ManagedScope != "" {
				t.Fatal("customer credential created compute envelopes")
			}
			continue
		}
		bucket, err := store.ProjectEnvironmentCloneObjectBucketForLease(ctx, lease, app.ID, source.BucketID, credential.BucketID)
		if err != nil || credential.ManagedScope != lease.Operation.TargetEnvironment || credential.ManagedPrefix != source.ManagedPrefix || ids[0] != credential.ID {
			t.Fatalf("managed binding lost captured target scope: %v", err)
		}
		want := map[string]string{"FILES_ENDPOINT": srv.objectStorage.PublicEndpoint, "FILES_REGION": srv.objectStorage.PublicRegion,
			"FILES_BUCKET": bucket.Name, "FILES_ACCESS_KEY_ID": credential.AccessKeyID, "FILES_SECRET_ACCESS_KEY": string(signingKey), "FILES_ADDRESSING_STYLE": "path"}
		if len(prepared.Secrets) != len(want) {
			t.Fatal("incomplete target runtime envelopes")
		}
		for _, secret := range prepared.Secrets {
			opened, err := secretbox.Open(identity, secret.Ciphertext)
			value, exists := want[secret.Key]
			if err != nil || !exists || len(opened) != 1 || opened[secret.Key] != value || secret.Scope != lease.Operation.TargetEnvironment ||
				secret.ManagedObjectStorageCredentialID != credential.ID || len(secret.ValueHash) != 16 {
				t.Fatalf("target envelope %s differs from independent expected value: %v", secret.Key, err)
			}
			delete(want, secret.Key)
		}
		if len(want) != 0 {
			t.Fatal("missing target envelope key")
		}
	}

	// A completed replay needs neither the current endpoint configuration nor
	// encryption keys. The durable receipts pin both generated values and IDs.
	registry, recipient := srv.objectStorage, setSecretRecipient
	srv.objectStorage = nil
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	replayed, replayIDs, replayCount, replayErr := srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease)
	srv.objectStorage, setSecretRecipient = registry, recipient
	if replayErr != nil || replayed.Operation.Revision != lease.Operation.Revision || len(replayIDs) != 1 || replayIDs[0] != ids[0] || replayCount != count || store.credentialWrites != 2 {
		t.Fatalf("completed replay regenerated prepared values: %v", replayErr)
	}
	if _, _, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID,
		SourceSlug: "production", TargetSlug: lease.Operation.TargetEnvironment, CloneOperationID: lease.Operation.ID, CloneOperationRevision: lease.Operation.Revision,
		ManagedBindingsPrepared: true, PreparedManagedBindingIDs: ids, PreparedManagedSecretCount: count}, api.MustLimitsFor(account.Plan)); err != nil {
		t.Fatalf("materialize worker-prepared bindings: %v", err)
	}
	return lease
}
