package main_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/oci"
)

// healthApp answers / with 404 and /healthz with 503 for the first
// unhealthy requests, then 200.
func healthApp(t *testing.T, docker fakeBootDocker, unhealthy int32) {
	t.Helper()
	var seen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/healthz":
			w.WriteHeader(http.StatusNotFound)
		case seen.Add(1) <= unhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(srv.Close)
	docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
}

func TestDoctorBootHealthPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unhealthy int32
		code      int
		status    string
		detail    string
	}{
		{name: "passes once warm", unhealthy: 3, code: 0, status: "ok", detail: "GET /healthz → 200"},
		{name: "never passes", unhealthy: 1 << 20, code: 1, status: "error", detail: "GET /healthz → 503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			healthApp(t, docker, tc.unhealthy)
			code, report, _ := runDoctorBoot(t, bootInspector(), "--healthcheck-path", "/healthz", "--json")
			c := bootCheck(t, report, "boot-http")
			if code != tc.code || c.Status != tc.status || c.Detail != tc.detail {
				t.Fatalf("exit %d, boot-http %+v", code, c)
			}
			if tc.status == "error" && (c.Code != "boot_health_failed" || !strings.Contains(c.Hint, "200-399")) {
				t.Fatalf("boot-http %+v", c)
			}
		})
	}
}

// Without a health path a 404 on / still counts as answering.
func TestDoctorBootWithoutHealthPathAcceptsAnyAnswer(t *testing.T) {
	docker := newFakeBootDocker(t)
	healthApp(t, docker, 0)
	code, report, _ := runDoctorBoot(t, bootInspector(), "--json")
	if c := bootCheck(t, report, "boot-http"); code != 0 || c.Status != "ok" || c.Detail != "GET / → 404" {
		t.Fatalf("exit %d, boot-http %+v", code, c)
	}
}

func healthcheckInspector() doctorInspectorFunc {
	return doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{Reference: "example.com/app", Digest: "sha256:" + strings.Repeat("a", 64), Config: oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/server"},
			Healthcheck: &oci.ImageHealthcheck{Test: []string{"CMD", "curl", "-f", "http://localhost:8080/healthz"}, Retries: 3}}}, nil
	})
}

func TestDoctorBootImageHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health string
		code   int
		status string
		check  func(main.DoctorCheck) bool
	}{
		{name: "healthy", health: `{"Status":"healthy","Log":[]}`, code: 0, status: "ok", check: func(c main.DoctorCheck) bool { return strings.Contains(c.Detail, "healthy after") }},
		{name: "unhealthy", health: `{"Status":"unhealthy","Log":[{"ExitCode":7,"Output":"curl: (7) Failed to connect"}]}`, code: 1, status: "error", check: func(c main.DoctorCheck) bool {
			return c.Code == "boot_healthcheck_unhealthy" && c.Output == "exit 7: curl: (7) Failed to connect"
		}},
		{name: "still starting", health: `{"Status":"starting","Log":[]}`, code: 0, status: "warn", check: func(c main.DoctorCheck) bool { return strings.Contains(c.Hint, `"starting"`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			healthApp(t, docker, 0)
			docker.write(t, "health", tc.health)
			code, report, _ := runDoctorBoot(t, healthcheckInspector(), "--json")
			c := bootCheck(t, report, "boot-healthcheck")
			if code != tc.code || c.Status != tc.status || !tc.check(c) {
				t.Fatalf("exit %d, boot-healthcheck %+v", code, c)
			}
			if run := docker.calls(t)[0]; !strings.Contains(run, "--health-interval 1s") {
				t.Errorf("docker run %q does not shorten the health interval", run)
			}
		})
	}
}

// An image without a HEALTHCHECK gets no health flags and no health check.
func TestDoctorBootNoImageHealthcheck(t *testing.T) {
	docker := newFakeBootDocker(t)
	healthApp(t, docker, 0)
	_, report, _ := runDoctorBoot(t, bootInspector(), "--json")
	for _, c := range report.Checks {
		if c.Name == "boot-healthcheck" {
			t.Fatalf("unexpected %+v", c)
		}
	}
	if run := docker.calls(t)[0]; strings.Contains(run, "--health-interval") {
		t.Errorf("docker run %q sets a health interval", run)
	}
}

func TestDoctorBootHealthPathValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--image", "example.com/app", "--healthcheck-path", "/healthz"},
		{"--image", "example.com/app", "--boot", "--healthcheck-path", "healthz"},
		{"--image", "example.com/app", "--boot", "--healthcheck-path", "/health z"},
	} {
		var stderr bytes.Buffer
		old := *main.OsStderr
		*main.OsStderr = &stderr
		code := main.CmdDoctorWithImageInspector(args, bootInspector())
		*main.OsStderr = old
		if code != 1 || !strings.Contains(stderr.String(), "--healthcheck-path requires --boot") {
			t.Errorf("%q: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}
