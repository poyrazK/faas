package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// `gregale keys list` showed revoked keys exactly like live ones.
func TestKeysListMarksRevokedKeys(t *testing.T) {
	resetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"a","label":"live-key","prefix":"fp_live_aaaaaaaa","status":"active"},` +
			`{"id":"b","label":"old-key","prefix":"fp_live_bbbbbbbb","status":"revoked"}]`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	code := cmdKeys([]string{"list"})
	os.Stdout = oldStdout
	_ = writer.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(reader)
	if code != 0 {
		t.Fatalf("keys list = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "active") || !strings.HasSuffix(strings.TrimSpace(lines[1]), "revoked") {
		t.Fatalf("keys list output = %q, want the live row unchanged and the revoked row marked", out.String())
	}
}

func TestKeysListHelpNeverRequestsAPI(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		direct bool
	}{
		{name: "public long help", args: []string{"keys", "list", "--help"}},
		{name: "public short help", args: []string{"keys", "list", "-h"}},
		{name: "public JSON help", args: []string{"keys", "list", "--help", "--json"}},
		{name: "handler long help", args: []string{"list", "--help"}, direct: true},
		{name: "handler short help", args: []string{"list", "-h"}, direct: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Error(w, "help must not make a request", http.StatusInternalServerError)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")

			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })

			var code int
			if tc.direct {
				code = cmdKeys(tc.args)
			} else {
				code = run(tc.args)
			}
			if code != 0 {
				t.Fatalf("command returned %d, want 0; output: %q", code, stdout.String())
			}
			if calls != 0 {
				t.Fatalf("help made %d API request(s)", calls)
			}
			if !strings.Contains(stdout.String(), "gregale keys list") {
				t.Fatalf("help output does not identify the list command: %q", stdout.String())
			}
		})
	}
}
