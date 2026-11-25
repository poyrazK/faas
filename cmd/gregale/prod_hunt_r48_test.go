package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r48: `env set A=1 B= C=2` saved A, failed at B and never wrote
// C; an empty value is now refused before any write.
func TestEnvSetRefusesEmptyValueBeforeWriting(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "env", "set", "--app", "demo", "A=1", "B=", "C=2")
	if code == 0 || !strings.Contains(stderr, "B: secret value must not be empty") {
		t.Fatalf("env set = %d %q, want the empty B refused", code, stderr)
	}
	if n := writes.Load(); n != 0 {
		t.Fatalf("%d writes before the refusal, want none", n)
	}
}

// Prod hunt r48: `KEY="x" # note` in a --file was refused as an unclosed
// multi-line value; a value that spans lines still reads whole.
func TestSecretsFileQuotedValueWithComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.env")
	body := "URL=\"https://x\" # note\nPEM=\"-----BEGIN\nabc\n-----END\"\nPORT=3000 # web\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, err := main.ReadSecretsFile(path)
	if err != nil {
		t.Fatalf("ReadSecretsFile: %v", err)
	}
	want := map[string]string{"URL": "https://x", "PEM": "-----BEGIN\nabc\n-----END", "PORT": "3000"}
	if len(pairs) != len(want) {
		t.Fatalf("pairs = %d, want %d", len(pairs), len(want))
	}
	for _, p := range pairs {
		if want[p.Key] != p.Value {
			t.Errorf("%s = %q, want %q", p.Key, p.Value, want[p.Key])
		}
	}
}

// Prod hunt r48: a trailing ` # comment` stayed in the value.
func TestDotenvValueDropsTrailingComment(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"3000 # web port", "3000"},
		{"3000\t# web port", "3000"},
		{"abc#def", "abc#def"},
		{`"x y" # note`, "x y"},
		{`"a \" # b" # note`, `a " # b`},
		{`'single # kept' # note`, "single # kept"},
		{`"plain"`, "plain"},
		{`'plain'`, "plain"},
		{`"unterminated # x`, `"unterminated # x`},
		{"plain", "plain"},
	} {
		if got := main.DotenvValue(tc.in); got != tc.want {
			t.Errorf("DotenvValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
