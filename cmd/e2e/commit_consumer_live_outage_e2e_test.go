package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

const commitPrometheusScrape = `(min(up{job="commit-schedd"}) == 1)
	and on() (schedd_commit_relay_observation_snapshot_success == 1)`

func commitPrometheusObservation(enabled, unavailable int) string {
	return commitPrometheusScrape + fmt.Sprintf(`
		and on() (schedd_commit_relay_enabled_sources == %d)
		and on() (schedd_commit_relay_unknown_sources == %d)
		and on() (schedd_commit_relay_failing_sources == %d)`, enabled, unavailable, unavailable)
}

func runCommitConsumerLiveOutage(t *testing.T, f *normalPathFixture, consumer *pgxpool.Pool, fixture commitConsumerLoadFixture, binary string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	prometheus := startCommitPrometheus(t, ctx, binary, f.h.ScheddMetricsURL)
	known := commitPrometheusObservation(1, 0)
	unknown := commitPrometheusObservation(1, 1) + " and on() (schedd_commit_relay_oldest_pending_timestamp_seconds == 0)"
	waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known)
	var baselineOperations int
	if err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1::uuid`, f.app.ID).Scan(&baselineOperations); err != nil {
		t.Fatal(err)
	}

	// Leave enough time to observe the production rule's five-minute hold and
	// 30-second evaluation interval. A steady low rate keeps recovery bounded.
	const production = 6*time.Minute + 30*time.Second
	var produced atomic.Int64
	result := make(chan commitConsumerLoadResult, 1)
	go func() {
		ids, err := produceCommitConsumerLoad(ctx, fixture.customer, production, 5*time.Second, &produced)
		result <- commitConsumerLoadResult{ids: ids, err: err}
	}()
	defer func() { cancel(); <-result }()
	waitCommitLoadCondition(t, ctx, "initial producer transactions", func() (bool, error) { return produced.Load() >= 2, nil })

	outageStartedAt := time.Now()
	if _, err := fixture.customer.Exec(ctx, `ALTER ROLE relay NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := fixture.customer.Exec(cleanupCtx, `ALTER ROLE relay LOGIN`); err != nil {
			t.Error(err)
		}
	})
	if _, err := fixture.customer.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='relay' AND pid<>pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	t.Log("Commit live relay outage started")
	waitCommitPrometheus(t, ctx, prometheus, 6*time.Minute, "firing", unknown)
	firedAt := time.Now()
	health := waitCommitSourceHealth(t, ctx, f.h, f.key, fixture.source.ID, "database_unavailable", outageStartedAt)
	if health.PendingEvents != nil || health.BlockedEvents != nil || health.OldestPendingAt != nil {
		t.Fatal("unavailable source reported a known backlog")
	}
	var backlog []string
	if err := fixture.customer.QueryRow(ctx, `SELECT ARRAY(SELECT event_id::text FROM public.gregale_outbox WHERE accepted_at IS NULL)`).Scan(&backlog); err != nil || len(backlog) < 2 {
		t.Fatalf("outage retained %d events: %v", len(backlog), err)
	}
	t.Logf("Commit live relay outage alerts firing with %d queued events", len(backlog))

	beforeRecovery := produced.Load()
	var recoveryStartedAt time.Time
	if err := fixture.customer.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&recoveryStartedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.customer.Exec(ctx, `ALTER ROLE relay LOGIN`); err != nil {
		t.Fatal(err)
	}
	waitCommitPrometheus(t, ctx, prometheus, 6*time.Minute, "inactive", known)
	clearedAt := time.Now()
	t.Log("Commit live relay outage alerts cleared")
	waitCommitLoadCondition(t, ctx, "production after restoring relay access", func() (bool, error) {
		return produced.Load() >= beforeRecovery+2, nil
	})
	var workload commitConsumerLoadResult
	select {
	case workload = <-result:
		result <- workload
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if workload.err != nil {
		t.Fatal(workload.err)
	}
	waitCommitConsumerLoadDrain(t, ctx, f, fixture, consumer, workload.ids, baselineOperations)
	waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known+`
		and on() (schedd_commit_relay_pending_events == 0)
		and on() (schedd_commit_relay_blocked_events == 0)`)

	var checkpointedAt, completedAt time.Time
	if err := fixture.customer.QueryRow(ctx, `
		SELECT max(accepted_at) FROM public.gregale_outbox WHERE event_id::text=ANY($1)`, backlog).Scan(&checkpointedAt); err != nil {
		t.Fatal(err)
	}
	if err := f.h.Pool.QueryRow(ctx, `
		SELECT max(completed_at) FROM commit_receipts WHERE source_id=$1::uuid AND event_id::text=ANY($2)`,
		fixture.source.ID, backlog).Scan(&completedAt); err != nil {
		t.Fatal(err)
	}
	if checkpointedAt.Before(recoveryStartedAt) || completedAt.Before(recoveryStartedAt) {
		t.Fatal("outage backlog timestamps precede the restoration of relay access")
	}
	baseline := struct {
		ProducedEvents    int   `json:"produced_events"`
		OutageEvents      int   `json:"outage_backlog_events"`
		OutageToFiringMS  int64 `json:"outage_to_alerts_firing_ms"`
		RestoreToClearMS  int64 `json:"restore_to_alerts_clear_ms"`
		CheckpointDrainMS int64 `json:"restore_to_last_checkpoint_ms"`
		CompletionDrainMS int64 `json:"restore_to_last_completion_ms"`
	}{
		ProducedEvents: len(workload.ids), OutageEvents: len(backlog),
		OutageToFiringMS:  firedAt.Sub(outageStartedAt).Milliseconds(),
		RestoreToClearMS:  clearedAt.Sub(recoveryStartedAt).Milliseconds(),
		CheckpointDrainMS: checkpointedAt.Sub(recoveryStartedAt).Milliseconds(),
		CompletionDrainMS: completedAt.Sub(recoveryStartedAt).Milliseconds(),
	}
	encoded, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit live relay outage baseline %s", encoded)
}

