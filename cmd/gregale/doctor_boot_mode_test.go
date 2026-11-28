package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestDoctorBootWorkerAndJob(t *testing.T) {
	for _, tc := range []struct {
		name, mode, state   string
		check, status, code string
		detail, fix         string
	}{
		{name: "worker keeps running", mode: "worker", state: "true 0 false", check: "boot-stay-up", status: "ok", detail: "as a worker must"},
		{name: "worker exits 0", mode: "worker", state: "false 0 false", check: "boot-start", status: "warn", code: "boot_worker_exited", fix: "--execution-mode job"},
		{name: "worker crashes", mode: "worker", state: "false 1 false", check: "boot-start", status: "error", code: "boot_exited", fix: "A worker must keep running"},
		{name: "worker out of memory", mode: "worker", state: "false 137 true", check: "boot-start", status: "error", code: "boot_oom_killed"},
		{name: "job completes", mode: "job", state: "false 0 false", check: "boot-start", status: "ok", detail: "completed with exit code 0"},
		{name: "job still running", mode: "job", state: "true 0 false", check: "boot-stay-up", status: "ok", detail: "a job may run longer"},
		{name: "job fails", mode: "job", state: "false 2 false", check: "boot-start", status: "error", code: "boot_exited", fix: "A job must exit with code 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			docker.write(t, "state", tc.state)
			_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--execution-mode", tc.mode, "--json")
			c := bootCheck(t, report, tc.check)
			if c.Status != tc.status || c.Code != tc.code || !strings.Contains(c.Detail, tc.detail) || !strings.Contains(c.Fix, tc.fix) {
				t.Fatalf("%s = %+v", tc.check, c)
			}
			for _, call := range docker.calls(t) {
				if strings.HasPrefix(call, "run ") && strings.Contains(call, "--publish") {
					t.Errorf("a %s published a port: %q", tc.mode, call)
				}
				if strings.HasPrefix(call, "port ") {
					t.Errorf("a %s was probed for HTTP: %q", tc.mode, call)
				}
			}
			for _, c := range report.Checks {
				if c.Name == "boot-listener" || c.Name == "boot-http" {
					t.Errorf("a %s got an HTTP check: %+v", tc.mode, c)
				}
			}
			if !docker.removed(t) {
				t.Error("container not removed")
			}
		})
	}
}

func TestDoctorBootExecutionModeFlagValidation(t *testing.T) {
	newFakeBootDocker(t)
	for _, args := range [][]string{
		{"--execution-mode", "daemon"},
		{"--execution-mode", "worker", "--healthcheck-path", "/healthz"},
	} {
		if code, _, _ := runDoctorBoot(t, bootInspector("3000/tcp"), args...); code == 0 {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestDoctorBootExistingWorkerApp(t *testing.T) {
	docker := newFakeBootDocker(t)
	main.SetDoctorBootAppClass(t.Cleanup, "worker")
	_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--app", "queue", "--json")
	if c := bootCheck(t, report, "boot-stay-up"); c.Status != "ok" || !strings.Contains(c.Detail, "as a worker must") {
		t.Fatalf("boot-stay-up = %+v", c)
	}
	for _, call := range docker.calls(t) {
		if strings.HasPrefix(call, "run ") && strings.Contains(call, "--publish") {
			t.Fatalf("a worker app was booted as an HTTP app: %q", call)
		}
	}
}

func TestDoctorBootExecutionModeTrimmed(t *testing.T) {
	newFakeBootDocker(t)
	if code, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--execution-mode", " worker ", "--json"); code != 0 {
		t.Fatalf("exit %d: %+v", code, report.Checks)
	}
}
