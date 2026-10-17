//go:build promotionacceptance && metal

package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/migrationprobe"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type promotionMigrationNative struct {
	p               *environmentPromotionNative
	coordinated     bool
	target, source  *postgresfixture.Fixture
	config          migrationprobe.Config
	recoveryRequest *api.ProjectPromotionMigrationRecoveryRequest
	recoveryReceipt *api.ProjectPromotionMigrationRecovery
}

func promotionMigrationNativeFiles(binary []byte, version string) fstest.MapFS {
	generation := version
	if version == "baseline" {
		generation = "stable" // old code reads the legacy column after expansion too
	}
	files := projectHTTPNativeFiles(binary, generation)
	// Procfile commands use /bin/sh -lc. The scratch image supplies an adapter
	// for this exact command instead of depending on a shell in the host base.
	files["Dockerfile.frontend"].Data = append(files["Dockerfile.frontend"].Data, []byte("COPY projectworker /bin/sh\n")...)
	if version == "tested" {
		files["Procfile"] = &fstest.MapFile{Data: []byte("release: /projectworker migration-promotion\n")}
	}
	return files
}

func newPromotionMigrationNative(t *testing.T, mode string) *promotionMigrationNative {
	return newPromotionMigrationNativeConfigured(t, mode, false)
}
func newPromotionMigrationNativeConfigured(t *testing.T, mode string, coordinated bool) *promotionMigrationNative {
	t.Helper()
	if os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP") == "" {
		t.Fatal("promotion migrations require the guarded isolated SQL proxy address")
	}
	identity, err := age.GenerateX25519Identity()
	mustProjectWorkerNative(t, err)
	keys := t.TempDir()
	private, public := filepath.Join(keys, "host.age"), filepath.Join(keys, "host.age.pub")
	mustProjectWorkerNative(t, secretbox.WriteHostKeyAtPath(private, identity))
	mustProjectWorkerNative(t, secretbox.WriteRecipientFile(public, identity))
	m := &promotionMigrationNative{coordinated: coordinated}
	owner := "frontend"
	if coordinated {
		owner = "backend"
	}
	runID := uuid.NewString()
	values := map[string]string{"POSTGRES_MIGRATION_RUN_ID": runID, "POSTGRES_PROMOTION_QUALIFICATION": "1"}
	scoped := map[string]map[string]string{}
	if coordinated {
		scoped[owner] = map[string]string{"POSTGRES_MIGRATION_RUN_ID": runID, "POSTGRES_PROMOTION_QUALIFICATION": "1"}
	}
	var hmac []byte
	var proxy postgresfixture.Proxy
	bind := func(f *projectWorkerNativeFixture, scope string, sql *postgresfixture.Fixture, commandMode string, released bool) migrationprobe.Config {
		_, bindings := sql.CreateInScope(t, f.accountID, f.apps[owner].ID, scope)
		config := migrationprobe.Config{RunID: runID, Major: sql.Major}
		for access, binding := range bindings {
			uri, err := sql.Observer.URI(t.Context(), binding)
			if err != nil {
				t.Fatal("observe scoped SQL credential delivery")
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
		mustProjectWorkerNative(t, config.InitializePromotion(t.Context(), commandMode, released))
		if coordinated {
			scoped[owner]["POSTGRES_MIGRATION_MAJOR"] = strconv.Itoa(sql.Major)
		}
		values["POSTGRES_MIGRATION_MAJOR"] = strconv.Itoa(sql.Major)
		for key, value := range values {
			mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, f.apps[owner].ID, scope, key, value))
		}
		return config
	}
	options := promotionNativeOptions{files: promotionMigrationNativeFiles, values: values,
		environment: []string{"FAAS_PROMOTION_MIGRATIONS_ENABLED=1", "FAAS_RELEASE_PHASE_ENABLED=1", "FAAS_APP_TASK_DISPATCH=1", "FAAS_HOST_KEY_PATH=" + private, "FAAS_HOST_AGE_IDENTITY_PATH=" + private, "FAAS_HOST_AGE_RECIPIENT_PATH=" + public, "FAAS_FLEET_AGE_RECIPIENT_PATH=" + public}}
	if coordinated {
		options.files = promotionCoordinatedNativeFiles
		options.serviceGraph = true
		options.values = nil
		options.workloadValues = scoped
		options.environment = append(options.environment, "FAAS_PROMOTION_SERVICE_GRAPH_ENABLED=1")
	}
	options.beforeDeploy = func(f *projectWorkerNativeFixture) {
		if coordinated {
			configurePromotionGraphDestination(t, f)
		}
		proxy = postgresfixture.OpenProxyWithDeadline(t, f.h.Pool, os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP"), 5432, 12*time.Minute)
		var err error
		hmac, err = os.ReadFile(f.h.HostHMACKeyPath)
		if err != nil {
			t.Fatal("read isolated SQL signing key")
		}
		ports := []int{5432}
		destinations := []string{netip.PrefixFrom(netip.MustParseAddr(proxy.Host), 32).String()}
		_, err = f.owner.UpdateApp(t.Context(), f.apps[owner].Slug, api.UpdateAppRequest{EgressPorts: &ports, EgressAllowlist: &destinations})
		mustProjectWorkerNative(t, err)
		m.target = postgresfixture.New(t, f.h.Pool, identity, hmac, proxy.Host, proxy.Port)
		if m.target == nil {
			t.Fatal("destination SQL fixture unavailable")
		}
		m.config = bind(f, "production", m.target, mode, false)
		t.Cleanup(func() { f.cleanup(t) })
	}
	options.beforeStaging = func(f *projectWorkerNativeFixture) {
		if coordinated {
			configurePromotionGraphSource(t, f)
		}
		m.source = postgresfixture.New(t, f.h.Pool, identity, hmac, proxy.Host, proxy.Port)
		if m.source == nil || m.source.Major != m.target.Major {
			t.Fatal("source SQL fixture unavailable or version differs")
		}
		bind(f, "staging", m.source, "success", true)
		t.Cleanup(func() { f.cleanup(t) })
	}
	m.p = newEnvironmentPromotionNativeWithOptions(t, !coordinated, nil, options)
	t.Cleanup(func() {
		if m.p.cleanupDone {
			return
		}
		mustProjectWorkerNative(t, m.p.h.RestartSchedd())
		if m.p.apidStopped {
			mustProjectWorkerNative(t, m.p.h.RestartAPID())
			m.p.apidStopped = false
		}
	})
	m.observe(t, false)
	return m
}

