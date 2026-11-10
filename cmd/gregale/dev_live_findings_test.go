package main_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevLocalNameRefusedWithoutRemote(t *testing.T) {
	for _, flag := range []string{"--offline", "--no-remote"} {
		t.Run(flag, func(t *testing.T) {
			var stderr bytes.Buffer
			old := *main.OsStderr
			*main.OsStderr = &stderr
			defer func() { *main.OsStderr = old }()
			if code := main.Run([]string{"dev", "--local", flag, "--name", "shop", "--path", t.TempDir()}); code == 0 {
				t.Fatalf("--name with %s was accepted", flag)
			}
			if !strings.Contains(stderr.String(), "--name") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRefuseDevFunctionSet(t *testing.T) {
	if err := main.RefuseDevFunctionSet(t.TempDir()); err != nil {
		t.Fatalf("plain app refused: %v", err)
	}

	set := t.TempDir()
	if err := os.MkdirAll(filepath.Join(set, "functions", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(set, "functions", "api", "index.js"), []byte("export default () => new Response('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(set, "gregale.yaml"), []byte("functions:\n  - name: api\n    root: functions/api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := main.RefuseDevFunctionSet(set)
	if err == nil || !strings.Contains(err.Error(), "functions project (api)") || !strings.Contains(err.Error(), "gregale dev --local") {
		t.Fatalf("err = %v", err)
	}
}

func TestDevDiagnosticAPITimeoutIsNotABuildTimeout(t *testing.T) {
	err := errors.New(`could not reach the API: Post "https://api.example.test/v1/apps/x/deployments/dev-source": http2: timeout awaiting response headers`)
	for _, phase := range []string{"source_sync", "image_build", ""} {
		d := main.DevDiagnosticFromError(err, phase)
		if d.Code == api.CodeStageImageBuildTimeout || !strings.Contains(d.Title, "API") {
			t.Errorf("phase %q: diagnostic = %+v", phase, d)
		}
	}
	if d := main.DevDiagnosticFromError(errors.New("request timed out"), "source_sync"); d.Code == api.CodeStageImageBuildTimeout {
		t.Errorf("sync-phase timeout labelled as build timeout: %+v", d)
	}
}
