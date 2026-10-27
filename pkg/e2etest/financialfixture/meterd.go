package financialfixture

import (
	"bytes"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// BuildMeterd always builds this checkout. It does not reuse merely executable
// binaries from another acceptance run. Call once on the parent test.
func BuildMeterd(t *testing.T) string {
	t.Helper()
	dir, err := e2etest.CallerDir()
	if err != nil {
		t.Fatalf("locate checkout: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "meterd")
	cmd := exec.Command("go", "build", "-p=1", "-ldflags=-s -w", "-o", binary, "./cmd/meterd")
	cmd.Dir = filepath.Clean(filepath.Join(dir, "../../.."))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build matching meterd: %v\n%s", err, output)
	}
	return binary
}

type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

// Meterd owns exactly one real child and reaps it before restarting. The env is
// explicit: caller credentials, config files and mail settings are not inherited.
type Meterd struct {
	t      *testing.T
	binary string
	env    []string
	cmd    *exec.Cmd
	done   chan error
	logs   lockedBuffer
}

func StartMeterd(t *testing.T, binary string, pool *pgxpool.Pool, extra []string) *Meterd {
	t.Helper()
	dsn := e2etest.DatabaseURL(pool)
	// Refuse a shared database even if a caller supplies the wrong pool.
	var expectedDB, expectedSchema string
	if err := pool.QueryRow(t.Context(), "SELECT current_database(),current_schema()").Scan(&expectedDB, &expectedSchema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(expectedDB, "faas_test_clone_") && !strings.HasPrefix(expectedSchema, "faas_test_") {
		t.Fatal("meter acceptance requires a pgtest clone or schema")
	}
	probe, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var gotDB, gotSchema string
	err = probe.QueryRow(t.Context(), "SELECT current_database(),current_schema()").Scan(&gotDB, &gotSchema)
	probe.Close()
	if err != nil || gotDB != expectedDB || gotSchema != expectedSchema {
		t.Fatalf("daemon database isolation mismatch: %v", err)
	}
	m := &Meterd{t: t, binary: binary, env: append([]string{
		"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + dsn,
		"FAAS_DEV=1", "FAAS_MAIL_TRANSPORT=log", "FAAS_SKIP_SOCKET_GROUP=1",
		"FAAS_OTEL_SPANS_WRITER_ENABLED=false", "FAAS_REQUEST_TELEMETRY_ENABLED=false",
		"FAAS_APP_ERRORS_ENABLED=false", "FAAS_METERD_CONFIG=" + filepath.Join(t.TempDir(), "absent.toml"),
		"FAAS_SCHEDD_ADDR=" + filepath.Join(t.TempDir(), "absent.sock"),
		"FAAS_EGRESS_SOCKET=" + filepath.Join(t.TempDir(), "absent-egress.sock"),
	}, extra...)}
	m.start()
	t.Cleanup(func() { m.Stop(); t.Logf("matching meterd output:\n%s", m.Logs()) })
	return m
}
func (m *Meterd) start() {
	m.t.Helper()
	m.cmd = exec.Command(m.binary)
	m.cmd.Env = m.env
	m.cmd.Stdout, m.cmd.Stderr = &m.logs, &m.logs
	if err := m.cmd.Start(); err != nil {
		m.t.Fatal(err)
	}
	m.done = make(chan error, 1)
	go func(cmd *exec.Cmd, done chan error) { done <- cmd.Wait(); close(done) }(m.cmd, m.done)
}
func (m *Meterd) RequireAlive() {
	m.t.Helper()
	select {
	case err := <-m.done:
		m.t.Fatalf("meterd exited: %v\n%s", err, m.Logs())
	default:
	}
}
func (m *Meterd) Kill() {
	m.t.Helper()
	if m.cmd == nil {
		return
	}
	if err := m.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		m.t.Fatal(err)
	}
	<-m.done
	m.cmd = nil
}
func (m *Meterd) Restart() { m.Kill(); m.start() }
func (m *Meterd) Stop() {
	if m.cmd == nil {
		return
	}
	_ = m.cmd.Process.Signal(os.Interrupt)
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
	m.cmd = nil
}
func (m *Meterd) Logs() string { return m.logs.String() }

// Wait bounds asynchronous acceptance without accepting a daemon exit as success.
func Wait(t *testing.T, m *Meterd, what string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if m != nil {
			m.RequireAlive()
		}
		if ready() {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-deadline.C:
			t.Fatalf("timed out: %s", what)
		case <-tick.C:
		}
	}
}
