package templates

import (
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestPostgresStarterRestoreManifest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("rest-api-postgres", dest); err != nil {
		t.Fatal(err)
	}
	manifest, present, err := gregalemanifest.Load(dest)
	if err != nil || !present || manifest == nil {
		t.Fatalf("load generated manifest: %v, present=%v", err, present)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.Hosting == nil || manifest.Hosting.Health != "/healthz" || manifest.Release == nil || manifest.Release.Command != "node migrate.js" || manifest.Lifecycle == nil {
		t.Fatalf("starter must declare readiness, release, and lifecycle: %+v", manifest)
	}
	hook := manifest.Lifecycle.ToAPI().AfterRestore
	if hook == nil || hook.Path != "/internal/after-restore" || hook.TimeoutMS != 2000 {
		t.Fatalf("starter hook = %#v", hook)
	}
	// The drain keeps SQL sessions out of every capture (ADR-351).
	drain := manifest.Lifecycle.ToAPI().BeforeCheckpoint
	if drain == nil || drain.Path != "/internal/before-checkpoint" || drain.TimeoutMS != 2000 {
		t.Fatalf("starter checkpoint hook = %#v", drain)
	}
	dockerfile, err := os.ReadFile(filepath.Join(dest, "Dockerfile"))
	if err != nil || !strings.Contains(string(dockerfile), `com.gregale.secret-reload-signal="SIGHUP"`) ||
		!strings.Contains(string(dockerfile), `com.gregale.secret-reload-readiness="required"`) ||
		!strings.Contains(string(dockerfile), `COPY runtime ./runtime`) {
		t.Fatal("PostgreSQL image must include its runtime and opt into startup-safe reload", err)
	}
}

// secret-reload-node shares the REST starter's database adapter, so it must
// call the same drain and restore hooks; without them a wake reused the pool
// captured in its snapshot.
func TestSecretReloadNodeStarterLifecycleHooks(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("secret-reload-node", dest); err != nil {
		t.Fatal(err)
	}
	manifest, present, err := gregalemanifest.Load(dest)
	if err != nil || !present || manifest == nil || manifest.Lifecycle == nil {
		t.Fatalf("load generated manifest: %v, present=%v", err, present)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hooks := manifest.Lifecycle.ToAPI()
	if hooks.BeforeCheckpoint == nil || hooks.BeforeCheckpoint.Path != "/internal/before-checkpoint" || hooks.BeforeCheckpoint.TimeoutMS != 2000 ||
		hooks.AfterRestore == nil || hooks.AfterRestore.Path != "/internal/after-restore" || hooks.AfterRestore.TimeoutMS != 2000 {
		t.Fatalf("starter hooks = before %#v, after %#v", hooks.BeforeCheckpoint, hooks.AfterRestore)
	}
	handler, err := os.ReadFile(filepath.Join(dest, "handler.js"))
	if err != nil || !strings.Contains(string(handler), `"/internal/before-checkpoint"`) || !strings.Contains(string(handler), `"/internal/after-restore"`) {
		t.Fatal("handler must serve both lifecycle hooks", err)
	}
}

// npm installation and mutating SQL require explicit opt-in. CI invokes this
// separately against its disposable service, with native TLS enabled and the
// fixture CA in NODE_EXTRA_CA_CERTS. Ordinary starter tests require no npm.
func TestPostgresStarterSQLRecovery(t *testing.T) {
	if os.Getenv("GREGALE_POSTGRES_STARTER_TEST") != "1" {
		t.Skip("GREGALE_POSTGRES_STARTER_TEST=1 required")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" || os.Getenv("NODE_EXTRA_CA_CERTS") == "" {
		t.Fatal("explicit SQL recovery requires DATABASE_URL, trusted TLS, and enabled PostgreSQL tests")
	}
	pool := pgtest.OpenDatabase(t)
	cfg := pool.Config().ConnConfig
	uri := url.URL{Scheme: "postgres", User: url.UserPassword(cfg.User, cfg.Password), Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database}
	uri.RawQuery = "sslmode=require"
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("rest-api-postgres", dest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	install := exec.CommandContext(ctx, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	install.Dir = dest
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install locked starter dependencies: %v\n%s", err, output)
	}
	cmd := exec.CommandContext(ctx, "npm", "run", "test:postgres")
	cmd.Dir = dest
	cmd.Env = append(os.Environ(), "GREGALE_POSTGRES_STARTER_TEST_URL="+uri.String(), "GREGALE_POSTGRES_STARTER_TEST_DISPOSABLE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("starter SQL recovery: %v\n%s", err, output)
	}
	t.Log(string(output))
}
