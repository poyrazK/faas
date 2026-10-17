//go:build !no_pg && !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type consumerAcceptanceBackend struct {
	client                           *api.Client
	store                            *state.PgStore
	account, token, database, schema string
	mu                               sync.Mutex
	process                          *consumerAcceptanceProcess
	crashes                          atomic.Int32
	restarts                         int
	databaseProxy                    *consumerAcceptanceDatabaseProxy
}

type consumerAcceptanceProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	output bytes.Buffer
	once   sync.Once
	killed atomic.Bool
	err    error
}

func (p *consumerAcceptanceProcess) stop(crash bool) error {
	p.once.Do(func() {
		if crash {
			if err := p.cmd.Process.Kill(); err != nil {
				p.err = fmt.Errorf("kill API fixture: %w", err)
				return
			}
		}
		p.stdin.Close()
		select {
		case err := <-p.done:
			if crash {
				var exited *exec.ExitError
				if !errors.As(err, &exited) {
					p.err = errors.New("API fixture was not killed")
					return
				}
				status, ok := exited.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					p.err = errors.New("API fixture did not exit with SIGKILL")
					return
				}
				p.killed.Store(true)
			} else if err != nil {
				p.err = fmt.Errorf("API fixture failed: %w\n%s", err, p.output.String())
			}
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
			p.err = errors.New("API fixture did not stop")
		}
	})
	return p.err
}

