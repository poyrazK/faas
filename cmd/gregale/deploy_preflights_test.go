package main_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A read-only state directory failed the deploy session write after the app
// was created, leaving an undeployed app behind (CLI journey 2026-11-05,
// #6625). The preflight runs first and names the fix; nothing remote changes.
func TestDeploySessionStoragePreflight(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	client := api.NewClient("https://api.example.test", "test-token")

	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	t.Setenv("XDG_STATE_HOME", readOnly)
	err := main.PreflightDeploySessionStorage(client)
	if err == nil || !strings.Contains(err.Error(), "nothing was created or deployed") || !strings.Contains(err.Error(), "XDG_STATE_HOME") {
		t.Fatalf("read-only state dir: err = %v", err)
	}

	writable := t.TempDir()
	t.Setenv("XDG_STATE_HOME", writable)
	if err := main.PreflightDeploySessionStorage(client); err != nil {
		t.Fatalf("writable state dir: %v", err)
	}
	var leftovers []string
	_ = filepath.WalkDir(writable, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Fatalf("preflight left files behind: %v", leftovers)
	}
}

// The deploy itself stops before creating the app when its session cannot
// be saved, and a retry with a writable state directory creates it once.
func TestDeployReadOnlyStateDirCreatesNoApp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	main.ResetJSONOut(t)
	var mu sync.Mutex
	var mutations []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mu.Lock()
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			mu.Unlock()
		}
		switch {
		case r.URL.Path == "/v1/account":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"acct-1","plan":"scale","status":"active"}`))
		default:
			http.Error(w, `{"status":404,"code":"not_found","title":"Not found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	t.Setenv("XDG_STATE_HOME", readOnly)

	src := t.TempDir()
	for name, body := range map[string]string{
		"package.json": `{"name":"demo","scripts":{"start":"node index.js"}}`,
		"index.js":     `require("http").createServer((q,s)=>s.end("ok")).listen(process.env.PORT||8080)`,
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.CmdDeployTarball([]string{"--path", src, "--name", "demo-readonly", "--yes", "--no-doctor"})
	restoreErr()
	if code == 0 {
		t.Fatalf("deploy with a read-only state dir succeeded")
	}
	if !strings.Contains(stderr.String(), "nothing was created or deployed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(mutations) != 0 {
		t.Fatalf("remote mutations before the session preflight: %v", mutations)
	}
}

// A Scale account's lookup timed out once and the deploy packed against the
// 100 MB floor, calling it the Free/Hobby cap (CLI journey 2026-11-05,
// #6631). The lookup now retries a transport failure once, and a fallback
// says it is local only and explains an over-cap pack.
func TestResolveSourceUploadCap(t *testing.T) {
	old := *main.SourceCapLookupBudgets
	*main.SourceCapLookupBudgets = []time.Duration{50 * time.Millisecond, 2 * time.Second}
	t.Cleanup(func() { *main.SourceCapLookupBudgets = old })

	for _, tc := range []struct {
		name         string
		handler      func(attempt int, w http.ResponseWriter, r *http.Request)
		wantMB       int
		wantVerified bool
		wantAttempts int
	}{
		{"plan read", func(_ int, w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"a","plan":"scale"}`))
		}, 250, true, 1},
		{"timeout then plan read", func(attempt int, w http.ResponseWriter, r *http.Request) {
			if attempt == 1 {
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(`{"id":"a","plan":"scale"}`))
		}, 250, true, 2},
		{"refused", func(_ int, w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"status":403,"code":"forbidden","title":"Forbidden"}`, http.StatusForbidden)
		}, 250, true, 1}, // the largest plan's cap; apid enforces the account's
		{"timeouts", func(_ int, _ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}, 100, false, 2},
		{"server error", func(_ int, w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"status":400,"code":"invalid_request","title":"Bad"}`, http.StatusBadRequest)
		}, 100, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				attempts++
				n := attempts
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				tc.handler(n, w, r)
			}))
			defer srv.Close()
			mb, verified, warning, explain := main.ResolveSourceUploadCapForTest(context.Background(), api.NewClient(srv.URL, "test-token"))
			if mb != tc.wantMB {
				t.Errorf("cap = %d MB, want %d", mb, tc.wantMB)
			}
			if verified != tc.wantVerified {
				t.Errorf("verified = %t, want %t", verified, tc.wantVerified)
			}
			mu.Lock()
			if attempts != tc.wantAttempts {
				t.Errorf("lookups = %d, want %d", attempts, tc.wantAttempts)
			}
			mu.Unlock()
			over := main.SourceUploadLimitError(mb)
			if verified {
				if warning != "" || explain(over) != over {
					t.Errorf("verified cap warned %q or rewrote %v", warning, explain(over))
				}
				return
			}
			if strings.Contains(warning, "Free") || strings.Contains(warning, "Hobby") || !strings.Contains(warning, "local only") {
				t.Errorf("fallback warning = %q", warning)
			}
			explained := explain(over).Error()
			if !strings.Contains(explained, "could not be read") || !strings.Contains(explained, "again") {
				t.Errorf("over-cap explanation = %q", explained)
			}
			other := errors.New("disk full")
			if explain(other) != other {
				t.Errorf("explain rewrote an unrelated error")
			}
		})
	}
}
