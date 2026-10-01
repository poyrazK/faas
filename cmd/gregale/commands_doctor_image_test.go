package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

type doctorInspectorFunc func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error)

func TestDoctorImageVolumePathsAndUDPIngressGuidance(t *testing.T) {
	inspector := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{Reference: "example.com/app", Digest: "sha256:fixture", Config: oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/server"}, Volumes: map[string]struct{}{"/var/data": {}, "/cache": {}}, ExposedPorts: map[string]struct{}{"5353/udp": {}}}}, nil
	})
	report := runDoctorImageChecks(t.Context(), "example.com/app", nil, inspector)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Image struct {
			VolumePaths []string `json:"volume_paths"`
		} `json:"image"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil || len(wire.Image.VolumePaths) != 2 || wire.Image.VolumePaths[0] != "/cache" || wire.Image.VolumePaths[1] != "/var/data" {
		t.Fatalf("volume projection=%+v err=%v", wire, err)
	}
	var human bytes.Buffer
	renderDoctorImage(&human, report.Image)
	if !strings.Contains(human.String(), `declared volumes: ["/cache" "/var/data"]`) || !strings.Contains(human.String(), "durable storage is not provisioned") {
		t.Fatalf("missing storage boundary in human output: %s", &human)
	}
	found := false
	volumeFinding := false
	for _, check := range report.Checks {
		if check.Name == "volumes" {
			volumeFinding = true
			if check.Status != "warn" || !strings.Contains(check.Hint, "cold fallback") || !strings.Contains(check.Hint, "node failure") || !strings.Contains(check.Fix, "external database") {
				t.Fatalf("missing durability boundary: %+v", check)
			}
		}
		if check.Name == "listener" {
			found = true
			if check.Status != "warn" || !strings.Contains(check.Fix, "app-owned UDP listeners") || !strings.Contains(check.Fix, "source CIDRs") || strings.Contains(check.Hint, "do not provide public ingress") {
				t.Fatalf("stale UDP guidance: %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("missing UDP-only listener finding")
	}
	if !volumeFinding {
		t.Fatal("missing volume durability finding")
	}
}

func (f doctorInspectorFunc) InspectImage(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.ImageInspection, error) {
	return f(ctx, ref, auth)
}

func TestDoctorImageFindings(t *testing.T) {
	for _, tc := range []struct {
		name, ref           string
		change              func(*oci.ImageConfig)
		wantError, wantWarn bool
	}{
		{"valid", "example.com/app", func(c *oci.ImageConfig) {}, false, false},
		{"arm", "example.com/app", func(c *oci.ImageConfig) { c.Architecture = "arm64" }, true, false},
		{"windows", "example.com/app", func(c *oci.ImageConfig) { c.OS = "windows" }, true, false},
		{"unknown platform", "example.com/app", func(c *oci.ImageConfig) { c.OS = "" }, true, false},
		{"invalid group", "example.com/app", func(c *oci.ImageConfig) { c.User = "1001:" }, true, false},
		{"missing command", "example.com/app", func(c *oci.ImageConfig) { c.Cmd = nil }, true, false},
		{"stateful", "postgres:16", func(c *oci.ImageConfig) {}, true, false},
		{"volume", "example.com/app", func(c *oci.ImageConfig) { c.Volumes = map[string]struct{}{"/data": {}} }, false, true},
		{"stop fallback", "example.com/app", func(c *oci.ImageConfig) { c.StopSignal = "SIGWINCH" }, false, true},
		{"healthcheck typo", "example.com/app", func(c *oci.ImageConfig) { c.Healthcheck = &oci.ImageHealthcheck{Test: []string{"TYPO", "curl"}} }, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/app/server"}, Env: map[string]string{"SECRET": "do-not-display"}}
			tc.change(&cfg)
			f := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
				return oci.ImageInspection{Reference: tc.ref, Config: cfg}, nil
			})
			r := runDoctorImageChecks(context.Background(), tc.ref, nil, f)
			if r.HasErrors() != tc.wantError || r.HasWarnings() != tc.wantWarn {
				t.Fatalf("unexpected findings: %+v", r.Checks)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "do-not-display") {
				t.Fatal("image env value leaked")
			}
			skipped := 0
			for _, c := range r.Checks {
				if c.Status == "skipped" {
					skipped++
				}
			}
			if skipped < 3 {
				t.Fatalf("unperformed runtime checks not marked skipped: %+v", r.Checks)
			}
		})
	}
}

func TestDoctorImageCommand(t *testing.T) {
	oldOut, oldErr, oldIn, oldJSON := osStdout, osStderr, osStdin, jsonOutput
	t.Cleanup(func() { osStdout, osStderr, osStdin, jsonOutput = oldOut, oldErr, oldIn, oldJSON })
	for _, tc := range []struct {
		name    string
		args    []string
		warning bool
		want    int
	}{
		{"json", []string{"--image", "example.com/app", "--json"}, false, 0},
		{"warnings", []string{"--image", "example.com/app", "--json"}, true, 0},
		{"strict", []string{"--image", "example.com/app", "--json", "--strict"}, true, 1},
		{"mixed path", []string{"--image", "example.com/app", "."}, false, 2},
		{"empty image", []string{"--image", ""}, false, 2},
		{"url", []string{"--image", "https://example.com/app"}, false, 2},
		{"missing credential flags", []string{"--image", "example.com/app", "--registry-user", "user"}, false, 2},
		{"credentials without image", []string{"--registry-user", "user", "--registry-password-stdin"}, false, 2},
		{"authenticated", []string{"--image", "example.com/app", "--json", "--registry-user", "user", "--registry-password-stdin"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			osStdout, osStderr, osStdin, jsonOutput = &out, &stderr, strings.NewReader("secret-token\n"), false
			calls := 0
			f := doctorInspectorFunc(func(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.ImageInspection, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing overall timeout")
				}
				if tc.name == "authenticated" && (auth == nil || auth.Username != "user" || auth.Password != "secret-token") {
					t.Error("credential input not passed correctly")
				}
				cfg := oci.ImageConfig{OS: "linux", Architecture: "amd64", Entrypoint: []string{"node"}, Cmd: []string{"app.js"}, User: "1000:1000"}
				if tc.warning {
					cfg.StopSignal = "typo"
				}
				return oci.ImageInspection{Reference: "example.com/app@sha256:" + strings.Repeat("a", 64), Digest: "sha256:" + strings.Repeat("a", 64), Config: cfg}, nil
			})
			if got := cmdDoctorWithImageInspector(tc.args, f); got != tc.want {
				t.Fatalf("exit %d want %d: %s", got, tc.want, stderr.String())
			}
			if tc.want == 2 {
				if calls != 0 {
					t.Fatal("invalid usage performed network inspection")
				}
				return
			}
			var report doctorReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("invalid JSON: %v: %s", err, out.String())
			}
			if len(report.Image.EffectiveArgv) != 2 || report.Image.User != "1000:1000" {
				t.Fatalf("incorrect runtime projection: %+v", report.Image)
			}
			if strings.Contains(out.String()+stderr.String(), "secret-token") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestDoctorImageRegistryErrorIsSafe(t *testing.T) {
	f := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{}, errors.New("server echoed private-token")
	})
	r := runDoctorImageChecks(context.Background(), "example.com/app", nil, f)
	b, _ := json.Marshal(r)
	if !r.HasErrors() || strings.Contains(string(b), "private-token") {
		t.Fatalf("unsafe or missing error: %s", b)
	}
}

func TestDoctorImageHumanReport(t *testing.T) {
	var out bytes.Buffer
	renderDoctorHuman(&out, doctorReport{Image: &doctorImage{Reference: "example.com/app@sha256:abc", Digest: "sha256:abc", OS: "linux", Architecture: "amd64", EffectiveArgv: []string{"node", "app.js"}, User: "app", WorkingDir: "/app", StopSignal: "SIGTERM"}, Checks: []doctorCheck{{Name: "runtime", Status: "skipped", Reason: "not executed"}}})
	for _, want := range []string{"linux/amd64", "app.js", "/app", "SIGTERM", "no layers downloaded", "skipped"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
}

func TestDoctorImagePlatformResolutionReport(t *testing.T) {
	source := "example.com/org/app@sha256:" + strings.Repeat("a", 64)
	child := "example.com/org/app@sha256:" + strings.Repeat("b", 64)
	inspector := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{InputReference: "example.com/org/app:latest", SourceReference: source, Reference: child, Config: oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"./app"}}}, nil
	})
	rep := runDoctorImageChecks(context.Background(), "example.com/org/app:latest", nil, inspector)
	if rep.HasErrors() || rep.Image.InputReference != "example.com/org/app:latest" || rep.Image.SourceReference != source || rep.Image.Reference != child {
		t.Fatalf("lost resolution provenance: %+v", rep)
	}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"input_reference", "source_reference", "reference"} {
		if !strings.Contains(string(body), `"`+key+`"`) {
			t.Fatalf("missing JSON field %s: %s", key, body)
		}
	}
	finding := doctorImageAccessError(&oci.PlatformSelectionError{Reason: "no compatible image", Available: []string{"linux/arm64"}})
	if finding.Status != "error" || !strings.Contains(finding.Hint, "linux/arm64") || !strings.Contains(finding.Fix, "Linux/amd64") {
		t.Fatalf("missing actionable platform diagnostic: %+v", finding)
	}
}

func TestDoctorImageListenerInference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ports  map[string]struct{}
		port   int
		status string
	}{
		{"implicit default", nil, api.DefaultAppPort, "ok"},
		{"single TCP", map[string]struct{}{"8787/tcp": {}}, 8787, "ok"},
		{"TCP and UDP", map[string]struct{}{"8787/tcp": {}, "53/udp": {}}, 8787, "ok"},
		{"ambiguous TCP", map[string]struct{}{"8787/tcp": {}, "9090/tcp": {}}, api.DefaultAppPort, "warn"},
		{"UDP only", map[string]struct{}{"53/udp": {}}, api.DefaultAppPort, "warn"},
		{"ignored malformed port", map[string]struct{}{"invalid/tcp": {}}, api.DefaultAppPort, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := oci.ImageConfig{OS: "linux", Architecture: "amd64", Cmd: []string{"/app/server"}, ExposedPorts: tc.ports}
			inspector := doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
				return oci.ImageInspection{Reference: "example.com/app", Config: cfg}, nil
			})
			report := runDoctorImageChecks(context.Background(), "example.com/app", nil, inspector)
			manifest, err := oci.ManifestFromConfig(oci.Config{Cmd: cfg.Cmd, ExposedPorts: cfg.ExposedPorts})
			if err != nil {
				t.Fatal(err)
			}
			if report.Image.ServingPort != tc.port || report.Image.ServingPort != manifest.EffectivePort() {
				t.Fatalf("doctor port %d, want %d; deployment port %d", report.Image.ServingPort, tc.port, manifest.EffectivePort())
			}
			found := false
			for _, check := range report.Checks {
				if check.Name == "listener" {
					found = true
					if check.Status != tc.status {
						t.Fatalf("listener: %+v", check)
					}
				}
			}
			if !found {
				t.Fatal("missing listener diagnostic")
			}
			body, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"serving_port":`) {
				t.Fatal("missing JSON serving port")
			}
		})
	}
}

