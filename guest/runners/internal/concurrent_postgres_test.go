package internal

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

// This opt-in test uses the production HTTP runner, generated Node adapter,
// the PostgreSQL starter's shipped database adapter and SDK runtime, the pg
// driver and independent SQL witnesses. It requires a
// disposable TLS/SCRAM PostgreSQL cluster with CREATEDB/CREATEROLE privileges;
// only the test process gets that authority. No Firecracker evidence is implied.
func TestConcurrentFunctionPostgres(t *testing.T) {
	if os.Getenv("FAAS_TEST_FUNCTION_POSTGRES") != "1" {
		t.Skip("set FAAS_TEST_FUNCTION_POSTGRES=1 with a disposable TLS PostgreSQL fixture")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("NODE_EXTRA_CA_CERTS") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("explicit SQL test requires DATABASE_URL, NODE_EXTRA_CA_CERTS and enabled PostgreSQL tests")
	}
	admin := pgtest.OpenDatabase(t)
	var ssl string
	if err := admin.QueryRow(t.Context(), "SHOW ssl").Scan(&ssl); err != nil || ssl != "on" {
		t.Fatal("SQL fixture requires PostgreSQL TLS")
	}
	role := "fn_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	prepareFunctionSQLRole(t, admin, role, password)
	srv, cfg := startPostgresFunction(t, functionSQLURL(admin, role, password))
	client := &http.Client{Timeout: 12 * time.Second}
	warm := functionGet(t, client, srv.URL+"/probe")
	pid, backend := warm["pid"], warm["backend"]
	if pid == nil || backend == nil {
		t.Fatal("missing process/SQL witness")
	}
	var tls bool
	if err := admin.QueryRow(t.Context(), "SELECT ssl FROM pg_stat_ssl WHERE pid=$1", int(backend.(float64))).Scan(&tls); err != nil || !tls {
		t.Fatal("function SQL session is not using native TLS")
	}

	t.Run("bounded admission and cancellation", func(t *testing.T) {
		lock, err := admin.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lock.Rollback(context.Background()) }()
		if _, err := lock.Exec(t.Context(), "LOCK TABLE notes IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		abandoned := asyncFunctionGet(client, ctx, srv.URL+"/write?id=cancelled")
		healthy := asyncFunctionGet(client, t.Context(), srv.URL+"/write?id=healthy")
		waitSQLCount(t, admin, role, "wait_event_type='Lock'", 2)
		busy := functionGet(t, client, srv.URL+"/write?id=must-not-run")
		if busy["error"] != "database_busy" || busy["pid"] != pid {
			t.Fatalf("admission did not reject excess SQL work in the same worker: %v", busy)
		}
		cancel()
		if result := <-abandoned; result.err == nil {
			t.Fatal("abandoned HTTP request completed successfully")
		}
		// PostgreSQL may notice a disconnected socket only after its lock
		// wait ends. Witness local admission cleanup now, then independently
		// verify rollback after unlocking; a socket close is not a SQL cancel.
		deadline := time.Now().Add(3 * time.Second)
		for functionGet(t, client, srv.URL+"/diagnostics")["activeRequests"] != float64(1) {
			if time.Now().After(deadline) {
				t.Fatal("abandoned query retained local admission")
			}
			time.Sleep(10 * time.Millisecond)
		}
		probe := functionGet(t, client, srv.URL+"/probe")
		if probe["pid"] != pid || probe["error"] != nil {
			t.Fatalf("neighbour lost shared worker: %v", probe)
		}
		if err := lock.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if result := <-healthy; result.err != nil || result.body["id"] != "healthy" || result.body["pid"] != pid {
			t.Fatalf("healthy concurrent write failed: body=%v err=%v", result.body, result.err)
		}
		var count int
		if err := admin.QueryRow(t.Context(), "SELECT count(*) FROM notes WHERE id IN ('cancelled','must-not-run')").Scan(&count); err != nil || count != 0 {
			t.Fatal("abandoned or rejected SQL committed after admission/cancellation")
		}
	})

	t.Run("trusted hooks and pool replacement", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/before-checkpoint", strings.NewReader(`{"lifecycle_hook":"before_checkpoint"}`))
		req.Header.Set("X-Faas-Before-Checkpoint", "1")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("public forged hook status=%d", resp.StatusCode)
		}
		checkpoint := localFunctionHook(t, cfg, "/internal/before-checkpoint", "X-Faas-Before-Checkpoint")
		if checkpoint["state"] != "checkpointed" {
			t.Fatalf("checkpoint failed: %v", checkpoint)
		}
		waitSQLCount(t, admin, role, "true", 0)
		if got := functionGet(t, client, srv.URL+"/probe"); got["error"] != "database_unavailable" {
			t.Fatalf("SQL admitted while checkpointed: %v", got)
		}
		restored := localFunctionHook(t, cfg, "/internal/after-restore", "X-Faas-After-Restore")
		if restored["state"] != "ready" || restored["generation"] != float64(1) {
			t.Fatalf("restore failed: %v", restored)
		}
		probe := functionGet(t, client, srv.URL+"/probe")
		if probe["pid"] != pid || probe["backend"] == backend || probe["error"] != nil {
			t.Fatalf("restore did not replace SQL session within shared worker: %v", probe)
		}
	})

	t.Run("lost SQL session and thaw callback", func(t *testing.T) {
		if _, err := admin.Exec(t.Context(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1", role); err != nil {
			t.Fatal(err)
		}
		waitSQLCount(t, admin, role, "true", 0)
		thawed := localFunctionHook(t, cfg, "/internal/after-restore", "X-Faas-After-Restore")
		if thawed["state"] != "ready" || thawed["generation"] != float64(2) {
			t.Fatalf("thaw callback failed: %v", thawed)
		}
		if got := functionGet(t, client, srv.URL+"/probe"); got["pid"] != pid || got["error"] != nil {
			t.Fatalf("SQL recovery failed: %v", got)
		}
	})

	t.Run("worker replacement and context isolation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pending := asyncFunctionGet(client, ctx, srv.URL+"/sleep")
		waitSQLCount(t, admin, role, "wait_event='PgSleep'", 1)
		crash, err := client.Get(srv.URL + "/crash")
		if err != nil {
			t.Fatal(err)
		}
		_ = crash.Body.Close()
		if crash.StatusCode != http.StatusInternalServerError {
			t.Fatalf("crash status=%d", crash.StatusCode)
		}
		if result := <-pending; result.body["error"] == nil && result.err == nil {
			t.Fatal("worker crash did not fail in-flight request")
		}
		replacement := functionGet(t, client, srv.URL+"/probe")["pid"]
		results := make(chan functionResult, 4)
		for i := 0; i < 4; i++ {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/identity", nil)
			invocation := "inv-" + strconv.Itoa(i)
			req.Header.Set("X-Faas-Invocation-Id", invocation)
			req.Header.Set("X-Token", invocation)
			go func() {
				resp, err := client.Do(req)
				if err != nil {
					results <- functionResult{err: err}
					return
				}
				defer func() { _ = resp.Body.Close() }()
				results <- decodeFunctionResult(resp.Body)
			}()
		}
		seen := map[string]bool{}
		for i := 0; i < 4; i++ {
			got := <-results
			invocation, ok := got.body["invocation"].(string)
			if got.err != nil || !ok || seen[invocation] || got.body["pid"] != replacement || replacement == pid || got.body["token"] != invocation {
				t.Fatalf("new worker/context mismatch: %v err=%v", got.body, got.err)
			}
			seen[invocation] = true
		}
		var count int
		if err := admin.QueryRow(t.Context(), "SELECT count(*) FROM notes").Scan(&count); err != nil || count != 1 {
			t.Fatal("writes were lost or replayed during worker recovery")
		}
	})
}

