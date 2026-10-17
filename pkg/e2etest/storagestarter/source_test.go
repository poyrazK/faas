package storagestarter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestSourceDeploysEveryShippedFileUnchanged(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for _, starter := range SuiteContract().Starters {
		t.Run(starter.Template, func(t *testing.T) {
			source, err := Source(starter.Template, ca)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			if err := fs.WalkDir(templates.FS, starter.Template, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				content, err := fs.ReadFile(templates.FS, path)
				if err != nil {
					return err
				}
				if source[path[len(starter.Template)+1:]] != string(content) {
					t.Fatalf("native acceptance changed shipped source %s", path)
				}
				count++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(source) != count+1 || source[CAPath] != string(ca) {
				t.Fatal("native source contains unexpected fixture edits")
			}
			from := map[string]string{"node22": "FROM node:22-", "python312": "FROM python:3.12-"}[starter.Runtime]
			if from == "" || !strings.HasPrefix(source["Dockerfile"], from) {
				t.Fatal("runtime label differs from the shipped Dockerfile's language family")
			}
			manifest, err := gregalemanifest.ParseBytes([]byte(source["gregale.yaml"]))
			if err != nil || manifest.Validate() != nil || manifest.Hosting.Health != "/readyz" ||
				manifest.Lifecycle.AfterRestore.Path != "/internal/after-restore" || manifest.Lifecycle.AfterRestore.TimeoutMS != 2000 {
				t.Fatal("shipped storage readiness and lifecycle contract missing")
			}
			for _, invalid := range [][]byte{nil, []byte("garbage"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private")}),
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}), append(ca, []byte("garbage")...), append([]byte("garbage"), ca...)} {
				if _, err := Source(starter.Template, invalid); err == nil {
					t.Fatal("native source accepted invalid or private trust material")
				}
			}
		})
	}
	if _, err := Source("../object-storage-node", ca); err == nil {
		t.Fatal("unsupported template accepted")
	}
}

func TestRuntimeManifestRejectsPartialForeignOrMalformedEvidence(t *testing.T) {
	contract := SuiteContract()
	for _, starter := range contract.Starters {
		t.Run(starter.Template, func(t *testing.T) {
			manifest := RuntimeManifest{Version: contract.Version, SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Template: starter.Template, Runtime: starter.Runtime, Artifacts: map[string]string{}}
			for _, artifact := range append(contract.Artifacts, starter.AppArtifact, starter.SourceArtifact) {
				manifest.Artifacts[artifact] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			}
			if !manifest.Valid() {
				t.Fatal("complete storage runtime manifest rejected")
			}
			delete(manifest.Artifacts, starter.SourceArtifact)
			if manifest.Valid() {
				t.Fatal("missing exact source archive accepted")
			}
			manifest.Artifacts[starter.SourceArtifact] = strings.Repeat("b", 64)
			delete(manifest.Artifacts, starter.AppArtifact)
			if manifest.Valid() {
				t.Fatal("unqualified application layer accepted")
			}
			manifest.Artifacts[starter.AppArtifact] = "not-a-digest"
			if manifest.Valid() {
				t.Fatal("malformed application digest accepted")
			}
			manifest.Artifacts[starter.AppArtifact] = strings.Repeat("b", 64)
			for _, other := range contract.Starters {
				if other.Template == starter.Template {
					continue
				}
				manifest.Runtime = other.Runtime
				if manifest.Valid() {
					t.Fatal("different starter's runtime accepted")
				}
				manifest.Runtime = starter.Runtime
				delete(manifest.Artifacts, starter.AppArtifact)
				manifest.Artifacts[other.AppArtifact] = strings.Repeat("b", 64)
				if manifest.Valid() {
					t.Fatal("different starter's application layer accepted")
				}
			}
		})
	}
}

func TestContractCoversBothShippedStorageRuntimes(t *testing.T) {
	want := []Starter{
		{Template: "object-storage-node", Runtime: "node22", AppArtifact: "node22_app_layer", SourceArtifact: "node22_source_archive", TrustEnv: "NODE_EXTRA_CA_CERTS"},
		{Template: "object-storage-python", Runtime: "python312", AppArtifact: "python312_app_layer", SourceArtifact: "python312_source_archive", TrustEnv: "AWS_CA_BUNDLE"},
	}
	if !reflect.DeepEqual(SuiteContract().Starters, want) {
		t.Fatal("native acceptance must cover both shipped storage starters with strict TLS trust")
	}
}