// Observe only customer SQL and daemon-owned evidence. The result latch below
// controls guest behavior; no lease, task outcome or publication row is edited.
func (m *promotionMigrationNative) observe(t *testing.T, applied bool) probesql.ObservePromotionRow {
	t.Helper()
	id, _ := m.config.ID()
	q := probesql.New()
	source, err := q.ObservePromotion(t.Context(), m.source.Data, id)
	mustProjectWorkerNative(t, err)
	target, err := q.ObservePromotion(t.Context(), m.target.Data, id)
	mustProjectWorkerNative(t, err)
	columns, err := q.SchemaColumns(t.Context(), m.target.Data)
	mustProjectWorkerNative(t, err)
	want := int32(0)
	if applied {
		want = 1
	}
	if source.Executions != 1 || !source.Applied || target.Executions != want || target.Applied != applied || !columns.Legacy || columns.Current != applied {
		t.Fatal("migration changed the source database, replayed SQL or lost the destination schema")
	}
	if applied {
		row, err := q.ReadCurrent(t.Context(), m.target.Data, id)
		mustProjectWorkerNative(t, err)
		if row.Value != m.config.Marker() {
			t.Fatal("migration/rollback lost customer data")
		}
	}
	return target
}

func (m *promotionMigrationNative) status(t *testing.T, id string) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	s, err := m.p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), m.p.project.Slug, "production", id)
	mustProjectWorkerNative(t, err)
	if s.Health == nil || s.Health.Migration == nil || s.Health.Policy.Migration == nil || (s.Health.Policy.Worker != nil) != m.coordinated {
		t.Fatal("promotion lost its immutable migration policy")
	}
	return s
}

func TestProjectEnvironmentPromotionMigrationSuccessMetal(t *testing.T) {
	qualifyPromotionMigrationNative(t, "success")
}

func TestProjectEnvironmentPromotionMigrationRollbackMetal(t *testing.T) {
	qualifyPromotionMigrationNative(t, "rollback")
}

