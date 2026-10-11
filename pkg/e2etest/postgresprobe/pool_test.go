package postgresprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type poolFixture struct {
	config       Config
	workload     *PoolWorkload
	fixture      *postgresfixture.Fixture
	proxy        postgresfixture.Proxy
	store        *state.PgStore
	account, app string
	bindings     map[mp.CredentialAccess]mp.Binding
}

func newPoolFixture(t *testing.T) poolFixture {
	t.Helper()
	catalog := pgtest.OpenMigrated(t)
	if catalog == nil {
		t.Fatal("pool test did not open its catalog")
	}
	store := state.NewPgStore(catalog)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@pool.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "pool-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "go124", RAMMB: 256, CPUMillicores: 250})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	proxy := postgresfixture.OpenProxy(t, catalog, "127.0.0.1", 0)
	fixture := postgresfixture.New(t, catalog, identity, []byte(strings.Repeat("h", 32)), proxy.Host, proxy.Port)
	if fixture == nil {
		t.Fatal("pool test did not open its customer SQL fixture")
	}
	database, bindings := fixture.Create(t, account.ID, app.ID)
	t.Cleanup(func() {
		fixture.Enabled = false
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, binding := range bindings {
			if _, err := fixture.Bindings.Delete(ctx, account.ID, binding.ID); err != nil {
				t.Error("pool fixture binding cleanup failed")
			}
			if err := fixture.Observer.Deleted(ctx, binding); err != nil {
				t.Error("pool fixture retained an encrypted secret")
			}
		}
		if _, err := fixture.Service.Delete(ctx, account.ID, database.ID); err != nil {
			t.Error("pool fixture database cleanup failed")
		}
	})
	uri := func(access mp.CredentialAccess) string {
		t.Helper()
		value, err := fixture.Observer.URI(t.Context(), bindings[access])
		if err != nil {
			t.Fatal("pool credential observation failed")
		}
		return value
	}
	config := Config{RunID: uuid.NewString(), RuntimeURI: uri(mp.CredentialReadWrite), ReaderURI: uri(mp.CredentialReadOnly), MigrationURI: uri(mp.CredentialMigration), PostgresMajor: fixture.Major}
	if err := config.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.MigrationURI = ""
	workload, err := NewPoolWorkload(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workload.Close)
	return poolFixture{config: config, workload: workload, fixture: fixture, proxy: proxy, store: store, account: account.ID, app: app.ID, bindings: bindings}
}

