package main_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// localInspectJSON is `docker image inspect --format '{{json .}}'` output,
// trimmed to the fields doctor reads plus a few it ignores.
func localInspectJSON(arch string) string {
	return `{"Id":"sha256:` + strings.Repeat("b", 64) + `","RepoTags":["acme/api:dev"],"Os":"linux","Architecture":"` + arch + `","Size":73400320,
"Config":{"User":"1000","Env":["PATH=/usr/bin","SECRET=hunter2"],"Cmd":["node","server.js"],"WorkingDir":"/app",
"ExposedPorts":{"3000/tcp":{}},"Volumes":{"/data":{}},"StopSignal":"SIGINT",
"Healthcheck":{"Test":["CMD","curl","-f","http://localhost:3000/"],"Interval":30000000000,"Retries":3}},
"RootFS":{"Type":"layers","Layers":["sha256:` + strings.Repeat("c", 64) + `"]}}`
}

func registryDown() doctorInspectorFunc {
	return doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{}, errors.New("unauthorized: repository not found")
	})
}

func checkNamed(t *testing.T, report main.DoctorReport, name string) main.DoctorCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check in %+v", name, report.Checks)
	return main.DoctorCheck{}
}

func TestDoctorImageFallsBackToLocalDocker(t *testing.T) {
	report := main.RunDoctorImageChecksWithLocal(t.Context(), "acme/api:dev", registryDown(), map[string]string{"acme/api:dev": localInspectJSON("amd64")})
	img := report.Image
	if img.Source != "local-docker" || img.Digest != "sha256:"+strings.Repeat("b", 64) || img.ServingPort != 3000 || img.User != "1000" || img.WorkingDir != "/app" || img.StopSignal != "SIGINT" || img.Healthcheck == nil || len(img.VolumePaths) != 1 {
		t.Fatalf("image = %+v", img)
	}
	if c := checkNamed(t, report, "registry-metadata"); c.Status != "warn" || c.Code != "image_inspection_failed" || !strings.Contains(c.Fix, "gregale deploy --local-image acme/api:dev") {
		t.Errorf("registry-metadata = %+v", c)
	}
	for _, name := range []string{"platform", "runtime-contract", "listener", "healthcheck", "stop-signal"} {
		if c := checkNamed(t, report, name); c.Status != "ok" {
			t.Errorf("%s = %+v", name, c)
		}
	}
	if c := checkNamed(t, report, "image-size"); !strings.Contains(c.Hint, "70.0 MB unpacked") {
		t.Errorf("image-size = %+v", c)
	}
	if c := checkNamed(t, report, "volumes"); c.Status != "warn" {
		t.Errorf("volumes = %+v", c)
	}
	if report.HasErrors() {
		t.Errorf("local image reported errors: %+v", report.Checks)
	}
	var human bytes.Buffer
	main.RenderDoctorImage(&human, img)
	if !strings.Contains(human.String(), "source: local Docker store") || strings.Contains(human.String(), "hunter2") {
		t.Errorf("human output:\n%s", &human)
	}
}

func TestDoctorImageLocalFallbackLimits(t *testing.T) {
	images := map[string]string{"acme/api:dev": localInspectJSON("arm64")}
	t.Run("arm64 local image fails the platform check", func(t *testing.T) {
		report := main.RunDoctorImageChecksWithLocal(t.Context(), "acme/api:dev", registryDown(), images)
		if c := checkNamed(t, report, "platform"); c.Status != "error" || c.Code != api.CodeAppArchMismatch {
			t.Fatalf("platform = %+v", c)
		}
	})
	t.Run("not in local Docker keeps the registry error", func(t *testing.T) {
		report := main.RunDoctorImageChecksWithLocal(t.Context(), "acme/other:dev", registryDown(), images)
		if c := checkNamed(t, report, "registry-metadata"); c.Status != "error" || report.Image.Source != "" {
			t.Fatalf("registry-metadata = %+v, source %q", c, report.Image.Source)
		}
	})
	t.Run("a registry that has the image is not second-guessed", func(t *testing.T) {
		wrongPlatform := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
			return oci.ImageInspection{}, &oci.PlatformSelectionError{Reason: "no linux/amd64 image", Available: []string{"linux/arm64"}}
		})
		report := main.RunDoctorImageChecksWithLocal(t.Context(), "acme/api:dev", wrongPlatform, map[string]string{"acme/api:dev": localInspectJSON("amd64")})
		if c := checkNamed(t, report, "registry-metadata"); c.Status != "error" || c.Code != api.CodeImageManifestInvalid || report.Image.Source != "" {
			t.Fatalf("registry-metadata = %+v, source %q", c, report.Image.Source)
		}
	})
}

// The command reads the local store through the Docker client, asking for
// the linux/amd64 variant first.
func TestDoctorCommandReadsLocalDockerImage(t *testing.T) {
	docker := newFakeBootDocker(t)
	docker.write(t, "image", localInspectJSON("amd64"))
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := *main.OsStdout, *main.OsStderr, *main.JsonOutput
	*main.OsStdout, *main.OsStderr, *main.JsonOutput = &out, &stderr, false
	defer func() { *main.OsStdout, *main.OsStderr, *main.JsonOutput = oldOut, oldErr, oldJSON }()
	if code := main.CmdDoctorWithImageInspector([]string{"--image", "acme/api:dev"}, registryDown()); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "source: local Docker store") {
		t.Fatalf("output:\n%s", out.String())
	}
	if call := docker.calls(t)[0]; call != "image inspect --platform linux/amd64 --format {{json .}} acme/api:dev" {
		t.Fatalf("docker call = %q", call)
	}
	out.Reset()
	if code := main.CmdDoctorWithImageInspector([]string{"--image", "acme/api:dev", "--strict"}, registryDown()); code != 1 {
		t.Fatalf("--strict exit %d, want 1 for the registry warning", code)
	}
}

// --boot on a local-only image uses the local image's serving port.
func TestDoctorBootLocalImageUsesItsPort(t *testing.T) {
	docker := newFakeBootDocker(t)
	docker.write(t, "image", localInspectJSON("amd64"))
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	docker.write(t, "addr", strings.TrimPrefix(srv.URL, "http://"))
	code, report, _ := runDoctorBoot(t, registryDown(), "--json")
	if code != 0 || report.Image.Source != "local-docker" || !report.Image.Booted {
		t.Fatalf("exit %d, image %+v, checks %+v", code, report.Image, report.Checks)
	}
	for _, call := range docker.calls(t) {
		if strings.HasPrefix(call, "run ") && !strings.Contains(call, "--env PORT=3000") {
			t.Fatalf("docker run %q does not set PORT=3000", call)
		}
	}
}