func startConsumerAcceptanceBackend(t *testing.T, databaseProxy ...bool) *consumerAcceptanceBackend {
	t.Helper()
	if os.Getenv("GREGALE_CONSUMER_ACCEPTANCE_BINARY") == "" {
		t.Skip("run make test-gregale-recovery-postgres")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	b := &consumerAcceptanceBackend{store: state.NewPgStore(pool)}
	account, err := b.store.CreateAccount(ctx, "consumer-acceptance@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	b.account = account.ID
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.CreateAPIKey(ctx, account.ID, hash, "consumer-acceptance", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	b.token = plain
	if err := pool.QueryRow(ctx, `select current_database(),current_schema()`).Scan(&b.database, &b.schema); err != nil {
		t.Fatal(err)
	}
	if len(databaseProxy) > 0 && databaseProxy[0] {
		b.databaseProxy = startConsumerAcceptanceDatabaseProxy(t, pool.Config())
	}
	b.start(t)
	return b
}

func (b *consumerAcceptanceBackend) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	p := &consumerAcceptanceProcess{done: make(chan error, 1)}
	p.cmd = exec.CommandContext(ctx, os.Getenv("GREGALE_CONSUMER_ACCEPTANCE_BINARY"), "-test.run=^TestScenarioConsumerAcceptanceServer$", "-test.timeout=3m")
	p.cmd.Env = append(os.Environ(), "GREGALE_CONSUMER_ACCEPTANCE_SERVER=1")
	p.cmd.ExtraFiles = []*os.File{write}
	p.cmd.Stdout = &p.output
	p.cmd.Stderr = &p.output
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		write.Close()
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		write.Close()
		p.stdin.Close()
		t.Fatal(err)
	}
	write.Close()
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() {
		if err := p.stop(false); err != nil {
			t.Error(err)
		}
	})
	host := ""
	var port uint16
	if b.databaseProxy != nil {
		address := b.databaseProxy.listener.Addr().(*net.TCPAddr)
		host = "127.0.0.1"
		port = uint16(address.Port)
	}
	if err := json.NewEncoder(p.stdin).Encode(struct {
		Database, Schema, Host string
		Port                   uint16
	}{b.database, b.schema, host, port}); err != nil {
		t.Fatal("select API fixture database", err)
	}
	var ready struct {
		URL, Database, Schema string
		PID                   int
	}
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(read).Decode(&ready) }()
	select {
	case err := <-decoded:
		if err != nil {
			t.Fatal("API fixture readiness failed", err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("API fixture readiness timed out")
	}
	if ready.Database != b.database || ready.Schema != b.schema || ready.PID != p.cmd.Process.Pid {
		t.Fatal("API fixture identity changed")
	}
	b.mu.Lock()
	b.process = p
	b.mu.Unlock()
	b.client = api.NewClient(ready.URL, b.token)
	// This authenticated read proves the original bearer survived the restart.
	if _, err := b.client.GetScenarioTestRegistration(ctx, strings.Repeat("f", 32)); err != nil {
		t.Fatal("persisted authentication failed", err)
	}
}

func (b *consumerAcceptanceBackend) crash() error {
	b.mu.Lock()
	p := b.process
	b.mu.Unlock()
	if err := p.stop(true); err != nil {
		return err
	}
	b.crashes.Add(1)
	return nil
}

func (b *consumerAcceptanceBackend) restart(t *testing.T, d *consumerAcceptanceDelivery) {
	t.Helper()
	b.mu.Lock()
	old := b.process
	b.mu.Unlock()
	if !old.killed.Load() {
		t.Fatal("restart requires a verified API process crash")
	}
	b.start(t)
	if old.cmd.Process.Pid == b.process.cmd.Process.Pid {
		t.Fatal("API process was reused")
	}
	target, err := url.Parse(b.client.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.target = target
	d.mu.Unlock()
	b.restarts++
}

// Only transport delivery is changed. All responses and durable effects are
// produced by the authenticated production handlers in the API subprocess.
type consumerAcceptanceDelivery struct {
	target                         *url.URL
	createFirst                    bool
	crashAfterCommit               func() error
	unavailable                    bool
	lostRun, delayedRun            string
	entered, release, committed    chan struct{}
	releaseOnce                    sync.Once
	mu                             sync.Mutex
	lostCreate, lostCancel, parked bool
	key                            api.ConsumerKeyResponse
	lostConsumerID                 string
	puts, keys, cancels            int
	completedReads                 map[string]int
	requests                       int
	mutations                      int
}

func (d *consumerAcceptanceDelivery) releaseCreation() { d.releaseOnce.Do(func() { close(d.release) }) }

func (d *consumerAcceptanceDelivery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		defer func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.completedReads == nil {
				d.completedReads = map[string]int{}
			}
			d.completedReads[r.URL.RequestURI()]++
		}()
	}

	d.mu.Lock()
	d.requests++
	if r.Method != "GET" && r.Method != "HEAD" {
		d.mutations++
	}
	unavailable := d.unavailable
	d.mu.Unlock()
	if unavailable {
		http.Error(w, "API unavailable", 503)
		return
	}
	park, cancelling, ok := d.beforeForward(r)
	if !ok {
		return
	}
	req := r.Clone(r.Context())
	d.mu.Lock()
	target := *d.target
	d.mu.Unlock()
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.RequestURI = ""
	req.Host = target.Host
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		http.Error(w, "upstream response failed", 502)
		return
	}
	if park {
		close(d.committed)
	}
	drop, err := d.afterForward(r, response.StatusCode, body, cancelling)
	if err != nil {
		http.Error(w, "invalid key response", 502)
		return
	}
	if drop {
		http.Error(w, "injected response loss after commit", 503)
		return
	}
	for k, values := range response.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func TestScenarioConsumerPostgresPortableRecovery(t *testing.T) {
	// adr: 984 — a recipient recovers real HTTP/SQL effects after delivery failures.
	for _, order := range []string{"cancel-first", "create-first"} {
		t.Run(order, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, order == "create-first", false) })
	}
}

func TestScenarioConsumerPostgresRestartRecovery(t *testing.T) {
	// adr: 984 — receipts, credentials and fences survive real API process deaths.
	for _, order := range []string{"cancel-first", "create-first"} {
		t.Run(order, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, order == "create-first", true) })
	}
}

func TestScenarioConsumerPostgresOutageRecovery(t *testing.T) {
	// adr: 984 — an interrupted recipient stays unresolved until API/SQL connectivity returns.
	for _, mode := range []string{"api-unavailable", "database-unavailable"} {
		t.Run(mode, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, false, false, mode) })
	}
}

