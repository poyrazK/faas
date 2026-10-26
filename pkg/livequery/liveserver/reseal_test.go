package liveserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// resealRows is an in-memory ResealStore: one table of keys, one of logins.
type resealRows struct {
	keys, logins map[string]livekeys.Sealed
	// rotate names a key replaced between the re-seal's read and its write.
	rotate string
}

func page(rows map[string]livekeys.Sealed, after string, limit int) []livekeys.Sealed {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []livekeys.Sealed{}
	for _, id := range ids[:min(limit, len(ids))] {
		out = append(out, rows[id])
	}
	return out
}

func swap(rows map[string]livekeys.Sealed, row livekeys.Sealed, blob []byte) bool {
	current, ok := rows[row.ID]
	if !ok || !bytes.Equal(current.Blob, row.Blob) {
		return false
	}
	current.Blob = blob
	rows[row.ID] = current
	return true
}

func (s *resealRows) SealedKeysAfter(_ context.Context, after string, limit int) ([]livekeys.Sealed, error) {
	return page(s.keys, after, limit), nil
}

func (s *resealRows) ResealKey(_ context.Context, key livekeys.Sealed, blob []byte) (bool, error) {
	if key.ID == s.rotate {
		row := s.keys[key.ID]
		row.Blob = []byte("rotated meanwhile")
		s.keys[key.ID] = row
	}
	return swap(s.keys, key, blob), nil
}

func (s *resealRows) SealedLoginsAfter(_ context.Context, after string, limit int) ([]livekeys.Sealed, error) {
	return page(s.logins, after, limit), nil
}

func (s *resealRows) ResealLogin(_ context.Context, login livekeys.Sealed, blob []byte) (bool, error) {
	return swap(s.logins, login, blob), nil
}

func TestResealBindsLegacyRowsToTheirDatabase(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	const db, other = "3f1c0b6e-9a2b-44d1-8c7e-0123456789ab", "0a1b2c3d-0000-4000-8000-000000000001"
	sealTo := func(to *age.X25519Identity, namespace, plain string) []byte {
		t.Helper()
		blob, err := secretbox.SealBytes(to.Recipient(), namespace, []byte(plain), livekeys.LoginMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	secrets := map[string]string{}
	newKey := func(rows map[string]livekeys.Sealed, to *age.X25519Identity, namespace string) string {
		t.Helper()
		text, err := seal.GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		secret, err := seal.ParseSecret(text)
		if err != nil {
			t.Fatal(err)
		}
		secrets[secret.KID] = text
		rows[secret.KID] = livekeys.Sealed{ID: secret.KID, DatabaseID: db, Blob: sealTo(to, namespace, text), Recipient: to.Recipient().String()}
		return secret.KID
	}
	store := &resealRows{keys: map[string]livekeys.Sealed{}, logins: map[string]livekeys.Sealed{}}
	legacy := newKey(store.keys, identity, livekeys.SealNamespace)
	bound := newKey(store.keys, identity, livekeys.KeySealNamespaceFor(db))
	elsewhere := newKey(store.keys, identity, livekeys.KeySealNamespaceFor(other))
	unopened := newKey(store.keys, stranger, livekeys.SealNamespace)
	rotated := newKey(store.keys, identity, livekeys.SealNamespace)
	store.rotate = rotated
	// Another key's secret copied onto this row: re-sealing it would launder
	// the copy into this database's namespace.
	copied := newKey(store.keys, identity, livekeys.SealNamespace)
	row := store.keys[copied]
	row.Blob = sealTo(identity, livekeys.SealNamespace, secrets[legacy])
	store.keys[copied] = row
	// More logins than a page, so the re-seal pages.
	dsns := map[string]string{}
	for i := range resealPage + 1 {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		dsns[id] = fmt.Sprintf("postgresql://gregale_live_%d:PASSWORD%d@db.internal/app", i, i)
		store.logins[id] = livekeys.Sealed{ID: id, DatabaseID: id, Blob: sealTo(identity, livekeys.LoginSealNamespace, dsns[id]), Recipient: identity.Recipient().String()}
	}
	before := map[string][]byte{}
	for id, k := range store.keys {
		before[id] = k.Blob
	}

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	report, err := Reseal(t.Context(), store, []*age.X25519Identity{identity}, log)
	if err != nil {
		t.Fatal(err)
	}
	want := ResealReport{Keys: ResealCounts{Resealed: 1, Bound: 1, Changed: 1, Refused: 3}, Logins: ResealCounts{Resealed: resealPage + 1}}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	opens := func(blob []byte, namespace, plain string) bool {
		got, text, err := secretbox.OpenBytes(identity, blob)
		return err == nil && got == namespace && string(text) == plain
	}
	if !opens(store.keys[legacy].Blob, livekeys.KeySealNamespaceFor(db), secrets[legacy]) {
		t.Error("the legacy key was not re-sealed to its database with the same secret")
	}
	for _, id := range []string{bound, elsewhere, unopened, copied} {
		if !bytes.Equal(store.keys[id].Blob, before[id]) {
			t.Errorf("key %s was changed; it must be left as it is", id)
		}
	}
	for id, dsn := range dsns {
		if !opens(store.logins[id].Blob, livekeys.LoginSealNamespaceFor(id), dsn) {
			t.Fatalf("login %s was not re-sealed to its database", id)
		}
	}
	for _, plain := range append([]string{secrets[legacy], secrets[copied]}, dsns["00000000-0000-4000-8000-000000000000"]) {
		if strings.Contains(logs.String(), plain) {
			t.Fatal("the re-seal logged a plaintext")
		}
	}
	if strings.Contains(logs.String(), "PASSWORD") {
		t.Fatal("the re-seal logged a login's password")
	}

	again, err := Reseal(t.Context(), store, []*age.X25519Identity{identity}, log)
	if err != nil {
		t.Fatal(err)
	}
	if again.Keys.Resealed != 0 || again.Logins.Resealed != 0 || again.Keys.Bound != 2 || again.Logins.Bound != resealPage+1 {
		t.Fatalf("a second re-seal = %+v; it must find every row it re-sealed already bound", again)
	}
}