func TestDoctorImageRejectsExcessListeners(t *testing.T) {
	ports := make(map[string]struct{})
	for i := 0; i <= api.WorkloadPortCapMax; i++ {
		ports[fmt.Sprintf("%d/tcp", 8000+i)] = struct{}{}
	}
	cfg := oci.ImageConfig{Cmd: []string{"/app/server"}, ExposedPorts: ports}
	if check := doctorImageContractCheck(cfg); check.Status != "error" || check.Code != api.CodeImageManifestInvalid {
		t.Fatalf("invalid listener contract accepted: %+v", check)
	}
	if check := doctorImageListenerCheck(cfg); check.Status != "error" {
		t.Fatalf("missing listener limit diagnostic: %+v", check)
	}
}

func TestDoctorImageExactHealthcheckTiming(t *testing.T) {
	report := runDoctorImageChecks(context.Background(), "fixture", nil, doctorInspectorFunc(func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
		return oci.ImageInspection{Reference: "fixture", Digest: "sha256:fixture", Config: oci.ImageConfig{
			OS: "linux", Architecture: "amd64", Entrypoint: []string{"/server"},
			Healthcheck: &oci.ImageHealthcheck{Test: []string{"CMD", "/probe"}, Retries: 2,
				ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(250 * time.Millisecond), TimeoutNS: int64(1500 * time.Millisecond), StartIntervalNS: int64(100 * time.Millisecond)}},
		}}, nil
	}))
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded doctorReport
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Image == nil || decoded.Image.Healthcheck == nil || decoded.Image.Healthcheck.ImageTiming == nil {
		t.Fatalf("JSON report dropped exact timing: %s", encoded)
	}
	var out bytes.Buffer
	renderDoctorImage(&out, decoded.Image)
	for _, want := range []string{"interval: 250ms", "timeout: 1.5s", "startup grace: 0s", "startup interval: 100ms", "retries: 2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	invalid := doctorImageHealthcheck(&oci.ImageHealthcheck{Test: []string{"CMD", "/probe"}, ImageTiming: &api.OCIHealthcheckTiming{TimeoutNS: 3}})
	if invalid.Status != "warn" {
		t.Fatalf("invalid exact timing accepted: %+v", invalid)
	}
}