func testConsumerPostgresPortableRecovery(t *testing.T, createFirst, restart bool, outages ...string) {
	t.Helper()
	outage := ""
	if len(outages) > 0 {
		outage = outages[0]
	}
	backend := startConsumerAcceptanceBackend(t, strings.Contains(outage, "database-unavailable"))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := newCampaignFaultHistoryFixture(t)
	target, _ := url.Parse(backend.client.BaseURL())
	delivery := &consumerAcceptanceDelivery{target: target, createFirst: createFirst, lostRun: strings.Repeat("1", 32), delayedRun: strings.Repeat("2", 32), entered: make(chan struct{}), release: make(chan struct{}), committed: make(chan struct{})}
	if restart {
		delivery.crashAfterCommit = backend.crash
	}
	proxy := httptest.NewServer(delivery)
	t.Cleanup(proxy.Close)
	t.Cleanup(delivery.releaseCreation)
	// Keep the real bearer private; this client changes only the destination.
	client := api.NewClient(proxy.URL, backend.token)
	journals := make([]testCampaignRunJournal, 3)
	for i, dir := range f.dirs {
		saved, err := readTestCampaignFaultJournals(dir, "a", f.scenarios["checkout"])
		if err != nil {
			t.Fatal(err)
		}
		j := saved[0]
		session, err := client.UpsertDevSession(ctx, "checkout", api.UpsertDevSessionRequest{WorkspaceID: j.RunID})
		if err != nil {
			t.Fatal("create developer session", err)
		}
		if err := client.RegisterScenarioTest(ctx, j.RunID, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{{Workload: "checkout", AppSlug: session.App.Slug}}}); err != nil {
			t.Fatal(err)
		}
		j.Workloads["checkout"] = testCampaignJournalWorkload{Project: "checkout", AppID: session.App.ID, State: "provisioned"}
		j.Faults.Consumers = map[string]testCampaignFaultConsumer{}
		writer := &testCampaignJournalWriter{directory: testCampaignJournalDirectory(dir, "a"), primaryProject: "checkout", journal: j}
		if i == 1 {
			expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
			req := api.ScenarioTestConsumerRequest{AppID: session.App.ID, ExpiresAt: expiry}
			digest, _ := api.ScenarioTestConsumerIntent(j.RunID, "customer", req)
			if err := writer.consumer("customer", testCampaignFaultConsumer{AppID: session.App.ID, AppSlug: session.App.Slug, ExternalRef: "gregale-test-" + j.RunID + "-customer", ExpiresAt: expiry, CreationIntentSHA256: digest}); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, _, err := provisionTestConsumers(ctx, client, session.App.Slug, f.scenarios["checkout"].Consumers, j.RunID, time.Minute, writer); err == nil {
				t.Fatal("lost response was not observed")
			}
		}
		if restart && i != 1 {
			backend.restart(t, delivery)
			record := writer.journal.Faults.Consumers["customer"]
			receipt, err := client.LookupScenarioTestConsumer(ctx, j.RunID, "customer", api.ScenarioTestConsumerRequest{AppID: record.AppID, ExpiresAt: record.ExpiresAt})
			recovered, verify := verifiedTestConsumerReceipt(receipt, j.RunID, "customer", api.ScenarioTestConsumerRequest{AppID: record.AppID, ExpiresAt: record.ExpiresAt}, record.CreationIntentSHA256)
			delivery.mu.Lock()
			originalID := delivery.lostConsumerID
			delivery.mu.Unlock()
			if record.ID != "" {
				originalID = record.ID
			}
			if err != nil || verify != nil || recovered.Status != "active" || recovered.ID != originalID {
				t.Fatal("creation receipt did not survive restart")
			}
		}
		journals[i] = writer.journal
	}
	delayed := journals[1].Faults.Consumers["customer"]
	req := api.ScenarioTestConsumerRequest{AppID: delayed.AppID, ExpiresAt: delayed.ExpiresAt}
	lateClient := client
	if outage == "watch:wait-database-unavailable" {
		// This request is intentionally parked across a full 30s verification
		// attempt. Keep it alive until the real cancellation fence is committed;
		// the fixture context still bounds it and status retains production limits.
		lateClient = api.NewClient(proxy.URL, backend.token)
		lateClient.HTTPClient().Timeout = 90 * time.Second
	}
	late := make(chan error, 1)
	go func() {
		_, err := lateClient.EnsureScenarioTestConsumer(ctx, journals[1].RunID, "customer", req)
		late <- err
	}()
	select {
	case <-delivery.entered:
	case <-ctx.Done():
		t.Fatal("creation was not parked")
	}
	if journals[0].Faults.Consumers["customer"].ID != "" || journals[2].Faults.Consumers["customer"].ID == "" {
		t.Fatal("lost responses did not preserve expected journal boundaries")
	}
	delivery.mu.Lock()
	key := delivery.key
	delivery.mu.Unlock()
	assertConsumerAcceptanceKey(t, backend, journals[2].Faults.Consumers["customer"].AppID, key, true)
	manifest, err := os.ReadFile(filepath.Join(f.root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(f.root, "plan.json")
	mustCampaignFaultJSON(t, planPath, f.plan)
	bundlePath := filepath.Join(t.TempDir(), "handoff.json")
	if code := cmdTestCampaignBundleExport([]string{"--plan", planPath, "--policy", filepath.Join(f.root, "policy.yaml"), "--manifest", filepath.Join(f.root, "manifest.yaml"), "--directory", f.dirs[2], "--output", bundlePath}); code != 0 {
		t.Fatal("CLI bundle export", code)
	}
	claims := filepath.Join(t.TempDir(), "claims")
	if err := os.Mkdir(claims, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "recipient")
	if code := cmdTestCampaignBundle([]string{"import", "--from", bundlePath, "--directory", root, "--activate", "--claim-directory", claims}); code != 0 {
		t.Fatal("CLI bundle import", code)
	}
	manifestPath := filepath.Join(root, "recipient-manifest.yaml")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(filepath.Join(root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	frozen := campaignFaultFrozenBytes(t, []string{filepath.Join(root, "artifacts")})
	work := filepath.Join(root, "work")
	if strings.HasPrefix(outage, "watch:") {
		testConsumerPostgresCleanupWatch(t, strings.TrimPrefix(outage, "watch:"), backend, delivery, proxy.URL, root, manifestPath, work, journals, key)
		if err := readTestCampaignJSON(filepath.Join(work, "checkpoint.json"), &f.cp); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-late:
			if !isConflictAcceptance(err) {
				t.Fatal("late creation was not fenced", err)
			}
		case <-ctx.Done():
			t.Fatal("delayed creation did not finish")
		}
	} else {
		if outage != "" {
			setOutage := func(active bool) {
				if backend.databaseProxy != nil {
					backend.databaseProxy.setOnline(!active)
				} else {
					delivery.mu.Lock()
					delivery.unavailable = active
					delivery.mu.Unlock()
				}
			}
			setOutage(true)
			failed, err := cleanupTestCampaignFaultHistory(ctx, client, f.plan, f.cp, f.scenarios, work, resolver, true)
			if err == nil || failed.Complete || failed.Issue == nil || !failed.Issue.Retryable || len(failed.Faults) != 3 || len(failed.Sessions) != 3 {
				t.Fatal("outage did not preserve unresolved recovery inventory")
			}
			for _, o := range failed.Faults {
				if o.Consumers["customer"] != "unverified" || o.Registration != "unverified" {
					t.Fatal("outage became resource absence")
				}
			}
			if len(failed.Faults[0].Issues) != 1 || failed.Faults[0].Issues[0].Attempts != 3 {
				t.Fatal("lookup retry budget was not enforced")
			}
			completion := testCampaignRecoveryCompletion{ResourceEvidenceComplete: true, KnownSessionsAbsent: true}
			applyTestCampaignFaultCompletion(ctx, &completion, client, f.plan, f.cp, f.scenarios, work, true, resolver)
			if completion.FaultResourcesVerified || completion.FaultResourcesAbsent || completion.RecoveryComplete {
				t.Fatal("outage certified recovery completion")
			}
			delivery.mu.Lock()
			cancels := delivery.cancels
			keys := delivery.keys
			delivery.mu.Unlock()
			if cancels != 0 || keys != 1 {
				t.Fatal("outage preflight mutated resources")
			}
			assertConsumerAcceptanceKey(t, backend, journals[2].Faults.Consumers["customer"].AppID, key, true)
			setOutage(false)
		}
		inspect, err := cleanupTestCampaignFaultHistory(ctx, client, f.plan, f.cp, f.scenarios, work, resolver, false)
		if err != nil {
			t.Fatal("read-only inspection", err)
		}
		pending := false
		for _, o := range inspect.Faults {
			if o.Consumers["customer"] == "pending" {
				pending = true
			}
		}
		if !pending {
			t.Fatal("unresolved creation was not pending")
		}
		delivery.mu.Lock()
		if delivery.cancels != 0 {
			t.Error("inspection cancelled creation")
		}
		delivery.mu.Unlock()
		assertConsumerAcceptanceKey(t, backend, journals[2].Faults.Consumers["customer"].AppID, key, true)
		for _, o := range inspect.Faults {
			expected := "active"
			if o.RunID == journals[1].RunID {
				expected = "pending"
			}
			if o.Consumers["customer"] != expected {
				t.Fatal("read-only inspection changed consumer state")
			}
		}
		first, err := cleanupTestCampaignFaultHistory(ctx, client, f.plan, f.cp, f.scenarios, work, resolver, true)
		if err == nil || first.Complete {
			t.Fatal("lost cancellation response certified completion")
		}
		if restart {
			backend.restart(t, delivery)
			// The current generation was revoked before the cancellation response loss.
			assertConsumerAcceptanceKey(t, backend, journals[2].Faults.Consumers["customer"].AppID, key, false)
			proof, err := client.LookupScenarioTestConsumer(ctx, journals[1].RunID, "customer", req)
			if err != nil || !validTestConsumerCancellation(proof, delayed) {
				t.Fatal("cancellation proof did not survive restart")
			}
			delivery.releaseCreation()
		}
		select {
		case err := <-late:
			if createFirst && err != nil {
				t.Fatal("creation should have committed first", err)
			}
			if !createFirst && !isConflictAcceptance(err) {
				t.Fatal("late creation was not fenced", err)
			}
		case <-ctx.Done():
			t.Fatal("delayed creation did not finish")
		}
		for i := 0; i < 2; i++ {
			result, err := cleanupTestCampaignFaultHistory(ctx, client, f.plan, f.cp, f.scenarios, work, resolver, true)
			if err != nil || !result.Complete {
				t.Fatal("recipient cleanup", err)
			}
		}
	}

	t.Setenv("FAAS_API", proxy.URL)
	t.Setenv("FAAS_TOKEN", backend.token)
	args := []string{"--plan", filepath.Join(root, "artifacts", "plan.json"), "--policy", filepath.Join(root, "artifacts", "policy.yaml"), "--manifest", manifestPath, "--directory", work, "--handoff", filepath.Join(root, "bundle-index.json"), "--journal", "--faults", "--fault-history", "--execute"}
	if code := cmdTestCampaignControl("cleanup", args); code != 0 {
		t.Fatal("CLI repeated recipient cleanup", code)
	}
	completion := testCampaignRecoveryCompletion{ResourceEvidenceComplete: true, KnownSessionsAbsent: true}
	applyTestCampaignFaultCompletion(ctx, &completion, client, f.plan, f.cp, f.scenarios, work, true, resolver)
	if !completion.FaultResourcesVerified || !completion.FaultResourcesAbsent || !completion.KnownSessionsAbsent || completion.RecoveryComplete || completion.TestExecutionComplete {
		t.Fatal("resource proof forged execution or did not confirm absence")
	}
	assertConsumerAcceptanceKey(t, backend, journals[2].Faults.Consumers["customer"].AppID, key, false)
	delivery.mu.Lock()
	puts := delivery.puts
	delivery.mu.Unlock()
	if puts != 3 {
		t.Fatal("cleanup reissued consumer creation", puts)
	}
	for i, j := range journals {
		c := j.Faults.Consumers["customer"]
		rows, err := backend.store.ListAPIConsumersForApp(ctx, backend.account, c.AppID)
		if err != nil {
			t.Fatal(err)
		}
		expected := 1
		if i == 1 && !createFirst {
			expected = 0
		}
		if len(rows) != expected {
			t.Fatal("duplicate or unexpected consumer", i, len(rows))
		}
		for _, row := range rows {
			if row.Active() {
				t.Fatal("active consumer survived recovery")
			}
		}
		if _, err := client.GetDevSession(ctx, "checkout", j.RunID); !isNotFound(err) {
			t.Fatal("session survived recovery", err)
		}
		registration, err := client.GetScenarioTestRegistration(ctx, j.RunID)
		if err != nil || len(registration.Members) != 0 {
			t.Fatal("namespace survived recovery", err)
		}
		receipt, err := client.EnsureScenarioTestConsumer(ctx, j.RunID, "customer", api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt})
		if i == 1 {
			if !isConflictAcceptance(err) {
				t.Fatal("creation accepted after cancellation", err)
			}
		} else if err != nil || receipt.Consumer == nil || receipt.Consumer.Status != "revoked" {
			t.Fatal("retry reactivated consumer", err)
		}
	}
	delivery.mu.Lock()
	keys, cancels := delivery.keys, delivery.cancels
	delivery.mu.Unlock()
	if keys != 1 || cancels != 1 {
		t.Fatal("cleanup replayed key provisioning or cancellation", keys, cancels)
	}
	if restart && (backend.crashes.Load() != 3 || backend.restarts != 3) {
		t.Fatal("API crash boundaries were not exercised", backend.crashes.Load(), backend.restarts)
	}
	for _, body := range campaignFaultFrozenBytes(t, []string{root}) {
		if bytes.Contains(body, []byte(key.Key)) {
			t.Fatal("credential persisted in portable evidence")
		}
	}
	assertCampaignFaultFrozen(t, frozen, []string{filepath.Join(root, "artifacts")})
}

func isConflictAcceptance(err error) bool {
	var e *api.APIError
	return errors.As(err, &e) && e.Problem.Status == 409
}

func assertConsumerAcceptanceKey(t *testing.T, b *consumerAcceptanceBackend, app string, key api.ConsumerKeyResponse, want bool) {
	t.Helper()
	if key.Key == "" || key.Prefix == "" {
		t.Fatal("lost key response did not contain a credential")
	}
	row, err := b.store.ConsumerKeyByAppAndPrefix(context.Background(), b.account, app, key.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := b.store.GetAPIConsumerByID(context.Background(), b.account, row.ConsumerID)
	if err != nil {
		t.Fatal(err)
	}
	usable := row.Active(time.Now()) && consumer.Active() && api.ConstantTimeEqualHash(row.Hash, api.HashConsumerKey(key.Key))
	if usable != want {
		t.Fatal("credential usability differs from expected state", usable, want)
	}
}

func (d *consumerAcceptanceDelivery) beforeForward(r *http.Request) (bool, bool, bool) {
	delayed := r.URL.Path == "/v1/dev/test-runs/"+d.delayedRun+"/consumers/customer"
	d.mu.Lock()
	park := delayed && r.Method == "PUT" && !d.parked
	if park {
		d.parked = true
	}
	d.mu.Unlock()
	if park {
		close(d.entered)
		select {
		case <-d.release:
		case <-r.Context().Done():
			return false, false, false
		}
	}
	cancelling := delayed && r.Method == "DELETE" && r.URL.Query().Get("consumer_id") == ""
	if cancelling && d.createFirst {
		d.releaseCreation()
		select {
		case <-d.committed:
		case <-r.Context().Done():
			return false, false, false
		}
	}
	return park, cancelling, true
}

func (d *consumerAcceptanceDelivery) afterForward(r *http.Request, status int, body []byte, cancelling bool) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	drop := false
	if r.Method == "PUT" && strings.Contains(r.URL.Path, "/consumers/") {
		d.puts++
		if strings.Contains(r.URL.Path, "/"+d.lostRun+"/") && !d.lostCreate && status == 200 {
			var receipt api.ScenarioTestConsumerReceipt
			if err := json.Unmarshal(body, &receipt); err != nil || receipt.Consumer == nil {
				return false, errors.New("invalid creation response")
			}
			d.lostConsumerID = receipt.Consumer.ID
			d.lostCreate = true
			drop = true
		}
	}
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/keys") && status == 201 {
		d.keys++
		if err := json.Unmarshal(body, &d.key); err != nil {
			return false, err
		}
		drop = true
	}
	if cancelling && status == 200 {
		d.cancels++
		if !d.lostCancel {
			d.lostCancel = true
			drop = true
		}
		if d.crashAfterCommit == nil {
			d.releaseCreation()
		}
	}
	if drop && d.crashAfterCommit != nil {
		if err := d.crashAfterCommit(); err != nil {
			return false, err
		}
	}
	return drop, nil
}
