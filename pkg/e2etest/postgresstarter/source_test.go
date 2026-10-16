package postgresstarter

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"math/big"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestSourceKeepsShippedEntrypointsAndManifest(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	files, err := Source(ca)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"handler.js", "app.js", "database.js", "runtime/postgres-pool-runtime.js", "migrate.js", "schema.sql", "package.json", "package-lock.json", "gregale.yaml"} {
		original, err := fs.ReadFile(templates.FS, "rest-api-postgres/"+name)
		if err != nil || files[name] != string(original) {
			t.Fatalf("deployed starter changed %s", name)
		}
	}
	manifest, err := gregalemanifest.ParseBytes([]byte(files["gregale.yaml"]))
	if err != nil || manifest.Validate() != nil || manifest.Release.Command != "node migrate.js" || manifest.Lifecycle.AfterRestore.TimeoutMS != 2000 || manifest.Lifecycle.BeforeCheckpoint == nil || manifest.Lifecycle.BeforeCheckpoint.TimeoutMS != 2000 {
		t.Fatal("deployed source lost its lifecycle contract")
	}
	if files["fixture-ca.pem"] != string(ca) {
		t.Fatal("fixture CA was not included")
	}
	for _, invalid := range [][]byte{nil, []byte("garbage"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private")}), append(ca, []byte("garbage")...), append([]byte("garbage"), ca...)} {
		if _, err := Source(invalid); err == nil {
			t.Fatal("source upload accepted invalid or private trust material")
		}
	}
}
