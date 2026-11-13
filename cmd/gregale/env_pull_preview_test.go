package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func runEnvPullPreview(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &out, &stderr
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	code := main.Run(append([]string{"env", "pull"}, args...))
	return code, out.String(), stderr.String()
}

type envPullTestReceipt struct {
	App           string `json:"app_slug"`
	Scope         string `json:"scope"`
	Remote        int    `json:"remote_key_count"`
	Added         int    `json:"added_key_count"`
	Preserved     int    `json:"preserved_key_count"`
	DryRun        bool   `json:"dry_run"`
	WriteRequired bool   `json:"write_required"`
	Written       bool   `json:"written"`
	Changes       []struct {
		Key    string `json:"key"`
		Action string `json:"action"`
	} `json:"changes"`
}

func readEnvPullReceipt(t *testing.T, output string) envPullTestReceipt {
	t.Helper()
	var receipt envPullTestReceipt
	if err := json.Unmarshal([]byte(output), &receipt); err != nil {
		t.Fatalf("invalid JSON receipt: %v: %s", err, output)
	}
	return receipt
}

func envPullPreviewServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("FAAS_JSON", "0")
	t.Chdir(t.TempDir())
}

func assertEnvPullContent(t *testing.T, path, want string) os.FileInfo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("local contents changed unexpectedly: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestEnvPullPreviewMatchesApplyAndNoop(t *testing.T) {
	var calls atomic.Int32
	envPullPreviewServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/secrets" || r.URL.Query().Get("scope") != "staging" {
			t.Errorf("unexpected remote request: %s %s", r.Method, r.URL)
		}
		main.WriteJSONTest(w, api.AppSecretListResponse{Count: 99, Secrets: []api.AppSecretResponse{{Key: "Z_NEW"}, {Key: "EXIST"}, {Key: "A_NEW"}}})
	})
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	existing := "# a comment\r\nexport EXIST=keep-private-value\r\nLOCAL_ONLY=local-private-value"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	before := assertEnvPullContent(t, path, existing)
	args := []string{"--app", "demo", "--env", "staging", "-o", path}
	code, text, stderr := runEnvPullPreview(t, append(args, "--dry-run")...)
	if code != 0 || !strings.Contains(text, "add") || !strings.Contains(text, "preserve") || !strings.Contains(text, "local_only") {
		t.Fatalf("text preview: %d, %s, %s", code, text, stderr)
	}
	code, output, stderr := runEnvPullPreview(t, append(args, "--dry-run", "--json")...)
	if code != 0 {
		t.Fatalf("preview: %s", stderr)
	}
	preview := readEnvPullReceipt(t, output)
	if !preview.DryRun || preview.Written || !preview.WriteRequired || preview.Remote != 3 || preview.Added != 2 || preview.Preserved != 2 || preview.App != "demo" || preview.Scope != "staging" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	var decisions []string
	for _, row := range preview.Changes {
		decisions = append(decisions, row.Key+":"+row.Action)
	}
	if !reflect.DeepEqual(decisions, []string{"A_NEW:add", "EXIST:preserve", "LOCAL_ONLY:local_only", "Z_NEW:add"}) {
		t.Fatalf("unstable or incomplete decisions: %v", decisions)
	}
	after := assertEnvPullContent(t, path, existing)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("preview changed the file or created a temporary file")
	}
	code, applied, stderr := runEnvPullPreview(t, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("apply: %s", stderr)
	}
	receipt := readEnvPullReceipt(t, applied)
	if receipt.DryRun || !receipt.Written || !reflect.DeepEqual(receipt.Changes, preview.Changes) {
		t.Fatalf("apply disagrees with preview: %+v", receipt)
	}
	want := existing + "\r\nA_NEW=\r\nZ_NEW=\r\n"
	written := assertEnvPullContent(t, path, want)
	if runtime.GOOS != "windows" && written.Mode().Perm() != 0o600 {
		t.Fatal("replacement file is not private")
	}
	code, noop, stderr := runEnvPullPreview(t, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("no-op pull: %s", stderr)
	}
	unchanged := readEnvPullReceipt(t, noop)
	if unchanged.WriteRequired || unchanged.Written || unchanged.Added != 0 {
		t.Fatalf("no-op receipt: %+v", unchanged)
	}
	current := assertEnvPullContent(t, path, want)
	if !os.SameFile(written, current) || !written.ModTime().Equal(current.ModTime()) {
		t.Fatal("unchanged file was rewritten")
	}
	if calls.Load() != 4 {
		t.Fatalf("unexpected request count: %d", calls.Load())
	}
	if strings.Contains(text+output+applied+noop+stderr, "private-value") {
		t.Fatal("receipt disclosed local values")
	}
}

func TestEnvPullPreviewDoesNotCreateMissingOutput(t *testing.T) {
	for _, keys := range [][]api.AppSecretResponse{nil, {{Key: "NEW_KEY"}}} {
		t.Run(map[bool]string{true: "empty", false: "keys"}[len(keys) == 0], func(t *testing.T) {
			envPullPreviewServer(t, func(w http.ResponseWriter, _ *http.Request) {
				main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: keys})
			})
			dir := t.TempDir()
			path := filepath.Join(dir, ".env")
			args := []string{"--app", "demo", "-o", path, "--json"}
			code, output, stderr := runEnvPullPreview(t, append(args, "--dry-run")...)
			if code != 0 || !readEnvPullReceipt(t, output).WriteRequired {
				t.Fatalf("missing-output preview: %s, %s", output, stderr)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("preview created output or temporary files")
			}
			code, output, stderr = runEnvPullPreview(t, args...)
			if code != 0 || !readEnvPullReceipt(t, output).Written {
				t.Fatalf("new-output apply: %s, %s", output, stderr)
			}
			want := ""
			if len(keys) > 0 {
				want = "NEW_KEY=\n"
			}
			assertEnvPullContent(t, path, want)
		})
	}
}

