package apidserver_test

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Accept-time source policy for GA (ADR-20261026152731).

func writeSourceArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	headers := make([]tar.Header, 0, len(files))
	bodies := make(map[string][]byte, len(files))
	for name, body := range files {
		headers = append(headers, tar.Header{Name: name})
		bodies[name] = []byte(body)
	}
	p := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(p, apidserver.BuildTestTarGz(t, headers, bodies), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Migrations, schemas and seed data under data/ or db/ are stateless source.
func TestStatefulShapeAcceptsStateDirSource(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{
		"myproject/index.js":                        "exports.handler = () => 1;\n",
		"myproject/db/migrations/20260101_init.sql": "create table t (id int);\n",
		"myproject/db/schema.ts":                    "export const t = 1;\n",
		"myproject/data/seed.json":                  "{}\n",
	})
	if prob := apidserver.ScanForStatefulShapeAtRoot(path, false, ""); prob != nil {
		t.Fatalf("stateless source rejected: %+v", prob)
	}
}

// A SQLite database is refused by its signature even without an extension.
func TestStatefulShapeRefusesSQLiteSignature(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{"myproject/data/store": "SQLite format 3\x00\x10\x00"})
	prob := apidserver.ScanForStatefulShapeAtRoot(path, false, "")
	if prob == nil || prob.Code != api.CodeStatelessOnlyViolation || !strings.Contains(prob.Detail, "data/store") {
		t.Fatalf("problem = %+v, want stateless violation naming data/store", prob)
	}
}

// Only credential formats refuse source; an SRI attribute and a heuristic
// high-entropy value do not.
func TestSourceSecretScanBlocksCredentialsOnly(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	limits := api.MustLimitsFor(api.PlanPro)
	clean := writeSourceArchive(t, map[string]string{
		"app/templates/base.html": `<script src="https://cdn.example.com/lib.js" integrity="sha384-oqVuAfXRKap7fdgcCY5uykM6+R9GqQ8K/uxy9rx7HNQlGYl1kPzQho1wx4JwY8wC"></script>` + "\n",
		"app/test/auth.test.js":   `const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U";` + "\n",
	})
	if prob := apidserver.ScanSourceTarballSecrets(clean, limits); prob != nil {
		t.Fatalf("advisory findings refused the source: %+v", prob)
	}
	leaked := writeSourceArchive(t, map[string]string{"app/config.js": `const AWS_KEY = "AKIA` + "IOSFODNN7EXAMPLQ" + `";` + "\n"})
	if prob := apidserver.ScanSourceTarballSecrets(leaked, limits); prob == nil || prob.Code != api.CodeSecretScanStrict {
		t.Fatalf("credential accepted: %+v", prob)
	}
}

func parseManifest(t *testing.T, yaml string) *gregalemanifest.Manifest {
	t.Helper()
	m, err := gregalemanifest.ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func TestResolveSourceDockerfileBuild(t *testing.T) {
	app := state.App{ID: "app-1", Type: state.AppTypeApp}
	manifest := parseManifest(t, "build:\n  dockerfile: docker/Dockerfile.prod\n  target: runtime\n  args: {NODE_ENV: production}\n  secrets:\n    - {id: npmrc, secret: NPM_TOKEN}\n")
	path := writeSourceArchive(t, map[string]string{
		"src/docker/Dockerfile.prod": "FROM node:24 AS runtime\nCMD [\"node\", \"index.js\"]\n",
		"src/index.js":               "x",
	})
	dockerfile, opts, ok, prob := apidserver.ResolveSourceDockerfileBuild(path, "", app, manifest)
	if prob != nil || !ok || dockerfile != "docker/Dockerfile.prod" {
		t.Fatalf("resolve = %q %v %+v", dockerfile, ok, prob)
	}
	if opts.Target != "runtime" || opts.Args["NODE_ENV"] != "production" || len(opts.Secrets) != 1 || opts.Secrets[0].Secret != "NPM_TOKEN" {
		t.Fatalf("options = %+v", opts)
	}

	missing := writeSourceArchive(t, map[string]string{"src/index.js": "x"})
	if _, _, _, prob := apidserver.ResolveSourceDockerfileBuild(missing, "", app, manifest); prob == nil || !strings.Contains(prob.Detail, "Dockerfile") {
		t.Fatalf("missing Dockerfile accepted: %+v", prob)
	}
	volume := writeSourceArchive(t, map[string]string{"src/docker/Dockerfile.prod": "FROM node:24\nVOLUME /data\n"})
	if _, _, _, prob := apidserver.ResolveSourceDockerfileBuild(volume, "", app, manifest); prob == nil || prob.Code != api.CodeStatelessOnlyViolation {
		t.Fatalf("VOLUME accepted: %+v", prob)
	}
	function := state.App{ID: "fn-1", Type: state.AppTypeFunction}
	if _, _, _, prob := apidserver.ResolveSourceDockerfileBuild(path, "", function, manifest); prob == nil {
		t.Fatal("function deploy accepted a build: section")
	}
	if _, _, ok, prob := apidserver.ResolveSourceDockerfileBuild(path, "", app, parseManifest(t, "hosting:\n  port: 8080\n")); ok || prob != nil {
		t.Fatalf("manifest without build: = %v %+v", ok, prob)
	}
}
