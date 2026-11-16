package internalsvc_test

// adr: 20261110210000

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func clusterRow(t *testing.T, recipient *age.X25519Recipient) (sealed []byte, pubPEM, kid string, priv ed25519.PrivateKey) {
	t.Helper()
	priv, pub, err := internalsvc.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err = secretbox.SealBytes(recipient, "cluster_svc", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 4096)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sealed, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})), internalsvc.KidFromPub(pub), priv
}

func TestOpenClusterKey(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, pubPEM, kid, priv := clusterRow(t, id.Recipient())
	got, gotKid, err := internalsvc.OpenClusterKey(sealed, pubPEM, kid, []*age.X25519Identity{id})
	if err != nil || gotKid != kid || !got.Equal(priv) {
		t.Fatalf("open = %v %q, %v", got != nil, gotKid, err)
	}
	if _, _, err := internalsvc.OpenClusterKey(sealed, pubPEM, kid, []*age.X25519Identity{other}); !errors.Is(err, internalsvc.ErrClusterKeySealed) {
		t.Fatalf("wrong identity = %v, want ErrClusterKeySealed", err)
	}
	if _, _, err := internalsvc.OpenClusterKey(sealed, pubPEM, "other-kid", []*age.X25519Identity{id}); err == nil {
		t.Fatal("a row whose key_id disagrees was opened")
	}
	_, otherPub, _, _ := clusterRow(t, id.Recipient())
	if _, _, err := internalsvc.OpenClusterKey(sealed, otherPub, kid, []*age.X25519Identity{id}); err == nil {
		t.Fatal("a row whose public_key_pem disagrees was opened")
	}
}
