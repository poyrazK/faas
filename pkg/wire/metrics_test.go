// Tests for the OpsMetrics helper and the /metrics handler. We exercise:
//   - counter incremented per Observe call, labelled by op + code
//   - histogram observes per Observe call, labelled by op
//   - code label is "ok" on success and "err" on non-nil error
//   - the HTTP handler emits both series in the Prometheus text format

package wire_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_ObserveCounter(t *testing.T) {
	m := wire.NewOpsMetrics("vmmd")
	m.Observe("CreateFromSnapshot", 12*time.Millisecond, nil)
	m.Observe("CreateFromSnapshot", 10*time.Millisecond, nil)
	m.Observe("Stats", 200*time.Microsecond, errors.New("boom"))

	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	body := string(buf)

	for _, want := range []string{
		`vmmd_ops_total{code="ok",op="CreateFromSnapshot"} 2`,
		`vmmd_ops_total{code="err",op="Stats"} 1`,
		`vmmd_op_duration_seconds_count{op="CreateFromSnapshot"} 2`,
		`vmmd_op_duration_seconds_count{op="Stats"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

func TestOpsMetrics_IndependentRegistries(t *testing.T) {
	// Two daemons must not collide if they construct in the same process —
	// that's the point of per-daemon Registry over the global default.
	a := wire.NewOpsMetrics("vmmd")
	b := wire.NewOpsMetrics("builderd")
	a.Observe("ColdBoot", time.Millisecond, nil)
	b.Observe("Build", 50*time.Millisecond, nil)

	// vmmd's endpoint must NOT mention builderd series, and vice versa.
	bodyA := render(t, a)
	bodyB := render(t, b)

	if !strings.Contains(bodyA, `vmmd_ops_total{code="ok",op="ColdBoot"} 1`) {
		t.Errorf("vmmd endpoint missing vmmd series:\n%s", bodyA)
	}
	if strings.Contains(bodyA, "builderd_") {
		t.Errorf("vmmd endpoint leaked builderd:\n%s", bodyA)
	}
	if !strings.Contains(bodyB, `builderd_ops_total{code="ok",op="Build"} 1`) {
		t.Errorf("builderd endpoint missing builderd series:\n%s", bodyB)
	}
	if strings.Contains(bodyB, "vmmd_") {
		t.Errorf("builderd endpoint leaked vmmd:\n%s", bodyB)
	}
}

func render(t *testing.T, m *wire.OpsMetrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	body, err := readAll(t, srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return body
}

func TestOpsMetrics_ObserveBuild(t *testing.T) {
	m := wire.NewOpsMetrics("builderd")
	m.ObserveBuildCount("ok")
	m.ObserveBuildCount("ok")
	m.ObserveBuildCount("cache_hit")
	m.ObserveBuildCount("user_error")
	m.ObserveBuildDuration(42 * time.Second)
	m.ObserveBuildQueueWait(3 * time.Second)

	body := render(t, m)
	for _, want := range []string{
		`builderd_ops_total{code="ok",op="build"} 2`,
		`builderd_ops_total{code="cache_hit",op="build"} 1`,
		`builderd_ops_total{code="user_error",op="build"} 1`,
		`builderd_build_duration_seconds_count 1`,
		`builderd_build_queue_wait_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

func TestOpsMetrics_ObserveBuildNilSafe(t *testing.T) {
	// builderd unit tests construct the orchestrator without metrics; the
	// observers must be no-ops on a nil receiver rather than panicking.
	var m *wire.OpsMetrics
	m.ObserveBuildCount("ok")
	m.ObserveBuildDuration(time.Second)
	m.ObserveBuildQueueWait(time.Second)
}

func TestRenderSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{time.Millisecond, "0.001"},
		{500 * time.Microsecond, "0.0005"},
		{2 * time.Second, "2"},
	} {
		if got := wire.RenderSeconds(tc.in); got != tc.want {
			t.Errorf("RenderSeconds(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpsMetrics_RegistryAccess(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	if m.Registry() == nil {
		t.Fatal("Registry() returned nil")
	}
	// Observe something so the CounterVec has a series to gather.
	m.Observe("whoami", time.Millisecond, nil)
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(mfs) == 0 {
		t.Error("expected at least one metric family after construction")
	}
}

func TestOpsMetrics_HandlerStandalone(t *testing.T) {
	// Handler() must be usable without an httptest server wrapper — that's
	// the form daemons actually mount onto their main mux.
	m := wire.NewOpsMetrics("meterd")
	m.Observe("tick", time.Millisecond, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `meterd_ops_total{code="ok",op="tick"} 1`) {
		t.Errorf("body missing tick series:\n%s", rec.Body.String())
	}
}

func readAll(t *testing.T, url string) (string, error) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return string(buf), nil
		}
	}
}
