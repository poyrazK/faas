package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeDevSecretClient struct {
	keys      []api.AppSecretResponse
	listCalls int
	sets      []struct {
		app, key, value, scope string
	}
}

func (f *fakeDevSecretClient) ListSecretsWithScope(context.Context, string, string) (api.AppSecretListResponse, error) {
	f.listCalls++
	return api.AppSecretListResponse{Secrets: f.keys}, nil
}

func (f *fakeDevSecretClient) SetSecretWithScope(_ context.Context, app, key, value, scope string) error {
	f.sets = append(f.sets, struct {
		app, key, value, scope string
	}{app, key, value, scope})
	return nil
}

func TestReadDevEnvFileIsSafeAndPreservesEquals(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dev")
	if err := os.WriteFile(path, []byte("# local\nTOKEN=secret=value\nEMPTY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, _, err := readDevEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0].Key != "TOKEN" || pairs[0].Value != "secret=value" || pairs[1].Value != "" {
		t.Fatalf("pairs = %+v", pairs)
	}

	if err := os.WriteFile(path, []byte("BROKEN secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = readDevEnvFile(path)
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("malformed env error = %v; plaintext must not be echoed", err)
	}
}

func TestDevEnvSyncIsKeyOnlyAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dev")
	if err := os.WriteFile(path, []byte("NEW_KEY=new-value\nEXISTING=rotated-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDevSecretClient{keys: []api.AppSecretResponse{{Key: "EXISTING"}}}
	state := &devEnvSyncState{}
	report, err := state.sync(context.Background(), fake, "dev-app", path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Keys != 2 || report.Added != 1 || report.Existing != 1 || !report.Changed {
		t.Fatalf("report = %+v", report)
	}
	if len(fake.sets) != 2 || fake.sets[0].value != "new-value" || fake.sets[1].value != "rotated-value" {
		t.Fatalf("sets = %+v", fake.sets)
	}
	if fake.sets[0].scope != "" || fake.sets[0].app != "dev-app" {
		t.Fatalf("sets = %+v; sync must use the default scope and developer app", fake.sets)
	}

	second, err := state.sync(context.Background(), fake, "dev-app", path)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || len(fake.sets) != 2 || fake.listCalls != 1 {
		t.Fatalf("second sync = %+v, list calls=%d, sets=%d; expected idempotent", second, fake.listCalls, len(fake.sets))
	}
}

func TestDevEnvSyncDoesNotMarkFailedUploadAsSynced(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dev")
	if err := os.WriteFile(path, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &failingDevSecretClient{}
	state := &devEnvSyncState{}
	if _, err := state.sync(context.Background(), fake, "dev-app", path); err == nil {
		t.Fatal("sync succeeded despite failed secret write")
	}
	if state.synced {
		t.Fatal("failed upload was marked synced")
	}
}

func TestReadDevServiceOverrideFileValidatesSupportedURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.services.local")
	if err := os.WriteFile(path, []byte("DATABASE_URL=postgresql://dev:secret@db.example.test/app\nREDIS_URL=rediss://cache.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, _, err := readDevServiceOverrideFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0].Key != "DATABASE_URL" || pairs[1].Key != "REDIS_URL" {
		t.Fatalf("pairs = %+v", pairs)
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown key", body: "API_TOKEN=secret-value\n", want: "not supported"},
		{name: "wrong scheme", body: "DATABASE_URL=redis://cache.example.test\n", want: "postgres"},
		{name: "loopback", body: "REDIS_URL=redis://localhost:6379\n", want: "runs remotely"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := readDevServiceOverrideFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error = %v, want %q without plaintext", err, tc.want)
			}
		})
	}
}

func TestDevServiceOverrideSyncUsesDeveloperAppAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.services.local")
	if err := os.WriteFile(path, []byte("DATABASE_URL=postgres://db.example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDevSecretClient{}
	state := &devEnvSyncState{}
	report, err := state.syncServiceOverrides(context.Background(), fake, "dev-app", path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || report.Keys != 1 || len(fake.sets) != 1 {
		t.Fatalf("report=%+v sets=%+v", report, fake.sets)
	}
	if fake.sets[0].app != "dev-app" || fake.sets[0].key != "DATABASE_URL" || fake.sets[0].scope != "" {
		t.Fatalf("set = %+v; override must use the developer app default scope", fake.sets[0])
	}
	second, err := state.syncServiceOverrides(context.Background(), fake, "dev-app", path)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || len(fake.sets) != 1 || fake.listCalls != 1 {
		t.Fatalf("second sync=%+v list calls=%d sets=%d; expected idempotent", second, fake.listCalls, len(fake.sets))
	}
}

type failingDevSecretClient struct{}

func (*failingDevSecretClient) ListSecretsWithScope(context.Context, string, string) (api.AppSecretListResponse, error) {
	return api.AppSecretListResponse{}, nil
}

func (*failingDevSecretClient) SetSecretWithScope(context.Context, string, string, string, string) error {
	return errors.New("write failed")
}

func TestPackExcludesExplicitDeveloperEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env.dev")
	servicePath := filepath.Join(dir, ".env.services.local")
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("TOKEN=never-upload-this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicePath, []byte("DATABASE_URL=postgres://never-upload-this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "source.tar.gz")
	if _, err := packDirToTarGz(dir, archivePath, defaultZeroConfigSourceCapMB, nil, envPath, servicePath); err != nil {
		t.Fatal(err)
	}
	f, err := openCustomerFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(header.Name, ".env.dev") || strings.Contains(header.Name, ".env.services.local") || strings.Contains(readTarEntry(t, tr), "never-upload-this") {
			t.Fatal("developer env file was included in source archive")
		}
	}
}

func readTarEntry(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
