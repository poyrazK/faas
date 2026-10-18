package productemail_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func TestProductEmailLocalCaptureAndSealedGrant(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail")
	sink, err := productemail.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	msg := mail.Message{To: []string{"fixture@example.test"}, Subject: "Fixture", TextBody: "Fixture"}
	for range 2 {
		status, e := sink.Send(t.Context(), id, msg)
		if e != nil || status != "captured" {
			t.Fatal(status, e)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	st, _ := os.Stat(filepath.Join(dir, files[0].Name()))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	msg.TextBody = "changed"
	if _, err = sink.Send(t.Context(), id, msg); err == nil {
		t.Fatal("changed idempotent capture")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("A", 43)
	sealed, err := productemail.Seal(identity, id, token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), token) {
		t.Fatal("plaintext grant stored")
	}
	if value, e := productemail.Open([]*age.X25519Identity{identity}, id, sealed); e != nil || value != token {
		t.Fatal(e)
	}
	if _, err = productemail.Open([]*age.X25519Identity{identity}, uuid.NewString(), sealed); err == nil {
		t.Fatal("cross-subscription grant")
	}
	data, _ := json.Marshal(struct{ Grant []byte }{sealed})
	if strings.Contains(string(data), token) {
		t.Fatal("plaintext token escaped")
	}
}
