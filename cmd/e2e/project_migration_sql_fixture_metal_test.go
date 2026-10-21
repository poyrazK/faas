//go:build metal

package e2e_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/migrationprobe"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type projectContractSQLSetup struct {
	Environment  []string
	BeforeDeploy func(*projectWorkerNativeFixture)
}
type projectContractSQL struct {
	fixture    *postgresfixture.Fixture
	config     migrationprobe.Config
	seen       map[string]bool
	lastWrites int64
	before     int64
}

func newProjectContractSQLSetup(t *testing.T) projectContractSQLSetup {
	t.Helper()
	if os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP") == "" {
		t.Fatal("managed contract lane requires its isolated SQL proxy address")
	}
	identity, err := age.GenerateX25519Identity()
	mustProjectWorkerNative(t, err)
	keys := t.TempDir()
	private := filepath.Join(keys, "host.age")
	public := filepath.Join(keys, "host.age.pub")
	mustProjectWorkerNative(t, secretbox.WriteHostKeyAtPath(private, identity))
	mustProjectWorkerNative(t, secretbox.WriteRecipientFile(public, identity))
	setup := projectContractSQLSetup{Environment: []string{"FAAS_RELEASE_PHASE_ENABLED=1", "FAAS_APP_TASK_DISPATCH=1", "FAAS_HOST_KEY_PATH=" + private, "FAAS_HOST_AGE_IDENTITY_PATH=" + private, "FAAS_HOST_AGE_RECIPIENT_PATH=" + public, "FAAS_FLEET_AGE_RECIPIENT_PATH=" + public}}
	setup.BeforeDeploy = func(f *projectWorkerNativeFixture) {
		proxy := postgresfixture.OpenProxyWithDeadline(t, f.h.Pool, os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP"), 5432, 5*time.Minute)
		hmac, err := os.ReadFile(f.h.HostHMACKeyPath)
		if err != nil {
			t.Fatal("read isolated signing key")
		}
		fixture := postgresfixture.New(t, f.h.Pool, identity, hmac, proxy.Host, proxy.Port)
		if fixture == nil {
			t.Fatal("managed SQL fixture unavailable")
		}
		app := f.apps["frontend"]
		ports := []int{5432}
		destinations := []string{netip.PrefixFrom(netip.MustParseAddr(proxy.Host), 32).String()}
		_, err = f.owner.UpdateApp(t.Context(), app.Slug, api.UpdateAppRequest{EgressPorts: &ports, EgressAllowlist: &destinations})
		mustProjectWorkerNative(t, err)
		_, bindings := fixture.CreateInScope(t, f.accountID, app.ID, "production")
		config := migrationprobe.Config{RunID: uuid.NewString(), Major: fixture.Major}
		for access, binding := range bindings {
			uri, err := fixture.Observer.URI(t.Context(), binding)
			if err != nil {
				t.Fatal("observe scoped credential delivery")
			}
			switch access {
			case mp.CredentialReadWrite:
				config.RuntimeURI = uri
			case mp.CredentialReadOnly:
				config.ReaderURI = uri
			case mp.CredentialMigration:
				config.MigrationURI = uri
			}
		}
		mustProjectWorkerNative(t, config.Initialize(t.Context()))
		for name, value := range map[string]string{"POSTGRES_MIGRATION_RUN_ID": config.RunID, "POSTGRES_MIGRATION_MAJOR": strconv.Itoa(config.Major)} {
			mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, app.ID, "production", name, value))
		}
		f.sql = &projectContractSQL{fixture: fixture, config: config, seen: map[string]bool{}}
		// Retire native guests before the fixture drops customer roles/database.
		t.Cleanup(func() { f.cleanup(t) })
	}
	return setup
}
func (s *projectContractSQL) observeReport(t *testing.T, r projectWorkerNativeReport) {
	t.Helper()
	if r.SQLValue != s.config.Marker() || r.SQLWrites <= s.lastWrites {
		t.Fatal("mixed-version SQL lost data or counter progress")
	}
	s.lastWrites = r.SQLWrites
	s.seen[r.Generation] = true
}
func (s *projectContractSQL) beforeAdmission(t *testing.T, f *projectWorkerNativeFixture, slug string) {
	t.Helper()
	q := probesql.New()
	id, _ := s.config.ID()
	columns, err := q.SchemaColumns(t.Context(), s.fixture.Data)
	mustProjectWorkerNative(t, err)
	if !columns.Legacy || !columns.Current || !s.seen["stable"] || !s.seen["progressive"] {
		t.Fatal("expansion lacked real old/new compatibility observations")
	}
	row, err := q.ReadCurrent(t.Context(), s.fixture.Data, id)
	mustProjectWorkerNative(t, err)
	if row.Value != s.config.Marker() || row.Writes < s.lastWrites {
		t.Fatal("expansion lost existing customer data")
	}
	s.before = row.Writes
	// An ordinary manual task can use runtime SQL but cannot elevate itself by
	// choosing the exact cleanup command. Both execute in real task guests.
	for _, command := range []string{"migration-check-runtime", "migration-success"} {
		task, err := f.owner.CreateAppTask(t.Context(), slug, api.CreateAppTaskRequest{Command: []string{"/projectworker", command}, TimeoutSeconds: 30})
		mustProjectWorkerNative(t, err)
		waitProjectWorkerNative(t, time.Minute, "manual task credential audience", func() bool {
			current, err := f.owner.GetAppTask(t.Context(), slug, task.ID)
			mustProjectWorkerNative(t, err)
			if !current.Status.Terminal() {
				return false
			}
			want := 0
			if command == "migration-success" {
				want = 2
			}
			if current.ExitCode == nil || *current.ExitCode != want {
				t.Fatal("manual task credential audience violated")
			}
			return true
		})
	}
}
func (s *projectContractSQL) awaitHeldTransaction(t *testing.T) {
	t.Helper()
	q := probesql.New()
	waitProjectWorkerNative(t, 30*time.Second, "staged transactional cleanup DDL", func() bool {
		held, err := q.ContractSessionHeld(t.Context(), s.fixture.Data, s.config.SessionName())
		mustProjectWorkerNative(t, err)
		return held
	})
}
func (s *projectContractSQL) afterOutcome(t *testing.T, f *projectWorkerNativeFixture, mode string) {
	t.Helper()
	q := probesql.New()
	id, _ := s.config.ID()
	waitProjectWorkerNative(t, 3*time.Minute, "cleanup SQL session quiescence", func() bool {
		held, err := q.ContractSessionHeld(t.Context(), s.fixture.Data, s.config.SessionName())
		mustProjectWorkerNative(t, err)
		return !held
	})
	columns, err := q.SchemaColumns(t.Context(), s.fixture.Data)
	mustProjectWorkerNative(t, err)
	if mode == "interrupted" {
		if !columns.Legacy || !columns.Current {
			t.Fatal("interrupted DDL did not preserve the expanded schema")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		old, err := q.ReadLegacy(ctx, s.fixture.Data, id)
		mustProjectWorkerNative(t, err)
		if old.Value != s.config.Marker() || old.Writes < s.before {
			t.Fatal("interruption lost committed customer data")
		}
		// Explicit operator recovery after observing a quiescent SQL transaction.
		// The deployment endpoint still refuses another cleanup task below.
		mustProjectWorkerNative(t, s.config.Contract(ctx, false))
	} else if columns.Legacy || !columns.Current {
		t.Fatal("cleanup did not contract obsolete schema")
	}
	current, err := q.ReadCurrent(t.Context(), s.fixture.Data, id)
	mustProjectWorkerNative(t, err)
	if current.Value != s.config.Marker() || current.Writes < s.before {
		t.Fatal("cleanup/recovery lost customer data")
	}
	_, err = q.ReadLegacy(t.Context(), s.fixture.Data, id)
	var sqlError *pgconn.PgError
	if !errors.As(err, &sqlError) || sqlError.Code != "42703" {
		t.Fatal("contracted schema still admitted old code")
	}
	f.assertHTTP(t, "progressive", "progressive-config")
	t.Logf("managed contract native: run=%s outcome=%s data=true old_new=true serving_ddl=denied manual_ddl=denied recovery=%t", s.config.RunID, mode, mode == "interrupted")
}
