package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
