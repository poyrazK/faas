package fcvm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

// This is a portable SQL/adapter/Manager test, with the VM boundary replaced
// by the existing fake VMM. It is not native capture or restore evidence.
func TestFunctionExecutionPostgres(t *testing.T) {
	if os.Getenv("FAAS_TEST_FUNCTION_POSTGRES") != "1" {
		t.Skip("requires the disposable TLS/SCRAM function SQL fixture")
	}
	if os.Getenv("NODE_EXTRA_CA_CERTS") == "" || os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("explicit SQL gate requires TLS trust and an enabled disposable database")
	}
	admin := pgtest.OpenDatabase(t)
	var tlsEnabled bool
	if err := admin.QueryRow(t.Context(), "SELECT current_setting('ssl')='on'").Scan(&tlsEnabled); err != nil || !tlsEnabled {
		t.Fatal("SQL gate requires TLS")
	}
	role := "bg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE TABLE notes (id text PRIMARY KEY)"); err != nil {
		t.Fatal("prepare SQL witness")
	}
	if _, err := admin.Exec(t.Context(), "CREATE ROLE "+quoted+" LOGIN NOINHERIT PASSWORD '"+password+"'"); err != nil {
		t.Fatal("prepare restricted role")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1", role)
		for _, statement := range []string{"DROP OWNED BY " + quoted, "DROP ROLE " + quoted} {
			if _, err := admin.Exec(ctx, statement); err != nil {
				t.Error("clean restricted SQL role")
			}
		}
	})
	for _, statement := range []string{"GRANT CONNECT ON DATABASE " + pgx.Identifier{admin.Config().ConnConfig.Database}.Sanitize() + " TO " + quoted, "GRANT USAGE ON SCHEMA public TO " + quoted, "GRANT INSERT ON notes TO " + quoted} {
		if _, err := admin.Exec(t.Context(), statement); err != nil {
			t.Fatal("grant restricted SQL fixture access")
		}
	}
	var scram bool
	if err := admin.QueryRow(t.Context(), "SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname=$1", role).Scan(&scram); err != nil || !scram {
		t.Fatal("SQL gate requires SCRAM")
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.Mkdir(app, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := templates.FS.ReadFile("rest-api-postgres/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install := exec.CommandContext(t.Context(), "npm", "ci", "--ignore-scripts", "--omit=dev", "--no-audit", "--no-fund")
	install.Dir = app
	install.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	if cache := os.Getenv("npm_config_cache"); cache != "" {
		install.Env = append(install.Env, "npm_config_cache="+cache)
	}
	if err := install.Run(); err != nil {
		t.Fatal("install locked pg fixture")
	}
	source := `import pg from 'pg';
const db=new pg.Pool({connectionString:process.env.DATABASE_URL,max:1});
export async function handler(event,ctx){
  ctx.waitUntil(db.query('INSERT INTO notes(id) VALUES($1)',[ctx.invocation_id]).then(()=>db.end()));
  return {statusCode:202,body:'accepted'};
}`
	if err := os.WriteFile(filepath.Join(app, "handler.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rootfs.NormalizeFunctionHandlerFrom(dir, "/app/handler.js", "/app/node22.js"); err != nil {
		t.Fatal(err)
	}
	c := admin.Config().ConnConfig
	dsn := (&url.URL{Scheme: "postgres", Host: net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), Path: "/" + c.Database, User: url.UserPassword(role, password), RawQuery: "sslmode=verify-full"}).String()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(app, "node22.js"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NODE_EXTRA_CA_CERTS=" + os.Getenv("NODE_EXTRA_CA_CERTS"), "DATABASE_URL=" + dsn, "FAAS_PERSISTENT_WORKER=1", "FAAS_FUNCTION_CONCURRENCY=4"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	reader := bufio.NewReader(stdout)
	next := func() map[string]any {
		t.Helper()
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal("adapter did not complete SQL protocol")
		}
		var frame map[string]any
		if json.Unmarshal(line, &frame) != nil {
			t.Fatal("invalid adapter frame")
		}
		return frame
	}
	if next()["__faas_ready"] != true {
		t.Fatal("adapter not ready")
	}
	lock, err := admin.Begin(t.Context())
	if err != nil {
		t.Fatal("open independent SQL lock")
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), "LOCK TABLE notes IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal("hold independent SQL lock")
	}
	m, lease, a := functionExecutionManager(t)
	if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1)); got.Status != functionlifetime.OK {
		t.Fatal("host did not acknowledge execution ownership")
	}
	env := map[string]any{"__faas_id": "1", "method": "POST", "path": "/", "headers": map[string]string{"X-Faas-Invocation-Id": "write-once"}, "body_b64": "", "background": map[string]int{"timeout_ms": api.PlanFree.TailTimeoutSeconds() * 1000, "max_tasks": api.PlanFree.TailCapMax()}}
	if err := json.NewEncoder(stdin).Encode(env); err != nil {
		t.Fatal(err)
	}
	for frame := next(); ; frame = next() {
		if frame["status"] == float64(202) {
			break
		}
		if frame["frame"] != "background" {
			t.Fatal("HTTP response did not finish before SQL work")
		}
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		var blocked int
		var tls bool
		err := admin.QueryRow(t.Context(), "SELECT count(*),coalesce(bool_and(s.ssl),false) FROM pg_stat_activity a JOIN pg_stat_ssl s USING(pid) WHERE a.usename=$1 AND a.wait_event_type='Lock'", role).Scan(&blocked, &tls)
		if err != nil {
			t.Fatal("query independent SQL witness")
		}
		if blocked == 1 && tls {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background SQL did not reach its TLS lock wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.Park(t.Context(), "f", SnapshotSpec{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("park did not preserve an active post-response SQL write")
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err == nil {
		t.Fatal("froze a VM with background SQL executing")
	}
	if n, _ := a.Inflight("f"); n != 1 {
		t.Fatal("HTTP completion dropped SQL execution ownership")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal("release independent SQL lock")
	}
	settled := false
	for frame := next(); ; frame = next() {
		if frame["frame"] == "background" {
			event, _ := frame["background"].(map[string]any)
			if event["event"] == "settled" && event["outcome"] == "completed" {
				settled = true
			}
		}
		if frame["frame"] == "complete" {
			break
		}
	}
	if !settled {
		t.Fatal("SQL promise did not complete successfully")
	}
	for range 2 {
		m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1))
	}
	if n, _ := a.Inflight("f"); n != 0 {
		t.Fatal("SQL completion did not release ownership exactly once")
	}
	var rows int
	if err := admin.QueryRow(t.Context(), "SELECT count(*) FROM notes WHERE id='write-once'").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("background SQL mutation was lost or replayed")
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal("completed execution still blocks idle admission")
	}
}
