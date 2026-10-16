//go:build !no_pg && !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type consumerAcceptanceBackend struct {
	client  *api.Client
	store   *state.PgStore
	account string
	token   string
}

func startConsumerAcceptanceBackend(t *testing.T) consumerAcceptanceBackend {
	t.Helper()
	binary := os.Getenv("GREGALE_CONSUMER_ACCEPTANCE_BINARY")
	if binary == "" {
		t.Skip("run make test-gregale-recovery-postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestScenarioConsumerAcceptanceServer$", "-test.timeout=3m")
	cmd.Env = append(os.Environ(), "GREGALE_CONSUMER_ACCEPTANCE_SERVER=1")
	cmd.ExtraFiles = []*os.File{write}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		write.Close()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		write.Close()
		t.Fatal(err)
	}
	write.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("API fixture failed: %v\n%s", err, output.String())
			}
		case <-time.After(10 * time.Second):
			cancel()
			<-done
			t.Error("API fixture did not stop")
		}
	})
	var ready struct{ URL, Token, AccountID, Database, Schema string }
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
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid acceptance database config")
	}
	cfg.ConnConfig.Database = ready.Database
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{ready.Schema}.Sanitize() + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect to acceptance database", err)
	}
	t.Cleanup(pool.Close)
	return consumerAcceptanceBackend{api.NewClient(ready.URL, ready.Token), state.NewPgStore(pool), ready.AccountID, ready.Token}
}

// Only transport delivery is changed. All responses and durable effects are
// produced by the authenticated production handlers in the API subprocess.
type consumerAcceptanceDelivery struct {
	target                         *url.URL
	createFirst                    bool
	lostRun, delayedRun            string
	entered, release, committed    chan struct{}
	releaseOnce                    sync.Once
	mu                             sync.Mutex
	lostCreate, lostCancel, parked bool
	key                            api.ConsumerKeyResponse
	puts, keys, cancels            int
}

func (d *consumerAcceptanceDelivery) releaseCreation() { d.releaseOnce.Do(func() { close(d.release) }) }

func (d *consumerAcceptanceDelivery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	park, cancelling, ok := d.beforeForward(r)
	if !ok {
		return
	}
	req := r.Clone(r.Context())
	req.URL.Scheme = d.target.Scheme
	req.URL.Host = d.target.Host
	req.RequestURI = ""
	req.Host = d.target.Host
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
		t.Run(order, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, order == "create-first") })
	}
}

func testConsumerPostgresPortableRecovery(t *testing.T, createFirst bool) {
	t.Helper()
	backend := startConsumerAcceptanceBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := newCampaignFaultHistoryFixture(t)
	target, _ := url.Parse(backend.client.BaseURL())
	delivery := &consumerAcceptanceDelivery{target: target, createFirst: createFirst, lostRun: strings.Repeat("1", 32), delayedRun: strings.Repeat("2", 32), entered: make(chan struct{}), release: make(chan struct{}), committed: make(chan struct{})}
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
		journals[i] = writer.journal
	}
	delayed := journals[1].Faults.Consumers["customer"]
	req := api.ScenarioTestConsumerRequest{AppID: delayed.AppID, ExpiresAt: delayed.ExpiresAt}
	late := make(chan error, 1)
	go func() {
		_, err := client.EnsureScenarioTestConsumer(ctx, journals[1].RunID, "customer", req)
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
	first, err := cleanupTestCampaignFaultHistory(ctx, client, f.plan, f.cp, f.scenarios, work, resolver, true)
	if err == nil || first.Complete {
		t.Fatal("lost cancellation response certified completion")
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

func assertConsumerAcceptanceKey(t *testing.T, b consumerAcceptanceBackend, app string, key api.ConsumerKeyResponse, want bool) {
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
		d.releaseCreation()
	}
	return drop, nil
}