func TestProjectEnvironmentPromotionMigrationFailureMetal(t *testing.T) {
	qualifyPromotionMigrationNative(t, "failure")
}

func TestProjectEnvironmentPromotionMigrationRecoveryMetal(t *testing.T) {
	qualifyPromotionMigrationNative(t, "recovery")
}

func qualifyPromotionMigrationNative(t *testing.T, scenario string) {
	mode := "success"
	if scenario == "failure" {
		mode = "failure"
	} else if scenario == "recovery" {
		mode = "hold"
	}
	m := newPromotionMigrationNative(t, mode)
	p := m.p
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, true)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote || preview.Health == nil || preview.Health.Migration == nil {
		t.Fatalf("migration promotion refused: %v", preview.BlockingReasons)
	}
	request := api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}
	key := uuid.NewString()
	mustProjectWorkerNative(t, p.h.KillSchedd())
	transport := p.owner.HTTPClient().Transport
	drop := &contractLostResponseTransport{base: transport}
	p.owner.HTTPClient().Transport = drop
	_, err = p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	p.owner.HTTPClient().Transport = transport
	if err == nil || !drop.dropped {
		t.Fatal("did not lose a successfully committed promotion response")
	}
	accepted, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	id := accepted.PromotionID
	var queued api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, 20*time.Second, "durable queued migration task", func() bool {
		queued = m.status(t, id)
		return queued.Health.Migration.TaskID != ""
	})
	taskID := queued.Health.Migration.TaskID
	task, err := p.owner.GetAppTask(t.Context(), p.apps["frontend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	if task.Status != api.AppTaskStatusQueued || task.AttemptCount != 0 || task.StartedAt != nil {
		t.Fatal("migration executed while its scheduler was absent")
	}
	m.observe(t, false)
	p.assertGraph(t, p.baseline.ID)
	// One restart covers queued persistence and admission disablement, without
	// changing the real one-minute migration start deadline.
	mustProjectWorkerNative(t, p.h.SetAPIDEnv("FAAS_PROMOTION_MIGRATIONS_ENABLED", "0"))
	mustProjectWorkerNative(t, p.h.RestartAPID())
	replayed, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	restarted := m.status(t, id)
	if replayed.PromotionID != id || restarted.Health.Migration.TaskID != taskID || !restarted.Health.Deadline.Equal(queued.Health.Deadline) {
		t.Fatal("queued owner restart or response replay changed task identity/deadline")
	}
	blocked, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, true)
	mustProjectWorkerNative(t, err)
	if blocked.CanPromote || !strings.Contains(strings.Join(blocked.BlockingReasons, " "), "enabled migration admission") {
		t.Fatal("migration admission remained open after disablement")
	}
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	var running api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, time.Minute, "real destination expansion", func() bool {
		id, _ := m.config.ID()
		row, err := probesql.New().ObservePromotion(t.Context(), m.target.Data, id)
		mustProjectWorkerNative(t, err)
		running = m.status(t, accepted.PromotionID)
		return row.Applied
	})
	m.observe(t, true)
	if scenario != "failure" {
		task, err = p.owner.GetAppTask(t.Context(), p.apps["frontend"].Slug, taskID)
		mustProjectWorkerNative(t, err)
		if task.Status != api.AppTaskStatusRunning || task.ExitCode != nil {
			t.Fatal("result barrier did not retain a real running command")
		}
		p.assertGraph(t, p.baseline.ID)
		mustProjectWorkerNative(t, p.h.RestartAPID())
		s := m.status(t, id)
		if s.Health.Migration.TaskID != taskID || !s.Health.Deadline.Equal(running.Health.Deadline) {
			t.Fatal("running owner restart changed migration identity/deadline")
		}
	}
	evidence := map[string]any{"schema_version": 1, "scenario": "migration_" + scenario, "promotion_id": id, "task_id": taskID,
		"run_id": m.config.RunID, "baseline_graph": p.baseline.ID, "lost_response": true, "queued_restart": true,
		"admission_disabled": true, "destination_only": true, "artifact_reused": true,
		"migration_admitted_at": queued.Health.StartedAt, "migration_deadline": queued.Health.Deadline}
	if scenario != "failure" {
		evidence["running_restart"] = true
	}
	if scenario == "recovery" {
		evidence["recovery"] = m.recover(t, id, taskID, request)
	} else if scenario != "failure" {
		rowID, _ := m.config.ID()
		mustProjectWorkerNative(t, probesql.New().ReleasePromotion(t.Context(), m.target.Data, rowID))
	}
	var final api.ProjectEnvironmentPromotionStatusResponse
	if scenario == "failure" {
		waitProjectWorkerNative(t, time.Minute, "nonzero migration refusal", func() bool {
			final = m.status(t, id)
			return final.Status == "failed"
		})
		p.assertGraph(t, p.baseline.ID)
		p.assertConfig(t, p.targetConfig.ConfigHash)
		p.assertHTTP(t, "stable", "baseline-config")
		evidence["graph_config_preserved"] = true
	} else {
		final = m.finish(t, id, scenario == "rollback", evidence)
	}
	task, err = p.owner.GetAppTask(t.Context(), p.apps["frontend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	wantExit := 0
	if scenario == "failure" {
		wantExit = 23
	}
	if task.AttemptCount != 1 || task.RetryMax != 0 || task.StartedAt == nil || task.FinishedAt == nil || final.Health.Migration.TaskID != taskID ||
		(scenario == "recovery" && task.ExitCode != nil) || (scenario != "recovery" && (task.ExitCode == nil || *task.ExitCode != wantExit)) {
		t.Fatal("migration replayed, fabricated an exit, or lost its terminal receipt")
	}
	if scenario != "failure" && scenario != "recovery" && !strings.Contains(task.StdoutTail, "native promotion expansion generation=tested mode=tested-config") {
		t.Fatal("migration did not execute the tested artifact and paired configuration")
	}
	before := task
	mustProjectWorkerNative(t, p.h.RestartAPID())
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	replayed, err = p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	after, err := p.owner.GetAppTask(t.Context(), p.apps["frontend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	if replayed.PromotionID != id || !reflect.DeepEqual(before, after) {
		t.Fatal("terminal restart/replay changed original task history")
	}
	if m.recoveryRequest != nil {
		receipt, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, *m.recoveryRequest)
		mustProjectWorkerNative(t, err)
		if !reflect.DeepEqual(receipt, *m.recoveryReceipt) {
			t.Fatal("cutover/restart changed the original recovery receipt")
		}
		changed := *m.recoveryRequest
		changed.ExpectedGeneration++
		if _, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, changed); err == nil {
			t.Fatal("recovery request identity permitted changed replay intent")
		}
	}
	destination := m.observe(t, true)
	p.assertArtifacts(t, final)
	rowID, _ := m.config.ID()
	source, err := probesql.New().ObservePromotion(t.Context(), m.source.Data, rowID)
	mustProjectWorkerNative(t, err)
	var tasks int
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM app_tasks WHERE deployment_id=$1 AND kind='release'", final.Workloads[0].TargetDeploymentID).Scan(&tasks))
	if tasks != 1 {
		t.Fatal("promotion created a second release task")
	}
	evidence["task_count"], evidence["attempts"], evidence["executions"], evidence["source_executions"] = tasks, task.AttemptCount, destination.Executions, source.Executions
	evidence["exit_code"], evidence["migration_status"], evidence["terminal_restart"] = task.ExitCode, final.Health.Migration.Status, true
	evidence["task_started_at"], evidence["task_finished_at"] = task.StartedAt, task.FinishedAt
	selected, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	evidence["selected_graph"], evidence["phase"], evidence["schema_preserved"] = selected.ID, final.Health.Phase, true
	p.cleanup(t)
	evidence["cleanup"] = true
	raw, err := json.Marshal(evidence)
	mustProjectWorkerNative(t, err)
	t.Logf("environment promotion native: %s", raw)
}