func TestEnvPullRefusesEditsDuringRemoteRead(t *testing.T) {
	for _, change := range []string{"edit", "replace", "create", "delete"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if change != "create" {
				if err := os.WriteFile(path, []byte("LOCAL=original\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			envPullPreviewServer(t, func(w http.ResponseWriter, _ *http.Request) {
				var err error
				switch change {
				case "edit", "create":
					err = os.WriteFile(path, []byte("LOCAL=editor-value\n"), 0o600)
				case "replace":
					err = os.WriteFile(path+".other", []byte("LOCAL=original\n"), 0o600)
					if err == nil {
						err = os.Rename(path+".other", path)
					}
				case "delete":
					err = os.Remove(path)
				}
				if err != nil {
					t.Errorf("fixture edit: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "NEW_KEY"}}})
			})
			code, _, stderr := runEnvPullPreview(t, "--app", "demo", "-o", path)
			if code == 0 || !strings.Contains(stderr, "changed since planning") {
				t.Fatalf("concurrent edit accepted: %d, %s", code, stderr)
			}
			switch change {
			case "edit", "create":
				assertEnvPullContent(t, path, "LOCAL=editor-value\n")
			case "replace":
				assertEnvPullContent(t, path, "LOCAL=original\n")
			case "delete":
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("deleted file was recreated")
				}
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".gregale-env-pull-*"))
			if err != nil || len(leftovers) > 0 {
				t.Fatal("failed apply left temporary files")
			}
		})
	}
}

func TestEnvPullStagedFailurePreservesOriginal(t *testing.T) {
	for _, failure := range []string{"cancel", "abort", "publish"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte("LOCAL=private\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			commit, cleanup, temp, err := main.StageEnvPullWriteForTest(t, path, []byte("LOCAL=private\nNEW_KEY=\n"))
			if err != nil {
				t.Fatal(err)
			}
			assertEnvPullContent(t, path, "LOCAL=private\n")
			switch failure {
			case "cancel":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := commit(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation ignored: %v", err)
				}
			case "abort":
				cleanup()
			case "publish":
				if err := os.Remove(temp); err != nil {
					t.Fatal(err)
				}
				if err := commit(t.Context()); err == nil {
					t.Fatal("missing staged file was published")
				}
			}
			cleanup()
			assertEnvPullContent(t, path, "LOCAL=private\n")
			if _, err := os.Lstat(temp); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("aborted temporary file remained")
			}
		})
	}
}

func TestEnvPullRejectsInvalidRemoteKeyWithoutPrintingIt(t *testing.T) {
	envPullPreviewServer(t, func(w http.ResponseWriter, _ *http.Request) {
		main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "BAD\nINJECT=private-value"}}})
	})
	path := filepath.Join(t.TempDir(), ".env")
	code, output, stderr := runEnvPullPreview(t, "--app", "demo", "-o", path, "--dry-run", "--json")
	if code == 0 || strings.Contains(output+stderr, "private-value") {
		t.Fatalf("invalid key was accepted or disclosed: %d", code)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid response created output")
	}
}

func TestEnvPullNoopPreservesRawContent(t *testing.T) {
	for name, contents := range map[string]string{"empty": "", "comment": "# no final newline", "assignment": "KEEP=private-value"} {
		t.Run(name, func(t *testing.T) {
			envPullPreviewServer(t, func(w http.ResponseWriter, _ *http.Request) {
				var keys []api.AppSecretResponse
				if strings.HasPrefix(contents, "KEEP=") {
					keys = []api.AppSecretResponse{{Key: "KEEP"}}
				}
				main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: keys})
			})
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			before := assertEnvPullContent(t, path, contents)
			code, output, stderr := runEnvPullPreview(t, "--app", "demo", "-o", path, "--json")
			if code != 0 {
				t.Fatal(stderr)
			}
			receipt := readEnvPullReceipt(t, output)
			if receipt.Written || receipt.WriteRequired || strings.Contains(output, "private-value") {
				t.Fatal("no-op pull changed the file or reported values")
			}
			after := assertEnvPullContent(t, path, contents)
			if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("no-op pull replaced the original file")
			}
		})
	}
}

func TestEnvPullRejectsUnsafeDestinationBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	envPullPreviewServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		main.WriteJSONTest(w, api.AppSecretListResponse{})
	})
	for _, kind := range []string{"directory", "symlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".env")
			target := filepath.Join(dir, "target")
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "symlink" {
					if err := os.WriteFile(target, []byte("OUTSIDE=private\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			for _, preview := range []bool{true, false} {
				args := []string{"--app", "demo", "-o", path}
				if preview {
					args = append(args, "--dry-run")
				}
				if code, _, _ := runEnvPullPreview(t, args...); code == 0 {
					t.Fatal("unsafe destination was accepted")
				}
			}
			if kind == "symlink" {
				assertEnvPullContent(t, target, "OUTSIDE=private\n")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe destination reached the API")
	}
}

func TestEnvPullRechecksSymlinkBeforePublishing(t *testing.T) {
	dir := t.TempDir()
	path, target := filepath.Join(dir, ".env"), filepath.Join(dir, "outside")
	if err := os.WriteFile(path, []byte("LOCAL=original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("OUTSIDE=private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commit, cleanup, _, err := main.StageEnvPullWriteForTest(t, path, []byte("NEW_KEY=\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := commit(t.Context()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("swapped symlink accepted: %v", err)
	}
	cleanup()
	assertEnvPullContent(t, target, "OUTSIDE=private\n")
}
