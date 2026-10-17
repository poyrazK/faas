package postgresstarter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the exact native Python source and pinned driver: libpq's explicit
// system trust honors SSL_CERT_FILE without modifying managed connection URIs.
func TestPythonStarterSystemTrust(t *testing.T) {
	if os.Getenv("GREGALE_PYTHON_POSTGRES_TEST") != "1" {
		t.Skip("requires the disposable TLS/SCRAM starter service")
	}
	python := os.Getenv("GREGALE_PYTHON")
	if python == "" {
		t.Fatal("GREGALE_PYTHON must select the pinned starter environment")
	}
	uri, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil || uri.Hostname() == "" {
		t.Fatal("missing disposable SQL URI")
	}
	query := uri.Query()
	ca, err := os.ReadFile(query.Get("sslrootcert"))
	if err != nil {
		t.Fatal("explicit disposable SQL CA required")
	}
	source, err := Source("rest-api-postgres-python", ca)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, content := range source {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	query.Del("sslrootcert")
	query.Set("sslmode", "require") // The unchanged app must upgrade managed require.
	uri.RawQuery = query.Encode()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "unrelated-ca.pem")
	if err := os.WriteFile(foreign, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "invalid-ca.pem")
	if err := os.WriteFile(invalid, []byte("invalid public trust"), 0600); err != nil {
		t.Fatal(err)
	}
	const code = `import os,sys
sys.path.insert(0,sys.argv[1])
import psycopg
from database import connection_kwargs
try:
    uri=os.environ["GREGALE_OWNED_SQL_URI"]
    kwargs=connection_kwargs(uri)
    if kwargs["sslmode"] != "verify-full" or kwargs["sslrootcert"] != "system":
        raise RuntimeError("verification policy changed")
    with psycopg.connect(uri,**kwargs) as connection:
        assert connection.execute("SELECT 1 AS ok").fetchone()["ok"] == 1
except Exception:
    raise SystemExit(2) from None
`
	for _, test := range []struct {
		name, path string
		success    bool
	}{
		{"packaged CA", filepath.Join(dir, CAPath), true}, {"unrelated CA", foreign, false}, {"malformed CA", invalid, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, python, "-I", "-B", "-c", code, dir)
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "SSL_CERT_FILE=") && !strings.HasPrefix(value, "GREGALE_OWNED_SQL_URI=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, "SSL_CERT_FILE="+test.path, "GREGALE_OWNED_SQL_URI="+uri.String())
			err := command.Run()
			var failure *exec.ExitError
			if test.success {
				if err != nil {
					t.Fatal("materialized Python driver did not trust the packaged CA")
				}
			} else if !errors.As(err, &failure) || failure.ExitCode() != 2 {
				t.Fatal("materialized Python driver did not reject the selected CA")
			}
		})
	}
}
