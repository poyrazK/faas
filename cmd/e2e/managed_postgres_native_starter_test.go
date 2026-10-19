//go:build managed_postgres_native && metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresstarter"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresNativeStarterPhases(t *testing.T, starter postgresstarter.Starter, expected map[int64]string, h *e2etest.Harness, key, slug, appID, account string, pool *pgxpool.Pool, store *state.PgStore, fixture *postgresfixture.Fixture, proxy postgresfixture.Proxy, bindings map[mp.CredentialAccess]mp.Binding, phase func(string, func(*testing.T)), rotate func(*testing.T, bool), workload *func(*testing.T, mp.Binding, string), deploymentID, snapshotID *string, recordRuntime func(*testing.T, string, string, string)) {
	t.Helper()
	sourceArchive := filepath.Join(t.TempDir(), "starter-source.tar.gz")
	noteBody := starter.Runtime + "-" + randHexSuffix()
	starterPhase := func(name string, fn func(*testing.T)) { phase(starter.PhasePrefix+"_"+name, fn) }
	adopted := func(t *testing.T) {
		waitPostgresStarterAdoption(t, h, key, slug, *deploymentID, bindings[mp.CredentialReadWrite].CredentialGeneration)
	}
	request := func(t *testing.T, method, path string, value any, want int) []byte {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var body io.Reader
		if value != nil {
			body = bytes.NewReader(encoded)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, gatewayAppURL(h, slug)+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = slug + ".apps.test.example"
		req.Header.Set("Content-Type", "application/json")
		if path == "internal/after-restore" {
			req.Header.Set("X-Faas-After-Restore", "1")
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
		}
		response, err := h.HTTPClient().Do(req)
		if err != nil {
			t.Fatal("starter HTTP request failed")
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("starter %s %s: status=%d want=%d", method, path, response.StatusCode, want)
		}
		return data
	}
	read := func(t *testing.T, method string) {
		t.Helper()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, slug)+"notes", slug+".apps.test.example", 45*time.Second)
		var result struct {
			OK    bool `json:"ok"`
			Notes []struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
			} `json:"notes"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &result) != nil || !result.OK || len(result.Notes) != len(expected) {
			t.Fatal("starter recovery lost or duplicated its note")
		}
		seen := make(map[int64]bool)
		for _, note := range result.Notes {
			want, known := expected[note.ID]
			if !known || seen[note.ID] || want != note.Body {
				t.Fatal("starter recovery changed its notes dataset")
			}
			seen[note.ID] = true
		}
		adopted(t)
		if method != "" {
			if wakeID == "" {
				t.Fatal("starter response omitted wake receipt")
			}
			if _, err := e2etest.WaitForWakeMethod(t.Context(), t, pool, wakeID, method, 10*time.Second); err != nil {
				t.Fatalf("starter did not use %s: %v", method, err)
			}
		}
	}
	park := func(t *testing.T) {
		t.Helper()
		// Require a capture of the current process rather than reuse a prior init
		// snapshot. This modifies only the owned acceptance application's cache.
		if current, err := store.LatestSnapshotForTier(t.Context(), *deploymentID, state.SnapshotTierInit); err == nil {
			*snapshotID = current.ID
			if err := store.MarkSnapshotStale(t.Context(), current.ID); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			t.Fatal("could not inspect the starter snapshot before fresh capture")
		}
		// Failed-hook fallback and rolling rotation can leave only stale
		// captures. Parking the replacement creates the next fresh init.
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park starter: status=%d", status)
		}
		*snapshotID = waitAfterRestoreSnapshot(t, store, *deploymentID, *snapshotID)
	}
	// Client sessions the guest holds in the customer database; the fixture's own
	// Data pool is excluded.
	guestSessions := func(ctx context.Context) (int, error) {
		var sessions int
		err := fixture.Data.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND usename <> current_user AND backend_type = 'client backend'`).Scan(&sessions)
		return sessions, err
	}
	starterPhase("release_and_initial_restore", func(t *testing.T) {
		ca, err := os.ReadFile(os.Getenv("GREGALE_POSTGRES_NATIVE_CA_PATH"))
		if err != nil {
			t.Fatal("read disposable SQL trust certificate")
		}
		source, err := postgresstarter.Source(starter.Template, ca)
		if err != nil {
			t.Fatal(err)
		}
		source["faas-build-token"] = noteBody
		if status := statusOnly(t, h, key, http.MethodPut, "/v1/apps/"+slug+"/env/"+starter.TrustEnv, api.PutAppEnvRequest{Value: "/app/" + postgresstarter.CAPath}); status != http.StatusOK {
			t.Fatalf("set fixture trust: status=%d", status)
		}
		archive := buildTarGz(t, source)
		if err := os.WriteFile(sourceArchive, archive, 0600); err != nil {
			t.Fatal("retain exact SQL starter source")
		}
		body, status := postMultipartDeployment(t, h, key, slug, archive, true, "")
		if status != http.StatusAccepted {
			t.Fatalf("deploy embedded starter: status=%d", status)
		}
		*deploymentID, _ = parseQueuedDeployment(t, body)
		ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, *deploymentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("starter deployment: %v", err)
		}
		recordRuntime(t, *deploymentID, starter.Runtime, sourceArchive)
		release, err := store.ReleaseAppTaskByDeployment(ctx, *deploymentID)
		if err != nil || release.Kind != state.AppTaskKindRelease || release.Status != state.AppTaskSucceeded {
			t.Fatal("starter migration release did not succeed")
		}
		app, err := store.AppByID(ctx, appID)
		if err != nil || app.Manifest.AfterRestore == nil || app.Manifest.AfterRestore.Path != "/internal/after-restore" || app.Manifest.AfterRestore.TimeoutMS != 2000 ||
			app.Manifest.BeforeCheckpoint == nil || app.Manifest.BeforeCheckpoint.Path != "/internal/before-checkpoint" || app.Manifest.BeforeCheckpoint.TimeoutMS != 2000 {
			t.Fatal("generated starter manifest was not applied")
		}
		if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		*snapshotID = waitAfterRestoreSnapshot(t, store, *deploymentID, "")
		// Health reads the migrated relation but never creates it. A new note is
		// issued once; requests containing mutations are never replayed by the test.
		body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, slug)+"healthz", slug+".apps.test.example", 45*time.Second)
		if status != http.StatusOK || !bytes.Contains(body, []byte(`"db":"ok"`)) || wakeID == "" {
			t.Fatal("starter initial readiness failed")
		}
		if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, "restore", 10*time.Second); err != nil {
			t.Fatal(err)
		}
		write := func(t *testing.T, body string) {
			t.Helper()
			var created struct {
				OK   bool `json:"ok"`
				Note struct {
					ID   int64  `json:"id"`
					Body string `json:"body"`
				} `json:"note"`
			}
			if json.Unmarshal(request(t, http.MethodPost, "notes", map[string]string{"body": body}, http.StatusCreated), &created) != nil || !created.OK || created.Note.ID <= 0 || created.Note.Body != body {
				t.Fatal("starter write failed")
			}
			if _, exists := expected[created.Note.ID]; exists {
				t.Fatal("starter write reused a prior note ID")
			}
			expected[created.Note.ID] = body
		}
		write(t, noteBody)
		read(t, "")
		generation := bindings[mp.CredentialReadWrite].CredentialGeneration
		*workload = func(t *testing.T, binding mp.Binding, method string) {
			read(t, method)
			if binding.CredentialGeneration > generation {
				write(t, "rotated-"+randHexSuffix())
				generation = binding.CredentialGeneration
				read(t, "")
			}
		}
	})
	starterPhase("manual_task_without_migration_secret", func(t *testing.T) {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/tasks", api.CreateAppTaskRequest{Command: starter.TaskCommand, TimeoutSeconds: 30})
		var response api.AppTaskResponse
		if status != http.StatusAccepted || json.Unmarshal(body, &response) != nil || response.ID == "" {
			t.Fatalf("starter manual task: status=%d", status)
		}
		postgresNativeWait(t, "starter task credential isolation", func(ctx context.Context) bool {
			task, err := store.AppTaskByID(ctx, account, appID, response.ID)
			if err != nil {
				return false
			}
			if task.Status.Terminal() && task.Status != state.AppTaskSucceeded {
				t.Fatal("starter task received wrong credential scope")
			}
			return task.Kind == state.AppTaskKindManual && task.Status == state.AppTaskSucceeded
		})
	})
	starterPhase("pooled_disconnect_recovery", func(t *testing.T) {
		read(t, "") // the driver pool keeps an idle session in the process about to be captured
		if sessions, err := guestSessions(t.Context()); err != nil || sessions == 0 {
			t.Fatal("starter kept no SQL session before capture; the drain check would prove nothing")
		}
		park(t)
		// before_checkpoint drained the pool, so the capture strands no server
		// session for keepalive to reap.
		postgresNativeWait(t, "starter capture to hold no SQL session", func(ctx context.Context) bool {
			sessions, err := guestSessions(ctx)
			return err == nil && sessions == 0
		})
		proxy.Disconnect()
		read(t, "restore")
		request(t, http.MethodPost, "internal/after-restore", nil, http.StatusForbidden)
	})
	starterPhase("failed_hook_cold_boot", func(t *testing.T) {
		park(t)
		failedSnapshot := *snapshotID
		proxy.Disconnect()
		proxy.DelayNextConnection(2500 * time.Millisecond) // fail hook, then permit fresh cold readiness
		defer proxy.DelayNextConnection(0)
		ackBefore := strings.Count(h.VmmdLogs(), "application after_restore failed (ack=13)")
		read(t, "cold_boot")
		if strings.Count(h.VmmdLogs(), "application after_restore failed (ack=13)") <= ackBefore {
			t.Fatal("starter cold boot did not follow failed application hook")
		}
		var stale bool
		if err := pool.QueryRow(t.Context(), `SELECT stale FROM snapshots WHERE id=$1`, failedSnapshot).Scan(&stale); err != nil || !stale {
			t.Fatal("failed starter capture remained eligible")
		}
		instances, err := store.ListInstancesForApp(t.Context(), appID)
		if err != nil {
			t.Fatal(err)
		}
		residents := 0
		for _, instance := range instances {
			if state.State(instance.State).CountsForRAM() {
				residents++
			}
		}
		if residents != 1 {
			t.Fatalf("failed restore left %d resident guests; want one cold-booted guest", residents)
		}
	})
	starterPhase("running_rotation_and_retirement", func(t *testing.T) { rotate(t, false) })
	starterPhase("parked_rotation_scheduler_recovery", func(t *testing.T) { read(t, ""); park(t); rotate(t, true) })
	// Read directly as an independent authority to catch duplicate replayed writes
	// or serving a healthy response from a different dataset.
	var rows int
	if err := fixture.Data.QueryRow(t.Context(), `SELECT count(*) FROM notes`).Scan(&rows); err != nil || rows != len(expected) {
		t.Fatal("independent SQL note preservation failed")
	}
	for id, body := range expected {
		var observed string
		if err := fixture.Data.QueryRow(t.Context(), `SELECT body FROM notes WHERE id=$1`, id).Scan(&observed); err != nil || observed != body {
			t.Fatal("independent SQL note contents changed")
		}
	}
}