func prepareFunctionSQLRole(t *testing.T, admin *pgxpool.Pool, role, password string) {
	t.Helper()
	quoted := pgx.Identifier{role}.Sanitize()
	for _, sql := range []string{
		"CREATE TABLE notes (id text PRIMARY KEY)",
		"CREATE ROLE " + quoted + " LOGIN NOINHERIT PASSWORD '" + password + "'",
	} {
		if _, err := admin.Exec(t.Context(), sql); err != nil {
			t.Fatal("prepare restricted function SQL fixture")
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1", role)
		for _, sql := range []string{"DROP OWNED BY " + quoted, "DROP ROLE " + quoted} {
			if _, err := admin.Exec(ctx, sql); err != nil {
				t.Error("clean restricted function SQL fixture")
			}
		}
	})
	for _, sql := range []string{
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{admin.Config().ConnConfig.Database}.Sanitize() + " TO " + quoted,
		"GRANT USAGE ON SCHEMA public TO " + quoted,
		"GRANT SELECT, INSERT ON notes TO " + quoted,
	} {
		if _, err := admin.Exec(t.Context(), sql); err != nil {
			t.Fatal("grant restricted function SQL fixture")
		}
	}
	var scram bool
	if err := admin.QueryRow(t.Context(), "SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname=$1", role).Scan(&scram); err != nil || !scram {
		t.Fatal("function fixture requires SCRAM password authentication")
	}
}

func functionSQLURL(admin *pgxpool.Pool, role, password string) string {
	config := admin.Config().ConnConfig
	return (&url.URL{Scheme: "postgres", Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + config.Database,
		User: url.UserPassword(role, password), RawQuery: "sslmode=require"}).String()
}

