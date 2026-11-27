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

// prod 2026-11-20: API_URL="https://…" in a pushed .env reached the app with
// its quotes. Quoted values are read the way dotenv and Docker Compose read
// them.
func TestEnvPushReadsQuotedValuesLikeDotenv(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			parts := strings.Split(r.URL.Path, "/")
			mu.Lock()
			got[parts[len(parts)-1]] = body.Value
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Quota: 25})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	file := filepath.Join(t.TempDir(), ".env")
	content := "URL=\"https://api.example.test/v1\"\n" +
		"export SINGLE='a \"literal\" $value'\n" +
		"ESCAPED=\"line1\\nsay \\\"hi\\\"\"\n" +
		"PLAIN=no-quotes\n" +
		"INNER=a\"b\"\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, restore := main.SwapIO(t)
	code := main.Run([]string{"env", "push", "--app", "hello", "-f", file})
	restore()
	if code != 0 {
		t.Fatalf("env push exit = %d", code)
	}
	want := map[string]string{
		"URL":     "https://api.example.test/v1",
		"SINGLE":  `a "literal" $value`,
		"ESCAPED": "line1\nsay \"hi\"",
		"PLAIN":   "no-quotes",
		"INNER":   `a"b"`,
	}
	mu.Lock()
	defer mu.Unlock()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