type commitPrometheus struct {
	base   string
	client *http.Client
	done   <-chan struct{}
	log    string
}

func startCommitPrometheus(t *testing.T, ctx context.Context, binary string, metricsURLs ...string) commitPrometheus {
	t.Helper()
	dir := t.TempDir()
	source, err := os.ReadFile("../../deploy/ansible/roles/prometheus/files/faas.rules.yml")
	if err != nil {
		t.Fatal(err)
	}
	var repoRules struct {
		Groups []yaml.Node `yaml:"groups"`
	}
	if err := yaml.Unmarshal(source, &repoRules); err != nil {
		t.Fatal(err)
	}
	var groups []yaml.Node
	for _, group := range repoRules.Groups {
		var header struct {
			Name string `yaml:"name"`
		}
		if err := group.Decode(&header); err != nil {
			t.Fatal(err)
		}
		if header.Name == "faas_commit" {
			groups = append(groups, group)
		}
	}
	if len(groups) != 1 {
		t.Fatal("expected one production Commit rule group")
	}
	rules, err := yaml.Marshal(map[string][]yaml.Node{"groups": groups})
	if err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join(dir, "rules.yml")
	if err := os.WriteFile(rulesPath, rules, 0600); err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, metricsURL := range metricsURLs {
		scheduler, err := url.Parse(metricsURL)
		if err != nil || scheduler.Hostname() != "127.0.0.1" || scheduler.Path != "/metrics" {
			t.Fatalf("expected a loopback scheduler metrics endpoint: %q", metricsURL)
		}
		targets = append(targets, scheduler.Host)
	}
	targetList, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("global:\n  scrape_interval: 2s\n  scrape_timeout: 1s\nrule_files:\n  - %q\nscrape_configs:\n  - job_name: commit-schedd\n    static_configs:\n      - targets: %s\n", rulesPath, targetList)
	configPath := filepath.Join(dir, "prometheus.yml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "prometheus.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	processCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, binary, "--config.file="+configPath,
		"--storage.tsdb.path="+filepath.Join(dir, "tsdb"), "--web.listen-address="+address)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		stop()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	client := &http.Client{Timeout: 3 * time.Second}
	t.Cleanup(func() { stop(); <-done; client.CloseIdleConnections() })
	return commitPrometheus{base: "http://" + address, client: client, done: done, log: logPath}
}

func waitCommitPrometheus(t *testing.T, parent context.Context, p commitPrometheus, budget time.Duration, state, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var rules struct {
			Groups []struct {
				Rules []struct {
					Name   string `json:"name"`
					State  string `json:"state"`
					Health string `json:"health"`
				} `json:"rules"`
			} `json:"groups"`
		}
		err := p.read(ctx, "/api/v1/rules?type=alert", &rules)
		states := make(map[string]string)
		for _, group := range rules.Groups {
			for _, rule := range group.Rules {
				if rule.Health == "ok" {
					states[rule.Name] = rule.State
				}
			}
		}
		if err == nil && states["FaasCommitSourceObservationUnknown"] == state &&
			states["FaasCommitRelaySourceFailure"] == state && states["FaasCommitBacklogOverdue"] == "inactive" {
			var sample struct {
				Result []json.RawMessage `json:"result"`
			}
			err = p.read(ctx, "/api/v1/query?query="+url.QueryEscape(query), &sample)
			if err == nil && len(sample.Result) == 1 {
				return
			}
		}
		select {
		case <-p.done:
			output, _ := os.ReadFile(p.log)
			t.Fatalf("disposable Prometheus exited: %s", output)
		case <-ctx.Done():
			t.Fatalf("Commit alert rules did not reach %s with the expected live metrics: states=%v API error=%v", state, states, err)
		case <-ticker.C:
		}
	}
}

func (p commitPrometheus) read(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Prometheus returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Status != "success" {
		return fmt.Errorf("unexpected Prometheus API status %q", envelope.Status)
	}
	return json.Unmarshal(envelope.Data, target)
}