func (m *promotionMigrationNative) finish(t *testing.T, id string, rollback bool, evidence map[string]any) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	p := m.p
	var preflight, cutover, final api.ProjectEnvironmentPromotionStatusResponse
	preflightRounds := 0
	waitProjectWorkerNative(t, time.Minute, "migration-complete preflight", func() bool {
		preflight = m.status(t, id)
		return preflight.Health.Phase == "preflight"
	})
	if preflight.Health.Deadline.Sub(preflight.Health.StartedAt) != api.ProjectReleaseVerificationTimeout {
		t.Fatal("migration did not receive a fresh full health window")
	}
	rowID, _ := m.config.ID()
	beforeHealth, err := probesql.New().ReadLegacy(t.Context(), m.target.Data, rowID)
	mustProjectWorkerNative(t, err)
	p.assertGraph(t, p.baseline.ID)
	mustProjectWorkerNative(t, p.h.RestartAPID())
	restarted := m.status(t, id)
	if !restarted.Health.Deadline.Equal(preflight.Health.Deadline) {
		t.Fatal("completed-task restart reset health deadline")
	}
	waitProjectWorkerNative(t, 5*time.Minute, "migration promotion cutover", func() bool {
		cutover = m.status(t, id)
		h := cutover.Health
		if h.Phase == "preflight" || h.Phase == "cutover" {
			preflightRounds = max(preflightRounds, h.HealthyRounds)
			p.assertGraph(t, p.baseline.ID)
			return false
		}
		if h.Phase != "verifying" || cutover.ReleaseGraph == nil {
			t.Fatalf("migration ended before cutover: %s", h.Phase)
		}
		return true
	})
	if preflightRounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("cutover lacked both real baseline/candidate health rounds")
	}
	afterHealth, err := probesql.New().ReadLegacy(t.Context(), m.target.Data, rowID)
	mustProjectWorkerNative(t, err)
	if afterHealth != beforeHealth {
		t.Fatal("critical promotion health checks modified customer data")
	}
	evidence["read_only_health"] = true
	p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
	p.assertConfig(t, p.sourceConfig.ConfigHash)
	p.assertHTTP(t, "tested", "tested-config")
	if rollback {
		_, status := projectWorkerNativeRequest(t, p.h.EdgeURL()+"/control/regress", p.apps["frontend"].Slug+".apps.test.example", http.MethodPost)
		if status != http.StatusNoContent {
			t.Fatal("candidate refused critical health regression")
		}
	}
	var windows []time.Time
	regressions := 0
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "migration post-cutover outcome", func() bool {
		final = m.status(t, id)
		h := final.Health
		regressions = max(regressions, h.RegressedRounds)
		if h.CheckedAt != nil && h.Verdict == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if rollback {
			return h.Phase == "failed" && final.RollbackStatus == "rolled_back"
		}
		return h.Phase == "succeeded"
	})
	if rollback {
		if regressions < api.ProjectReleaseVerificationRounds || final.ReleaseGraph == nil || final.ReleaseGraph.RestoredTargetReleaseSetID == "" {
			t.Fatal("rollback lacked real regression or restored graph evidence")
		}
		p.assertGraph(t, final.ReleaseGraph.RestoredTargetReleaseSetID)
		p.assertConfig(t, p.targetConfig.ConfigHash)
		p.assertHTTP(t, "stable", "baseline-config")
	} else {
		if len(windows) < 2 || windows[1].Sub(windows[0]) < time.Minute {
			t.Fatal("post-cutover health windows were not fresh and minute-spaced")
		}
		p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
	}
	evidence["preflight_rounds"] = preflightRounds
	if rollback {
		evidence["regressed_rounds"] = regressions
	} else {
		evidence["healthy_windows"] = windows
	}
	evidence["health_started_at"], evidence["health_deadline"], evidence["cutover_graph"] = preflight.Health.StartedAt, preflight.Health.Deadline, cutover.ReleaseGraph.TargetReleaseSetID
	evidence["completed_restart"], evidence["graph_config_restored"] = true, rollback
	return final
}

