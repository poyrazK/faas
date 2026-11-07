package main_test

import (
	"os"
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

const afterRestoreManifest = "hosting:\n  port: 8080\nlifecycle:\n  after_restore:\n    path: /internal/after-restore\n    timeout_ms: 2000\n"

// ChronoLake report: snapshot wakes of an app with an after_restore hook fell
// back to cold boots because its image had no secret-reload label. The deploy
// preflight now says so before the build.
func TestDoctorCheckSecretReload(t *testing.T) {
	for _, tc := range []struct {
		name        string
		manifest    string
		dockerfile  string
		wantPresent bool
		wantStatus  string
	}{
		{name: "hook without label warns", manifest: afterRestoreManifest, dockerfile: "FROM node:24-bookworm-slim\nCMD [\"node\", \"app.mjs\"]\n", wantPresent: true, wantStatus: "warn"},
		{name: "hook with label is ok", manifest: afterRestoreManifest, dockerfile: "FROM node:24-bookworm-slim\nLABEL com.gregale.secret-reload-signal=\"SIGHUP\"\n", wantPresent: true, wantStatus: "ok"},
		{name: "no hook is not checked", manifest: "hosting:\n  port: 8080\n", dockerfile: "FROM node:24-bookworm-slim\n"},
		{name: "source build is not checked", manifest: afterRestoreManifest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.dockerfile != "" {
				if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(tc.dockerfile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			check, present := main.DoctorCheckSecretReload(dir)
			if present != tc.wantPresent || check.Status != tc.wantStatus {
				t.Fatalf("check = %+v present=%v; want present=%v status=%q", check, present, tc.wantPresent, tc.wantStatus)
			}
			if tc.wantStatus == "warn" && (check.Code != "after_restore_without_secret_reload" || len(check.Sources) != 1 || check.Sources[0] != "Dockerfile:1") {
				t.Fatalf("warn finding = %+v", check)
			}
		})
	}
}
