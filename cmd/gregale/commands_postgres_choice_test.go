package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPostgresCommandsNameInvalidChoices pins the production-us hunt #9
// finding that `postgres create --class prod` printed only the usage line,
// so a typo in an enumerated flag read like a syntax error. Every postgres
// command with a closed-set flag must name the flag, the value and the
// accepted set, before contacting the API.
func TestPostgresCommandsNameInvalidChoices(t *testing.T) {
	const requestID = "7a1f3c52-9a0e-4c41-8d36-2f1a8e8b9c10"
	cases := []struct {
		name string
		run  func([]string) int
		args []string
		want string
	}{
		{name: "create class", run: cmdPostgresCreate, args: []string{"orders", "--region", "us-east-2", "--class", "prod"},
			want: `invalid --class "prod" (want development|burstable|production)`},
		{name: "create availability", run: cmdPostgresCreate, args: []string{"orders", "--region", "us-east-2", "--availability", "multi_zone"},
			want: `invalid --availability "multi_zone" (want single_zone|high_availability)`},
		{name: "attach access", run: cmdPostgresAttach, args: []string{"orders", "api", "--scope", "production", "--access", "admin"},
			want: `invalid --access "admin" (want read_write|read_only|migration|data_api)`},
		{name: "bindings create access", run: cmdPostgresBindingsCreate, args: []string{"db-1", "--app", "app-1", "--scope", "production", "--environment-key", "DATABASE_URL", "--access", "superuser"},
			want: `invalid --access "superuser"`},
		{name: "resize class", run: cmdPostgresResize, args: []string{"orders", "--class", "huge", "--request-id", requestID},
			want: `invalid --class "huge"`},
		{name: "add postgres class", run: cmdAddPostgres, args: []string{"--app", "api", "--env", "production", "--class", "tiny"},
			want: `invalid --class "tiny"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_API", "http://127.0.0.1:1") // a request would fail loudly instead of reaching a real API
			var stderr bytes.Buffer
			code := 0
			_ = captureStderrSwap(t, &stderr, func() int { code = tc.run(tc.args); return code })
			if code == 0 {
				t.Fatalf("invalid choice accepted; stderr %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), tc.want)
			}
		})
	}
	// The auth mode of application Auth, a dark launch, is listed only
	// while Auth is turned on in the CLI.
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	var stderr bytes.Buffer
	_ = captureStderrSwap(t, &stderr, func() int { return cmdPostgresAttach([]string{"orders", "api", "--scope", "production", "--access", "admin"}) })
	if !strings.Contains(stderr.String(), `(want read_write|read_only|migration|data_api|auth)`) {
		t.Fatalf("with Auth on, stderr = %q", stderr.String())
	}
}