func (f poolFixture) counter(t *testing.T) int64 {
	t.Helper()
	var value int64
	if err := f.fixture.Data.QueryRow(t.Context(), "SELECT counter FROM public.gregale_durable_qualification_probe WHERE id=$1", f.config.RunID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func poolHook(p *PoolWorkload, remote, header string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, RestorePath, nil)
	request.RemoteAddr = remote
	request.Header.Set("X-Faas-After-Restore", header)
	response := httptest.NewRecorder()
	p.Handler().ServeHTTP(response, request)
	return response
}

func TestPoolProbeRetainsIdleSessionsAndRestoresAfterDisconnect(t *testing.T) {
	f := newPoolFixture(t)
	first, err := f.workload.Check(t.Context())
	if err != nil || first.Counter != 1 || first.PoolGeneration != 1 {
		t.Fatal("initial pooled workload failed")
	}
	// Exceed the old proxy's unconditional thirty-second session deadline.
	// No SQL is issued during this interval and no connection is repaired.
	timer := time.NewTimer(31 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("idle pool test canceled")
	}
	second, err := f.workload.Check(t.Context())
	if err != nil || second.Counter != 2 || second.WriterConnections != first.WriterConnections || second.ReaderConnections != first.ReaderConnections {
		t.Fatal("idle pools lost their retained TLS sessions")
	}
	f.proxy.Disconnect()
	if response := poolHook(f.workload, "127.0.0.1:12345", "1"); response.Code != http.StatusNoContent {
		t.Fatal("restore did not reconnect pools")
	}
	third, err := f.workload.Check(t.Context())
	if err != nil || third.Counter != 3 || third.Marker != first.Marker || third.PoolGeneration != 2 || third.WriterConnections <= second.WriterConnections || third.ReaderConnections <= second.ReaderConnections {
		t.Fatal("restore did not replace broken sessions and preserve SQL data")
	}
	if response := poolHook(f.workload, "[::1]:12345", "1"); response.Code != http.StatusNoContent || f.counter(t) != 3 {
		t.Fatal("repeated restore hook mutated data")
	}
	if _, err := f.fixture.Data.Exec(t.Context(), "TRUNCATE public.gregale_durable_qualification_probe"); err != nil {
		t.Fatal(err)
	}
	if f.workload.Restore(t.Context()) == nil {
		t.Fatal("restore repaired or accepted missing data")
	}
}

func TestPoolProbeConcurrentRequestsAndCanceledWrite(t *testing.T) {
	f := newPoolFixture(t)
	const count = 16
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, count)
	for range count {
		go func() { result, err := f.workload.Check(t.Context()); results <- outcome{result, err} }()
	}
	counters := make(map[int64]bool)
	for range count {
		completed := <-results
		if completed.err != nil {
			t.Fatal("concurrent pooled SQL request failed")
		}
		counters[completed.result.Counter] = true
	}
	if len(counters) != count || f.counter(t) != count {
		t.Fatal("concurrent requests lost or duplicated writes")
	}
	lock, err := f.fixture.Data.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), "SELECT counter FROM public.gregale_durable_qualification_probe WHERE id=$1 FOR UPDATE", f.config.RunID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { defer close(done); _, err := f.workload.Check(ctx); done <- err }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("canceled SQL request did not exit")
		}
	})
	writer, err := pgx.ParseConfig(f.config.RuntimeURI)
	if err != nil {
		t.Fatal("invalid private writer URI")
	}
	wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := f.fixture.Data.QueryRow(wait, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename=$1 AND wait_event_type='Lock')", writer.User).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("writer never reached the injected row lock")
		case <-ticker.C:
		}
	}
	deadline, end := context.WithTimeout(t.Context(), 30*time.Millisecond)
	started := time.Now()
	if f.workload.Restore(deadline) == nil {
		t.Fatal("restore ignored the active SQL request")
	}
	end()
	if time.Since(started) > time.Second {
		t.Fatal("restore waited beyond its caller deadline")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("write cancellation did not return promptly")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.counter(t) != count {
		t.Fatal("canceled write was committed or replayed")
	}
	result, err := f.workload.Check(t.Context())
	if err != nil || result.Counter != count+1 || result.PoolGeneration != 1 {
		t.Fatal("pool did not recover after canceling its blocked write")
	}
}

