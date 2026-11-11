package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func envPushServer(t *testing.T) (*[]string, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			parts := strings.Split(r.URL.Path, "/")
			mu.Lock()
			puts = append(puts, parts[len(parts)-1])
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Quota: 25})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return &puts, &mu
}

// TestEnvPushAcceptsExportLinesAndChecksKeysFirst pins Hunt 32 H32-68: an
// `export KEY="v"` line failed as "Invalid secret key" after the earlier
// lines had already been saved.
func TestEnvPushAcceptsExportLinesAndChecksKeysFirst(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.env")
	if err := os.WriteFile(good, []byte("A=alpha\nB=bravo\nexport C=\"quoted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	puts, mu := envPushServer(t)
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"env", "push", "--app", "hello", "-f", good}); code != 0 {
		t.Fatalf("env push exit = %d", code)
	}
	mu.Lock()
	got := append([]string(nil), *puts...)
	*puts = nil
	mu.Unlock()
	if strings.Join(got, ",") != "A,B,C" {
		t.Fatalf("PUT keys = %v, want A,B,C", got)
	}

	bad := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(bad, []byte("A=alpha\nB=bravo\nnot a key=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.Run([]string{"env", "push", "--app", "hello", "-f", bad}); code == 0 {
		t.Fatal("a file with an invalid key was pushed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*puts) != 0 {
		t.Fatalf("keys written before the invalid one was found: %v", *puts)
	}
}
