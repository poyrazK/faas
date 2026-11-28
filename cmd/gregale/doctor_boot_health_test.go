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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// serveBoot points the fake Docker's published port at handler.
func serveBoot(t *testing.T, docker fakeBootDocker, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
}

// healthApp answers / with 404 and /healthz with 503 for the first
// unhealthy requests, then 200.
func healthApp(t *testing.T, docker fakeBootDocker, unhealthy int32) {
	t.Helper()
	var seen atomic.Int32
	serveBoot(t, docker, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/healthz":
			w.WriteHeader(http.StatusNotFound)
		case seen.Add(1) <= unhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
}

func TestDoctorBootHealthPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(t *testing.T, d fakeBootDocker)
		args    []string
		code    int
		status  string
		detail  string
		errCode string
	}{
		{name: "passes once warm", handler: func(t *testing.T, d fakeBootDocker) { healthApp(t, d, 3) }, status: "ok", detail: "GET /healthz → 200"},
		{name: "never passes", handler: func(t *testing.T, d fakeBootDocker) { healthApp(t, d, 1<<20) }, code: 1, status: "error", detail: "GET /healthz → 503", errCode: "boot_health_failed"},
		{name: "redirect in the container is followed", handler: func(t *testing.T, d fakeBootDocker) {
			serveBoot(t, d, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					http.Redirect(w, r, "/healthz/", http.StatusMovedPermanently)
				}
			})
		}, status: "ok", detail: "GET /healthz → 200"},
		{name: "redirect elsewhere fails as on Gregale", handler: func(t *testing.T, d fakeBootDocker) {
			serveBoot(t, d, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://app.example.com/healthz", http.StatusMovedPermanently)
			})
		}, code: 1, status: "error", detail: "GET /healthz → 301", errCode: "boot_health_failed"},
		{name: "host allow-list without --app is a warning", handler: func(t *testing.T, d fakeBootDocker) {
			serveBoot(t, d, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
		}, status: "warn", detail: "GET /healthz → 400"},
		{name: "--app sends the public host", handler: func(t *testing.T, d fakeBootDocker) {
			serveBoot(t, d, func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "api.gregale.app" || r.Header.Get("X-Forwarded-Proto") != "https" {
					w.WriteHeader(http.StatusBadRequest)
				}
			})
		}, args: []string{"--app", "api"}, status: "ok", detail: "GET /healthz → 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			main.SetDoctorBootMemorySources(t.Cleanup, signedOut, func(context.Context, string) (int, string, error) { return 512, "api.gregale.app", nil })
			tc.handler(t, docker)
			code, report, _ := runDoctorBoot(t, bootInspector(), append([]string{"--healthcheck-path", "/healthz", "--json"}, tc.args...)...)
			c := bootCheck(t, report, "boot-http")
			if code != tc.code || c.Status != tc.status || c.Detail != tc.detail || c.Code != tc.errCode {
				t.Fatalf("exit %d, boot-http %+v", code, c)
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

func healthcheckInspector(hc *oci.ImageHealthcheck) doctorInspectorFunc {
	return doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{Reference: "example.com/app", Digest: "sha256:" + strings.Repeat("a", 64), Config: oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/server"}, Healthcheck: hc}}, nil
	})
}

func TestDoctorBootImageHealthcheck(t *testing.T) {
	curl := []string{"CMD", "curl", "-f", "http://localhost:8080/healthz"}
	for _, tc := range []struct {
		name    string
		hc      *oci.ImageHealthcheck
		health  string
		code    int
		status  string
		retries string
		check   func(main.DoctorCheck) bool
	}{
		{name: "healthy", hc: &oci.ImageHealthcheck{Test: curl}, health: `{"Status":"healthy","Log":[]}`, status: "ok", retries: "90",
			check: func(c main.DoctorCheck) bool { return strings.Contains(c.Detail, "healthy after") }},
		{name: "still unhealthy at the deadline", hc: &oci.ImageHealthcheck{Test: curl, IntervalS: 2, Retries: 5}, health: `{"Status":"unhealthy","Log":[{"ExitCode":7,"Output":"curl: (7) Failed to connect"}]}`, code: 1, status: "error", retries: "10",
			check: func(c main.DoctorCheck) bool {
				return c.Code == "boot_healthcheck_unhealthy" && c.Output == "exit 7: curl: (7) Failed to connect"
			}},
		{name: "still starting", hc: &oci.ImageHealthcheck{Test: curl, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: 500_000_000}, Retries: 3}, health: `{"Status":"starting","Log":[{"ExitCode":1,"Output":"not yet"}]}`, status: "warn", retries: "2",
			check: func(c main.DoctorCheck) bool {
				return strings.Contains(c.Hint, `"starting"`) && c.Output == "exit 1: not yet"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			healthApp(t, docker, 0)
			docker.write(t, "health", tc.health)
			code, report, _ := runDoctorBoot(t, healthcheckInspector(tc.hc), "--json")
			c := bootCheck(t, report, "boot-healthcheck")
			if code != tc.code || c.Status != tc.status || !tc.check(c) {
				t.Fatalf("exit %d, boot-healthcheck %+v", code, c)
			}
			if run := docker.calls(t)[0]; !strings.Contains(run, "--health-interval 1s --health-retries "+tc.retries+" ") {
				t.Errorf("docker run %q does not keep the failure budget with %s retries at 1s", run, tc.retries)
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
	if run := docker.calls(t)[0]; strings.Contains(run, "--health-") {
		t.Errorf("docker run %q sets health flags", run)
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
