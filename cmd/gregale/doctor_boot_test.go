package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/oci"
)

// fakeBootDocker is a Docker client stand-in driven by files in dir: state
// holds `inspect` output, addr the published address, procnet the
// container's /proc/net/tcp, image the `image inspect` JSON, saved the
// `image save` output, health the
// `.State.Health` JSON; runfail makes
// `run` fail. Every call is logged.
type fakeBootDocker struct{ dir string }

func newFakeBootDocker(t *testing.T) fakeBootDocker {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d=` + dir + `
echo "$@" >> "$d/log"
case "$1" in
run) if [ -f "$d/runfail" ]; then echo "pull access denied" >&2; exit 125; fi; echo cid ;;
inspect) case "$*" in *Health*) cat "$d/health" 2>/dev/null || echo null ;; *) cat "$d/state" ;; esac ;;
port) cat "$d/addr" 2>/dev/null ;;
logs) echo "Error: DATABASE_URL is not set" ;;
exec) cat "$d/procnet" 2>/dev/null || exit 126 ;;
image) if [ "$2" = save ]; then cp "$d/saved" "$4" || exit 1; else cat "$d/image" 2>/dev/null || { echo "No such image" >&2; exit 1; }; fi ;;
esac
`
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_DOCKER", bin)
	main.SetDoctorBootTimings(t.Cleanup, 300*time.Millisecond, 200*time.Millisecond, 10*time.Millisecond)
	main.SetDoctorBootMemorySources(t.Cleanup, signedOut, nil)
	f := fakeBootDocker{dir: dir}
	f.write(t, "state", "true 0 false")
	return f
}

func (f fakeBootDocker) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f fakeBootDocker) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f fakeBootDocker) removed(t *testing.T) bool {
	for _, call := range f.calls(t) {
		if strings.HasPrefix(call, "rm --force --volumes gregale-doctor-") {
			return true
		}
	}
	return false
}

func bootInspector(exposed ...string) doctorInspectorFunc {
	return doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		ports := map[string]struct{}{}
		for _, p := range exposed {
			ports[p] = struct{}{}
		}
		return oci.ImageInspection{Reference: "example.com/app", Digest: "sha256:" + strings.Repeat("a", 64), Config: oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/server"}, ExposedPorts: ports}}, nil
	})
}

func runDoctorBoot(t *testing.T, inspector doctorInspectorFunc, args ...string) (int, main.DoctorReport, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := *main.OsStdout, *main.OsStderr, *main.JsonOutput
	*main.OsStdout, *main.OsStderr, *main.JsonOutput = &out, &stderr, false
	defer func() { *main.OsStdout, *main.OsStderr, *main.JsonOutput = oldOut, oldErr, oldJSON }()
	code := main.CmdDoctorWithImageInspector(append([]string{"--image", "example.com/app", "--boot"}, args...), inspector)
	var report main.DoctorReport
	if slices.Contains(args, "--json") {
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("invalid JSON: %v: %s (stderr %s)", err, out.String(), stderr.String())
		}
	}
	return code, report, out.String()
}

func bootCheck(t *testing.T, report main.DoctorReport, name string) main.DoctorCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check in %+v", name, report.Checks)
	return main.DoctorCheck{}
}

func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestDoctorBootHealthyImage(t *testing.T) {
	docker := newFakeBootDocker(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://")+"\n")
	envFile := filepath.Join(t.TempDir(), "app.env")
	code, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--env-file", envFile, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %+v", code, report.Checks)
	}
	for name, detail := range map[string]string{"boot-start": "answered in", "boot-listener": "0.0.0.0:3000 ($PORT)", "boot-http": "GET / → 200", "boot-stay-up": "stayed up"} {
		if c := bootCheck(t, report, name); c.Status != "ok" || !strings.Contains(c.Detail, detail) {
			t.Errorf("%s = %+v, want ok with %q", name, c, detail)
		}
	}
	for _, c := range report.Checks {
		if c.Name == "startup-and-port" {
			t.Errorf("boot ran, but startup-and-port is still reported skipped")
		}
	}
	run := docker.calls(t)[0]
	for _, want := range []string{"run --detach --name gregale-doctor-", "--platform linux/amd64", "--env PORT=3000", "--publish 127.0.0.1::3000", "--env-file " + envFile, " example.com/app"} {
		if !strings.Contains(run, want) {
			t.Errorf("docker run %q lacks %q", run, want)
		}
	}
	if !docker.removed(t) {
		t.Error("container not removed")
	}
}

func TestDoctorBootHumanOutput(t *testing.T) {
	docker := newFakeBootDocker(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
	code, _, out := runDoctorBoot(t, bootInspector())
	if code != 0 || !strings.Contains(out, "boot-http — GET / → 404") || !strings.Contains(out, "listening on 0.0.0.0:8080 ($PORT)") || !strings.Contains(out, "boot with local Docker") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestDoctorBootFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     func(*testing.T, fakeBootDocker)
		check     string
		code      string
		hint, out string
	}{
		{name: "crash", setup: func(t *testing.T, d fakeBootDocker) { d.write(t, "state", "false 1 false") }, check: "boot-start", code: "boot_exited", hint: "exited with code 1", out: "DATABASE_URL is not set"},
		{name: "oom", setup: func(t *testing.T, d fakeBootDocker) { d.write(t, "state", "false 137 true") }, check: "boot-start", code: "boot_oom_killed", hint: "MB of memory"},
		{name: "loopback", setup: func(t *testing.T, d fakeBootDocker) {
			d.write(t, "addr", closedAddr(t))
			d.write(t, "procnet", "  sl  local_address rem_address   st\n   0: 0100007F:1F90 00000000:0000 0A 0\n")
		}, check: "boot-listener", code: "boot_not_listening", hint: "Listening on 127.0.0.1:8080, not 0.0.0.0:8080"},
		{name: "wrong port", setup: func(t *testing.T, d fakeBootDocker) {
			d.write(t, "addr", closedAddr(t))
			d.write(t, "procnet", "   0: 00000000:0BB8 00000000:0000 0A 0\n   1: 0100007F:1F91 0100007F:9C40 01 0\n")
		}, check: "boot-listener", code: "boot_not_listening", hint: "Listening on 0.0.0.0:3000, not 0.0.0.0:8080 ($PORT)"},
		{name: "no shell tools", setup: func(t *testing.T, d fakeBootDocker) { d.write(t, "addr", closedAddr(t)) }, check: "boot-listener", code: "boot_not_listening", hint: "Nothing answered HTTP on 0.0.0.0:8080"},
		{name: "run fails", setup: func(t *testing.T, d fakeBootDocker) { d.write(t, "runfail", "") }, check: "boot-start", code: "boot_run_failed", hint: "pull access denied"},
		{name: "exits after answering", setup: func(t *testing.T, d fakeBootDocker) {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { d.write(t, "state", "false 2 false") }))
			t.Cleanup(srv.Close)
			d.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
		}, check: "boot-stay-up", code: "boot_exited", hint: "exited with code 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			tc.setup(t, docker)
			code, report, _ := runDoctorBoot(t, bootInspector(), "--json")
			c := bootCheck(t, report, tc.check)
			if code != 1 || c.Status != "error" || c.Code != tc.code || !strings.Contains(c.Hint, tc.hint) || !strings.Contains(c.Output, tc.out) {
				t.Fatalf("exit %d, %s = %+v", code, tc.check, c)
			}
			if !docker.removed(t) {
				t.Error("container not removed")
			}
		})
	}
}

func TestDoctorBootWithoutDocker(t *testing.T) {
	t.Setenv("GREGALE_DOCKER", filepath.Join(t.TempDir(), "missing-docker"))
	code, report, _ := runDoctorBoot(t, bootInspector(), "--json")
	if c := bootCheck(t, report, "boot-start"); code != 1 || c.Code != "boot_docker_missing" {
		t.Fatalf("exit %d, %+v", code, c)
	}
}

func TestDoctorBootFlagValidation(t *testing.T) {
	for _, args := range [][]string{{"--boot"}, {"--image", "example.com/app", "--env-file", "x.env"}, {"--image", "example.com/app", "--boot", "--env-file", " "}} {
		var stderr bytes.Buffer
		old := *main.OsStderr
		*main.OsStderr = &stderr
		code := main.CmdDoctorWithImageInspector(args, bootInspector())
		*main.OsStderr = old
		if code != 1 || !strings.Contains(stderr.String(), "requires") {
			t.Errorf("%q: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestParseProcNetListeners(t *testing.T) {
	text := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A
   1: 00000000000000000000000001000000:0BB8 00000000000000000000000000000000:0000 0A
   2: 0000000000000000FFFF00000100007F:0050 00000000000000000000000000000000:0000 0A
   3: 0100007F:1F90 0100007F:D431 01
`
	got := main.ProcNetListeners(text)
	want := []string{"[::]:8080", "[::1]:3000", "127.0.0.1:80"}
	if !slices.Equal(got, want) {
		t.Fatalf("listeners = %q, want %q", got, want)
	}
}
