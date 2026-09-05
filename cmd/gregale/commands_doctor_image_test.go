package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

type doctorInspectorFunc func(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error)

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
			if len(report.Image.EffectiveArgv) != 2 || report.Image.User != "1000" {
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