func startPostgresFunction(t *testing.T, dsn string) (*httptest.Server, ServeConfig) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("SQL integration requires Node")
	}
	dir := t.TempDir()
	for _, file := range []string{"package.json", "package-lock.json", "database.js",
		"runtime/binding-client-runtime.js", "runtime/postgres-pool-runtime.js", "runtime/secret-reload-runtime.js"} {
		content, err := templates.FS.ReadFile("rest-api-postgres/" + file)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "app", file)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install := exec.CommandContext(t.Context(), "npm", "ci", "--ignore-scripts", "--omit=dev", "--no-audit", "--no-fund")
	install.Dir = filepath.Join(dir, "app")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install locked pg fixture: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "handler.js"), []byte(postgresFunctionHandler), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rootfs.NormalizeFunctionHandlerFrom(dir, "/app/handler.js", "/app/node22.js"); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "app", "node22.js")
	// Only the restricted customer role's credentials enter
	// the interpreter, and the generated adapter advertises four shared slots.
	env := make([]string, 0, 6)
	for _, key := range []string{"PATH", "NODE_EXTRA_CA_CERTS", "TZ", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "DATABASE_URL="+dsn, "FAAS_FUNCTION_CONCURRENCY=4")
	cfg := ServeConfig{Runtime: "node22", Spec: workerpool.Spec{Executable: node, Args: []string{adapter}, Env: env, HandlerPath: adapter}}
	// Network requests model the gateway-facing socket. Guest-init hooks below
	// call the same Serve entry directly with its real loopback peer identity.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.RemoteAddr = "10.0.0.1:1234"; Serve(w, r, cfg) }))
	t.Cleanup(func() { srv.Close(); workerpool.Recycle() })
	return srv, cfg
}

type functionResult struct {
	body map[string]any
	err  error
}

func decodeFunctionResult(bodyReader io.Reader) functionResult {
	var body map[string]any
	err := json.NewDecoder(bodyReader).Decode(&body)
	return functionResult{body, err}
}

func asyncFunctionGet(client *http.Client, ctx context.Context, address string) <-chan functionResult {
	done := make(chan functionResult, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		resp, err := client.Do(req)
		if err != nil {
			done <- functionResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		done <- decodeFunctionResult(resp.Body)
	}()
	return done
}

func functionGet(t *testing.T, client *http.Client, address string) map[string]any {
	t.Helper()
	resp, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got := decodeFunctionResult(resp.Body)
	if got.err != nil {
		t.Fatal(got.err)
	}
	return got.body
}

func localFunctionHook(t *testing.T, cfg ServeConfig, path, marker string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set(marker, "1")
	w := httptest.NewRecorder()
	Serve(w, req, cfg)
	if w.Code != http.StatusOK {
		t.Fatalf("local lifecycle hook status=%d", w.Code)
	}
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()
	got := decodeFunctionResult(resp.Body)
	if got.err != nil {
		t.Fatal(got.err)
	}
	return got.body
}

func waitSQLCount(t *testing.T, admin *pgxpool.Pool, role, predicate string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := admin.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND "+predicate, role).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("SQL witness did not reach %d sessions (%s)", want, predicate)
}

const postgresFunctionHandler = `import pg from "pg";
import {createDatabase,poolConfig} from "./database.js";
const db=createDatabase(config=>new pg.Pool({...poolConfig(config.connectionString),max:2}),{
  maxInFlight:2,timeoutMS:8000,restoreTimeoutMS:1500,readinessQuery:"SELECT id FROM notes LIMIT 1",log:()=>{}
});
const started=db.start();
started.catch(()=>{});
export async function handler(event,ctx) {
  const base={pid:process.pid,invocation:ctx.invocation_id,token:event.headers["x-token"]};
  try {
    await started;
    if(event.path==="/internal/before-checkpoint"||event.path==="/internal/after-restore") {
      const before=event.path.endsWith("before-checkpoint");
      if(ctx.lifecycle_hook!==(before?"before_checkpoint":"after_restore")) return {statusCode:403,body:{error:"forbidden"}};
      if(before) await db.checkpoint({signal:ctx.signal}); else await db.restore({signal:ctx.signal});
      return {body:{...base,...db.diagnostics}};
    }
    if(event.path==="/crash") process.exit(17);
    if(event.path==="/diagnostics") return {body:{...base,...db.diagnostics}};
    if(event.path==="/identity") { await new Promise(resolve=>setTimeout(resolve,50)); return {body:base}; }
    if(event.path==="/write") {
      const id=new URLSearchParams(event.query).get("id");
      const result=await db.query("INSERT INTO notes(id) VALUES($1) RETURNING id",[id],{write:true,signal:ctx.signal});
      return {body:{...base,...result.rows[0]}};
    }
    if(event.path==="/sleep") await db.query("SELECT pg_sleep(20)",[],{signal:ctx.signal});
    const result=await db.query("SELECT pg_backend_pid() AS backend",[],{signal:ctx.signal});
    return {body:{...base,...result.rows[0]}};
  } catch(error) { return {statusCode:503,body:{...base,error:error.code||"database_unavailable"}}; }
}`
