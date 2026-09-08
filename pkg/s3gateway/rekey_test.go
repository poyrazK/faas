package s3gateway

import (
	"context"
	"sort"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type rekeyTestStore struct {
	rows     map[string]state.ObjectS3Credential
	resealed int
}

func (s *rekeyTestStore) ListObjectS3CredentialsForRekey(_ context.Context, limit int, afterID string) ([]state.ObjectS3Credential, error) {
	rows := make([]state.ObjectS3Credential, 0, len(s.rows))
	for _, row := range s.rows {
		if row.Status == state.ObjectS3CredentialStatusActive && row.ID > afterID {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *rekeyTestStore) ResealObjectS3Credential(_ context.Context, id, previousKID, currentKID string, sealed []byte) error {
	row, ok := s.rows[id]
	if !ok || row.Status != state.ObjectS3CredentialStatusActive || row.KID != previousKID {
		return state.ErrNotFound
	}
	row.KID, row.SecretSealed = currentKID, append([]byte(nil), sealed...)
	s.rows[id] = row
	s.resealed++
	return nil
}

func TestRekeyCredentialsMovesPreviousIdentityRows(t *testing.T) {
	previous, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(identity *age.X25519Identity, secret string) []byte {
		t.Helper()
		sealed, err := secretbox.SealBytes(identity.Recipient(), CredentialSecretNamespace, []byte(secret), 64)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	store := &rekeyTestStore{rows: map[string]state.ObjectS3Credential{
		"00000000-0000-0000-0000-000000000001": {
			ID: "00000000-0000-0000-0000-000000000001", KID: previous.Recipient().String(), SecretSealed: seal(previous, testSecret), Status: state.ObjectS3CredentialStatusActive,
		},
		"00000000-0000-0000-0000-000000000002": {
			ID: "00000000-0000-0000-0000-000000000002", KID: current.Recipient().String(), SecretSealed: seal(current, testSecret), Status: state.ObjectS3CredentialStatusActive,
		},
	}}

	rekeyed, err := RekeyCredentials(context.Background(), store, []*age.X25519Identity{current, previous})
	if err != nil || rekeyed != 1 || store.resealed != 1 {
		t.Fatalf("RekeyCredentials = %d, resealed=%d, err=%v", rekeyed, store.resealed, err)
	}
	row := store.rows["00000000-0000-0000-0000-000000000001"]
	if row.KID != current.Recipient().String() {
		t.Fatalf("kid = %q, want current", row.KID)
	}
	namespace, plaintext, err := secretbox.OpenBytesMulti([]*age.X25519Identity{current}, row.SecretSealed)
	if err != nil || namespace != CredentialSecretNamespace || string(plaintext) != testSecret {
		t.Fatalf("current-only open = namespace %q plaintext length %d err=%v", namespace, len(plaintext), err)
	}
	if _, _, err := secretbox.OpenBytesMulti([]*age.X25519Identity{previous}, row.SecretSealed); err == nil {
		t.Fatal("resealed credential still opens with only the previous identity")
	}
}

func TestRekeyCredentialsFailsClosedOnUnreadableActiveRow(t *testing.T) {
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := &rekeyTestStore{rows: map[string]state.ObjectS3Credential{
		"00000000-0000-0000-0000-000000000001": {
			ID: "00000000-0000-0000-0000-000000000001", KID: "age1previous", SecretSealed: []byte("not-an-envelope"), Status: state.ObjectS3CredentialStatusActive,
		},
	}}
	if _, err := RekeyCredentials(context.Background(), store, []*age.X25519Identity{current}); err == nil {
		t.Fatalf("RekeyCredentials error = %v, want failure", err)
	}
}
