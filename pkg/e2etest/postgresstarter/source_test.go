package postgresstarter

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
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
	for _, starter := range Starters() {
		t.Run(starter.Template, func(t *testing.T) {
			files, err := Source(starter.Template, ca)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			if err := fs.WalkDir(templates.FS, starter.Template, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				original, err := fs.ReadFile(templates.FS, path)
				if err != nil {
					return err
				}
				if files[strings.TrimPrefix(path, starter.Template+"/")] != string(original) {
					t.Fatalf("deployed starter changed %s", path)
				}
				count++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(files) != count+1 || !strings.Contains(files["Dockerfile"], "COPY runtime ./runtime\n") {
				t.Fatal("fixture source must add only the CA under the shipped runtime COPY")
			}
			manifest, err := gregalemanifest.ParseBytes([]byte(files["gregale.yaml"]))
			if err != nil || manifest.Validate() != nil || manifest.Release.Command != starter.ReleaseCommand || manifest.Lifecycle.AfterRestore.TimeoutMS != 2000 {
				t.Fatal("deployed source lost its lifecycle contract")
			}
			if files[CAPath] != string(ca) || files["fixture-ca.pem"] != "" {
				t.Fatal("fixture CA was not included")
			}
			for _, invalid := range [][]byte{nil, []byte("garbage"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private")}), append(ca, []byte("garbage")...), append([]byte("garbage"), ca...)} {
				if _, err := Source(starter.Template, invalid); err == nil {
					t.Fatal("source upload accepted invalid or private trust material")
				}
			}

		})
	}
	if _, err := Source("../rest-api-postgres", ca); err == nil {
		t.Fatal("unsupported template accepted")
	}
}

func TestStarterMatrixRequiresBothLanguagesAndAllGuestPhases(t *testing.T) {
	starters := Starters()
	if len(starters) != 2 || starters[0].Template != "rest-api-postgres" || starters[1].Template != "rest-api-postgres-python" ||
		starters[0].TrustEnv != "NODE_EXTRA_CA_CERTS" || starters[1].TrustEnv != "SSL_CERT_FILE" {
		t.Fatal("native matrix must cover both shipped starters with verified TLS")
	}
	contract := guestqualification.SuiteContract()
	for _, starter := range starters {
		for _, suffix := range []string{"release_and_initial_restore", "manual_task_without_migration_secret", "pooled_disconnect_recovery", "failed_hook_cold_boot", "running_rotation_and_retirement", "parked_rotation_scheduler_recovery"} {
			found := false
			for _, phase := range contract.Phases {
				found = found || phase == starter.PhasePrefix+"_"+suffix
			}
			if !found {
				t.Fatal("guest contract omits a shipped starter phase")
			}
		}
	}
}
