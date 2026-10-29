package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// s3Credentials is an in-memory set of active S3 credentials.
type s3Credentials struct {
	rows   map[string]state.ObjectS3Credential
	rotate string
}

func (f *s3Credentials) ListObjectS3CredentialsForRekey(_ context.Context, limit int, afterID string) ([]state.ObjectS3Credential, error) {
	var out []state.ObjectS3Credential
	for _, c := range f.rows {
		if c.ID > afterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *s3Credentials) ResealStorageCredential(_ context.Context, id string, old []byte, kid string, sealed []byte) (bool, error) {
	if id == f.rotate {
		f.rotate = ""
		return false, nil
	}
	c, ok := f.rows[id]
	if !ok || !bytes.Equal(c.SecretSealed, old) {
		return false, nil
	}
	c.SecretSealed, c.KID = sealed, kid
	f.rows[id] = c
	return true, nil
}

func TestMigrateStorage(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	secret := "0123456789abcdef0123456789abcdef01234567"
	seal := func(to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		b, err := secretbox.SealBytesTo(rs, s3gateway.CredentialSecretNamespace, []byte(secret), 64)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	f := &s3Credentials{rows: map[string]state.ObjectS3Credential{
		"c1": {ID: "c1", KID: fleet.Recipient().String(), SecretSealed: seal(fleet)},
		"c2": {ID: "c2", KID: fleet.Recipient().String(), SecretSealed: seal(domain, fleet)},
		"c3": {ID: "c3", KID: domain.Recipient().String(), SecretSealed: seal(domain)},
		"c4": {ID: "c4", KID: "age1other", SecretSealed: seal(stranger)},
		"c5": {ID: "c5", KID: fleet.Recipient().String(), SecretSealed: seal(fleet)},
	}}
	f.rotate = "c5"

	report, err := sealmigrate.MigrateStorage(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Storage, Target: domain.Recipient().String(),
		Scanned: 5, Resealed: 2, Current: 1, Changed: 1, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	for _, id := range []string{"c1", "c2"} {
		c := f.rows[id]
		if c.KID != domain.Recipient().String() {
			t.Errorf("%s key id = %q, want the domain recipient", id, c.KID)
		}
		if _, _, err := secretbox.OpenBytes(fleet, c.SecretSealed); err == nil {
			t.Errorf("%s still opens with the fleet identity", id)
		}
		if ns, plain, err := secretbox.OpenBytes(domain, c.SecretSealed); err != nil || ns != s3gateway.CredentialSecretNamespace || string(plain) != secret {
			t.Errorf("%s domain open = %q %d bytes, %v", id, ns, len(plain), err)
		}
	}
	again, err := sealmigrate.MigrateStorage(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 1 || again.Changed != 0 {
		t.Fatalf("second run = %+v, %v; want the rotated credential re-sealed", again, err)
	}
}
