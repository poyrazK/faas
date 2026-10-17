// adr: 1573
package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/e2etest/functionsql"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

func TestFunctionResourcesPostgres(t *testing.T) {
	if os.Getenv("FAAS_TEST_FUNCTION_POSTGRES") != "1" {
		t.Skip("requires disposable TLS/SCRAM SQL")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("NODE_EXTRA_CA_CERTS") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("explicit SQL gate requires private SQL URL and public CA")
	}
	sql := functionsql.New(t)
	control := functionsql.NewControl(t, "127.0.0.1")
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.Mkdir(app, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range functionsql.Files(t) {
		path := filepath.Join(app, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install := exec.CommandContext(t.Context(), "npm", "ci", "--ignore-scripts", "--omit=dev", "--no-audit", "--no-fund")
	install.Dir = app
	// Preserve network policy and CA trust without giving the installer SQL
	// credentials. No dependency lifecycle scripts are enabled.
	install.Env = []string{}
	for _, key := range []string{"PATH", "HOME", "npm_config_cache", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			install.Env = append(install.Env, key+"="+value)
		}
	}
	if err := install.Run(); err != nil {
		t.Fatal("install locked function SQL driver")
	}
	adapter := filepath.Join(app, "node22.js")
	if err := rootfs.NormalizeFunctionHandlerFrom(dir, "/app/handler.js", "/app/node22.js"); err != nil {
		t.Fatal(err)
	}
	host := &executionWireHost{}
	cfg := ServeConfig{Runtime: "node22", Execution: NewExecutionRuntime(host.dial), Spec: workerpool.Spec{Executable: "node", Args: []string{adapter}, HandlerPath: adapter, Env: []string{
		"PATH=" + os.Getenv("PATH"), "FAAS_FUNCTION_CONCURRENCY=1", "DATABASE_URL=" + sql.ConnectionString("", 0), "NODE_EXTRA_CA_CERTS=" + os.Getenv("NODE_EXTRA_CA_CERTS"),
		"FUNCTION_SQL_CONTROL_URL=" + control.URL, "FUNCTION_SQL_CONTROL_TOKEN=" + control.Token,
	}}}
	srv, ln, err := ListenAndServeLoopback(H2CListenerConfig{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { Serve(w, r, cfg) })})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	client := &http.Client{Timeout: 3 * time.Second}
	t.Cleanup(func() { control.Release(); _ = srv.Close(); client.CloseIdleConnections(); workerpool.Recycle() })
	get := func(path string, want int) int {
		t.Helper()
		r, err := client.Get("http://" + ln.Addr().String() + path)
		if err != nil {
			t.Fatal("function SQL HTTP failed")
		}
		defer func() { _ = r.Body.Close() }()
		body, err := io.ReadAll(r.Body)
		var value struct {
			PID int `json:"pid"`
		}
		if err != nil || r.StatusCode != want || json.Unmarshal(body, &value) != nil || value.PID == 0 {
			t.Fatal("function SQL HTTP response invalid")
		}
		return value.PID
	}
	lock := sql.Lock(t.Context(), t)
	pids := map[int]bool{}
	for _, id := range []string{"one", "two", "three"} {
		pids[get("/tail/"+id, 202)] = true
	}
	if len(pids) != 3 {
		t.Fatal("SQL work did not occupy three interpreters")
	}
	awaitBackground(t, func() bool { _, blocked := sql.Connections(t.Context(), t); return blocked == 3 })
	pids[get("/state", 200)] = true // populate the fourth interpreter and both its pools
	if len(pids) != 4 {
		t.Fatal("SQL fixture did not load four interpreters")
	}
	awaitBackground(t, func() bool { all, _ := sql.Connections(t.Context(), t); return all == 8 && host.count() == 3 })
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal("release independent SQL lock")
	}
	awaitBackground(t, func() bool { return host.count() == 0 && len(sql.Rows(t.Context(), t)) == 3 })
	// Model repeated continuing-VM captures: the same four interpreters must
	// drain and rebuild twice. Native snapshot bytes are a separate gate.
	for cycle := range 2 {
		control.Arm()
		control.ArmRecovery()
		callback := make(chan int, 1)
		go func() {
			f := &backgroundFunction{cfg: cfg}
			callback <- resourceHook(f, functionlifetime.BeforeResourcePath, "before_checkpoint")
		}()
		awaitBackground(t, func() bool { all, _ := sql.Connections(t.Context(), t); return control.Arrivals() == 4 && all == 0 })
		checkSQLWorkerGenerations(t, control.CheckpointWorkers(), pids, cycle)
		select {
		case <-callback:
			t.Fatal("checkpoint acknowledged before control released")
		default:
		}
		if host.count() != 1 {
			t.Fatal("SQL checkpoint lost host execution ownership")
		}
		control.ReleaseCheckpoint()
		select {
		case status := <-callback:
			if status != 204 {
				t.Fatal("SQL checkpoint failed")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("SQL checkpoint did not finish")
		}
		go func() {
			callback <- resourceHook(&backgroundFunction{cfg: cfg}, functionlifetime.AfterResourcePath, "after_restore")
		}()
		awaitBackground(t, func() bool {
			all, _ := sql.Connections(t.Context(), t)
			return control.RecoveryArrivals() == 4 && all == 8
		})
		checkSQLWorkerGenerations(t, control.RecoveryWorkers(), pids, cycle+1)
		select {
		case <-callback:
			t.Fatal("recovery acknowledged before every worker completed")
		default:
		}
		control.Release()
		select {
		case status := <-callback:
			if status != 204 {
				t.Fatal("SQL recovery failed")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("SQL recovery did not finish")
		}
		calls := control.Calls()
		if len(calls) != 4 {
			t.Fatal("resource callback history missed an interpreter")
		}
		for _, call := range calls {
			if call.Checkpoints != uint64(cycle+1) || call.Recoveries != uint64(cycle+1) {
				t.Fatal("resource callbacks replayed across release or recovery")
			}
		}
		if len(sql.Rows(t.Context(), t)) != 3 {
			t.Fatal("SQL recovery lost or replayed a mutation")
		}
		if !pids[get("/state", 200)] {
			t.Fatal("continuing recovery replaced a loaded interpreter")
		}
	}
	// A real worker exit rolls back its blocked transaction; it is never replayed.
	crashLock := sql.Lock(t.Context(), t)
	crashed := get("/worker-crash/interrupted", 202)
	awaitBackground(t, func() bool { return host.count() == 0 })
	if err := crashLock.Rollback(t.Context()); err != nil {
		t.Fatal("release crash SQL lock")
	}
	if next := get("/state", 200); next == crashed {
		t.Fatal("dead SQL worker was reused")
	}
	if rows := sql.Rows(t.Context(), t); len(rows) != 3 || rows["interrupted"] != 0 {
		t.Fatal("worker replacement replayed or committed interrupted SQL")
	}
}

func checkSQLWorkerGenerations(t *testing.T, workers []functionsql.WorkerWitness, pids map[int]bool, generation int) {
	t.Helper()
	if len(workers) != len(pids) {
		t.Fatal("resource witness missed a loaded interpreter")
	}
	for _, worker := range workers {
		if !pids[worker.PID] || len(worker.Generations) != 2 || worker.Generations[0] != generation || worker.Generations[1] != generation {
			t.Fatal("resource witness sampled an unfinished or repeated pool recovery")
		}
	}
}