func TestPoolProbeRestoreRejectsUntrustedRequestsAndSQLFailure(t *testing.T) {
	f := newPoolFixture(t)
	for _, request := range []struct{ remote, header string }{
		{"127.0.0.1:12345", ""}, {"192.0.2.1:12345", "1"}, {"not-a-peer", "1"}, {"[2001:db8::1]:12345", "1"},
	} {
		if response := poolHook(f.workload, request.remote, request.header); response.Code != http.StatusForbidden {
			t.Fatal("untrusted request invoked the reconnect hook")
		}
	}
	if f.workload.generation != 1 || f.counter(t) != 0 {
		t.Fatal("untrusted restore mutated the workload")
	}
	for _, invalid := range []Config{
		{RunID: f.config.RunID, RuntimeURI: f.config.ReaderURI, ReaderURI: f.config.ReaderURI, PostgresMajor: f.config.PostgresMajor},
		{RunID: f.config.RunID, RuntimeURI: f.config.RuntimeURI, ReaderURI: f.config.RuntimeURI, PostgresMajor: f.config.PostgresMajor},
		{RunID: f.config.RunID, RuntimeURI: f.config.RuntimeURI, ReaderURI: f.config.ReaderURI, MigrationURI: "private-migration", PostgresMajor: f.config.PostgresMajor},
	} {
		workload, err := NewPoolWorkload(t.Context(), invalid)
		if workload != nil {
			workload.Close()
		}
		if err == nil {
			t.Fatal("pooled workload accepted unsafe SQL authority")
		}
	}
	f.proxy.SetAvailable(false)
	started := time.Now()
	response := poolHook(f.workload, "127.0.0.1:12345", "1")
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > 2*time.Second || strings.Contains(response.Body.String(), "postgres://") || strings.Contains(response.Body.String(), "postgresql://") {
		t.Fatal("SQL outage did not fail the restore promptly and privately")
	}
	health := httptest.NewRecorder()
	f.workload.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusServiceUnavailable || f.counter(t) != 0 {
		t.Fatal("failed restore published readiness or mutated data")
	}
	f.proxy.SetAvailable(true)
	if response := poolHook(f.workload, "[::ffff:127.0.0.1]:12345", "1"); response.Code != http.StatusNoContent {
		t.Fatal("restore did not recover when SQL returned")
	}
	result, err := f.workload.Check(t.Context())
	if err != nil || result.Counter != 1 || result.PoolGeneration != 2 {
		t.Fatal("SQL recovery lost data or accepted the failed restore")
	}
	f.workload.Close()
	f.workload.Close()
	if _, err := f.workload.Check(t.Context()); err == nil {
		t.Fatal("closed pooled workload still served SQL")
	}
	if f.workload.Restore(t.Context()) == nil {
		t.Fatal("closed pooled workload restored")
	}
}

func TestPoolProbeRotationRetiresHeldSessionsAndPreservesData(t *testing.T) {
	f := newPoolFixture(t)
	first, err := f.workload.Check(t.Context())
	if err != nil || first.Counter != 1 {
		t.Fatal("initial pooled workload failed")
	}
	old := f.bindings[mp.CredentialReadWrite]
	rotated, err := f.fixture.Bindings.Rotate(t.Context(), f.account, old.ID)
	if err != nil {
		t.Fatal("pool fixture rotation failed")
	}
	f.bindings[mp.CredentialReadWrite] = rotated
	config := f.config
	config.RuntimeURI, err = f.fixture.Observer.URI(t.Context(), rotated)
	if err != nil {
		t.Fatal("rotated credential observation failed")
	}
	replacement, err := NewPoolWorkload(t.Context(), config)
	if err != nil {
		t.Fatal("replacement guest did not authenticate new pools")
	}
	t.Cleanup(replacement.Close)
	second, err := replacement.Check(t.Context())
	if err != nil || second.Counter != 2 || second.Marker != first.Marker || second.RuntimeProof == first.RuntimeProof || second.ReaderProof != first.ReaderProof {
		t.Fatal("pool rotation changed data or the reader credential")
	}
	// Local SQL acceptance has no guests. Deployed acceptance must obtain
	// this receipt from the scheduler after withdrawing old resident guests.
	if err := f.store.FinalizeManagedPostgresBindingRotationsForApp(t.Context(), f.app, rotated.RotationWakeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.fixture.Bindings.ReconcileRotationCleanup(t.Context(), f.account, old.ID); err != nil {
		t.Fatal("retired pool credential cleanup failed")
	}
	if err := f.fixture.VerifyRevoked(t.Context(), f.config.RuntimeURI); err != nil {
		t.Fatal("retired credential still authenticates")
	}
	if _, err := f.workload.Check(t.Context()); err == nil {
		t.Fatal("retired pool session still writes")
	}
	if f.workload.Restore(t.Context()) == nil || f.counter(t) != 2 {
		t.Fatal("retired credential reconnected or mutated data")
	}
	if _, err := replacement.reader.Exec(t.Context(), "UPDATE public.gregale_durable_qualification_probe SET counter=999"); err == nil {
		t.Fatal("reader pool obtained write privileges")
	}
	response := httptest.NewRecorder()
	replacement.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/probe", nil))
	var result Result
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Counter != 3 || result.PoolGeneration != 1 || strings.Contains(response.Body.String(), "postgres") {
		t.Fatal("replacement pool did not serve a private SQL result")
	}
}