func (m *promotionMigrationNative) recover(t *testing.T, id, taskID string, request api.PromoteProjectEnvironmentRequest) map[string]any {
	t.Helper()
	p := m.p
	task, err := p.store.AppTaskByID(t.Context(), p.accountID, p.apps["frontend"].ID, taskID)
	mustProjectWorkerNative(t, err)
	if task.LeaseExpiresAt == nil || task.LeaseToken == nil {
		t.Fatal("running migration has no owned execution lease")
	}
	leaseUntil := *task.LeaseExpiresAt
	mustProjectWorkerNative(t, p.h.KillSchedd())
	waitProjectWorkerNative(t, time.Minute, "real migration owner lease expiry", func() bool { return !time.Now().Before(leaseUntil) })
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	var failed api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, 3*time.Minute, "uncertain lost migration result", func() bool {
		failed = m.status(t, id)
		return failed.Status == "failed" && failed.Health.Migration.Status == "uncertain"
	})
	uncertainAt := time.Now().UTC()
	m.observe(t, true) // committed expansion, but no terminal command result
	p.assertGraph(t, p.baseline.ID)
	mustProjectWorkerNative(t, p.h.SetAPIDEnv("FAAS_PROMOTION_MIGRATIONS_ENABLED", "1"))
	mustProjectWorkerNative(t, p.h.RestartAPID())
	_, err = p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, uuid.NewString())
	var rejected *api.APIError
	if !errors.As(err, &rejected) || rejected.Problem.Status != http.StatusConflict {
		t.Fatal("uncertain SQL allowed a competing migration")
	}
	mustProjectWorkerNative(t, p.h.SetAPIDEnv("FAAS_PROMOTION_MIGRATIONS_ENABLED", "0"))
	mustProjectWorkerNative(t, p.h.KillSchedd())
	var node, runtime, lease string
	var destroyed *time.Time
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT runtime_node_id::text,runtime_instance_id::text,runtime_lease_token::text,destroyed_at FROM project_environment_promotion_migrations WHERE promotion_id=$1", id).Scan(&node, &runtime, &lease, &destroyed))
	if destroyed != nil || lease != *task.LeaseToken {
		t.Fatal("uncertain migration lost its original runtime identity")
	}
	inspection := fmt.Sprintf("%s/%s/%s/executions=1/expanded=true/data=%s", id, taskID, runtime, m.config.Marker())
	evidenceHash := fmt.Sprintf("%x", sha256.Sum256([]byte(inspection)))
	req := api.ProjectPromotionMigrationRecoveryRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedTaskID: taskID,
		ExpectedGeneration: failed.Health.Generation, Action: "resume", DatabaseStateChecked: true, SchemaCompatible: true, EvidenceHash: evidenceHash}
	receipt, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if receipt.Status != "quiescing" {
		t.Fatal("recovery bypassed scheduler-owned runtime teardown")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	replay, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if !reflect.DeepEqual(receipt, replay) || m.status(t, id).Health.Phase != failed.Health.Phase {
		t.Fatal("recovery advanced without a scheduler teardown acknowledgement")
	}
	other := req
	other.RequestID = strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, other); err == nil {
		t.Fatal("two recovery decisions were admitted")
	}
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	waitProjectWorkerNative(t, time.Minute, "acknowledged teardown and resumed preflight", func() bool {
		s := m.status(t, id)
		return s.Health.Recovery != nil && s.Health.Recovery.Status == "resumed"
	})
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT destroyed_at FROM project_environment_promotion_migrations WHERE promotion_id=$1 AND runtime_node_id=$2 AND runtime_instance_id=$3 AND runtime_lease_token=$4", id, node, runtime, lease).Scan(&destroyed))
	if destroyed == nil || destroyed.Before(receipt.RequestedAt) {
		t.Fatal("recovery lacks current, attributed runtime teardown")
	}
	q := probesql.New()
	waitProjectWorkerNative(t, 30*time.Second, "original migration SQL session gone", func() bool {
		alive, err := q.PromotionSessionAlive(t.Context(), m.target.Data, m.config.PromotionSessionName())
		mustProjectWorkerNative(t, err)
		return !alive
	})
	replay, err = p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if replay.Status != "resumed" || replay.Request != req || replay.ResolvedAt == nil {
		t.Fatal("recovery lost the exact operator decision")
	}
	m.recoveryRequest, m.recoveryReceipt = &req, &replay
	return map[string]any{"request_id": req.RequestID, "evidence_hash": evidenceHash, "node_id": node, "runtime_id": runtime, "lease_token": lease,
		"previous_lease_until": leaseUntil, "uncertain_at": uncertainAt, "requested_at": receipt.RequestedAt, "deadline": receipt.Deadline,
		"destroyed_at": destroyed, "resolved_at": replay.ResolvedAt, "status": replay.Status, "quiescing_restart": true, "competing_rejected": true,
		"session_gone": true, "original_receipt_preserved": true}
}
