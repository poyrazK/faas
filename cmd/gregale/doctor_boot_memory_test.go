package main_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func signedInAs(plan api.Plan) func(context.Context) (api.Limits, bool) {
	return func(context.Context) (api.Limits, bool) { return api.LimitsFor(plan) }
}

func signedOut(context.Context) (api.Limits, bool) { return api.Limits{}, false }

func TestDoctorBootMemoryLimit(t *testing.T) {
	appRAM := func(_ context.Context, slug string) (int, string, error) {
		if slug == "api" {
			return 1024, "api.gregale.app", nil
		}
		return 0, "", errors.New("app not found")
	}
	free, _ := api.LimitsFor(api.PlanFree)
	for _, tc := range []struct {
		name   string
		args   []string
		plan   func(context.Context) (api.Limits, bool)
		mb     int
		source string
	}{
		{name: "explicit --ram", args: []string{"--ram", "300", "--app", "api"}, plan: signedInAs(api.PlanFree), mb: 300, source: "--ram"},
		{name: "the app's RAM", args: []string{"--app", "api"}, plan: signedInAs(api.PlanFree), mb: 1024, source: "app api's RAM"},
		{name: "the plan's default", plan: signedInAs(api.PlanFree), mb: api.StandaloneAppDefaultRAMMB(free), source: "the free plan's default for a new app"},
		{name: "signed out", plan: signedOut, mb: api.StandaloneAppDefaultMaxRAMMB, source: "the default for a new app"},
		{name: "unreadable app", args: []string{"--app", "gone"}, plan: signedOut, mb: api.StandaloneAppDefaultMaxRAMMB, source: "app gone could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			main.SetDoctorBootMemorySources(t.Cleanup, tc.plan, appRAM)
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer srv.Close()
			docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
			code, report, _ := runDoctorBoot(t, bootInspector(), append(tc.args, "--json")...)
			if code != 0 {
				t.Fatalf("exit %d: %+v", code, report.Checks)
			}
			limit := strconv.Itoa(tc.mb) + "m"
			if run := docker.calls(t)[0]; !strings.Contains(run, "--memory "+limit+" --memory-swap "+limit) {
				t.Errorf("docker run %q lacks a %s limit without swap", run, limit)
			}
			if c := bootCheck(t, report, "boot-start"); !strings.Contains(c.Detail, "within "+strconv.Itoa(tc.mb)+" MB") || !strings.Contains(c.Detail, tc.source) {
				t.Errorf("boot-start detail %q, want %d MB from %q", c.Detail, tc.mb, tc.source)
			}
		})
	}
}

func TestDoctorBootOutOfMemory(t *testing.T) {
	docker := newFakeBootDocker(t)
	main.SetDoctorBootMemorySources(t.Cleanup, signedOut, nil)
	docker.write(t, "state", "false 137 true")
	code, report, _ := runDoctorBoot(t, bootInspector(), "--ram", "256", "--json")
	c := bootCheck(t, report, "boot-start")
	if code != 1 || c.Code != "boot_oom_killed" || !strings.Contains(c.Hint, "ran out of its 256 MB of memory (--ram)") || !strings.Contains(c.Fix, "gregale app update APP --ram MB") {
		t.Fatalf("exit %d, boot-start %+v", code, c)
	}
}

func TestDoctorBootRAMValidation(t *testing.T) {
	for _, args := range [][]string{{"--image", "example.com/app", "--ram", "512"}, {"--image", "example.com/app", "--boot", "--ram", "0"}} {
		var stderr bytes.Buffer
		old := *main.OsStderr
		*main.OsStderr = &stderr
		code := main.CmdDoctorWithImageInspector(args, bootInspector())
		*main.OsStderr = old
		if code != 1 || !strings.Contains(stderr.String(), "--ram requires --boot") {
			t.Errorf("%q: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}
