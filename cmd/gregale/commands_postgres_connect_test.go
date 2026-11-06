package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestChoosePostgresConnectBinding(t *testing.T) {
	apps := []api.AppResponse{{ID: "a1", Slug: "gateway"}, {ID: "a2", Slug: "store"}}
	bindings := []api.ManagedPostgresBinding{
		{ID: "ro", AppID: "a1", EnvironmentKey: "READ_DATABASE_URL", Access: "read_only", State: "ready"},
		{ID: "rw", AppID: "a2", EnvironmentKey: "DATABASE_URL", Access: "read_write", State: "ready"},
		{ID: "mig", AppID: "a1", EnvironmentKey: "MIGRATION_DATABASE_URL", Access: "migration", State: "ready"},
		{ID: "dead", AppID: "a1", EnvironmentKey: "OLD_URL", Access: "read_write", State: "failed"},
		{ID: "api", AppID: "a1", EnvironmentKey: "DATA_API", Access: "data_api", State: "ready"},
		{ID: "hidden", AppID: "other-account", EnvironmentKey: "DATABASE_URL", Access: "read_write", State: "ready"},
	}
	for _, tc := range []struct {
		name, app, env, access, wantID, wantSlug string
		wantErr                                  bool
	}{
		{name: "prefers read_write", wantID: "rw", wantSlug: "store"},
		{name: "app filter", app: "gateway", wantID: "mig", wantSlug: "gateway"},
		{name: "access filter", access: "read_only", wantID: "ro", wantSlug: "gateway"},
		{name: "env filter", env: "MIGRATION_DATABASE_URL", wantID: "mig", wantSlug: "gateway"},
		{name: "failed binding is skipped", env: "OLD_URL", wantErr: true},
		{name: "data_api is not a psql binding", env: "DATA_API", wantErr: true},
		{name: "unknown app", app: "nope", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, slug, err := choosePostgresConnectBinding(bindings, apps, tc.app, tc.env, tc.access)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got binding %s, want error", binding.ID)
				}
				return
			}
			if err != nil || binding.ID != tc.wantID || slug != tc.wantSlug {
				t.Fatalf("got %s/%s/%v, want %s/%s", binding.ID, slug, err, tc.wantID, tc.wantSlug)
			}
		})
	}
}

func TestParsePostgresConnectTarget(t *testing.T) {
	target, err := parsePostgresConnectTarget("postgres://app_rw:s3cr%40t@ep-cool-123.us-east-2.aws.neon.tech/chronolake?sslmode=verify-full&options=endpoint%3Dep-cool-123")
	if err != nil {
		t.Fatal(err)
	}
	if target.host != "ep-cool-123.us-east-2.aws.neon.tech" || target.hostPort != "ep-cool-123.us-east-2.aws.neon.tech:5432" ||
		target.user != "app_rw" || target.password != "s3cr@t" || target.database != "chronolake" || target.sslMode != "verify-full" {
		t.Fatalf("unexpected target %+v", target)
	}
	env := target.libpqEnv("127.0.0.1", 54321)
	for _, want := range []string{
		"PGHOST=ep-cool-123.us-east-2.aws.neon.tech", "PGHOSTADDR=127.0.0.1", "PGPORT=54321",
		"PGUSER=app_rw", "PGPASSWORD=s3cr@t", "PGDATABASE=chronolake", "PGSSLMODE=verify-full", "PGOPTIONS=endpoint=ep-cool-123",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("libpq env misses %q: %v", want, env)
		}
	}

	explicitPort, err := parsePostgresConnectTarget("postgresql://u:p@db.internal:6432/app")
	if err != nil || explicitPort.hostPort != "db.internal:6432" || explicitPort.sslMode != "prefer" {
		t.Fatalf("explicit port: %+v %v", explicitPort, err)
	}

	for _, bad := range []string{"mysql://u:secretpw@h/db", "not a url secretpw", "postgres:///db?password=secretpw"} {
		_, err := parsePostgresConnectTarget(bad)
		if err == nil {
			t.Fatalf("%q parsed", bad)
		}
		if strings.Contains(err.Error(), "secretpw") {
			t.Fatalf("error quotes the credential: %v", err)
		}
	}
}

func TestSplitPassthroughArgs(t *testing.T) {
	own, rest := splitPassthroughArgs([]string{"orders", "--proxy", "--", "-c", "select 1"})
	if !slices.Equal(own, []string{"orders", "--proxy"}) || !slices.Equal(rest, []string{"-c", "select 1"}) {
		t.Fatalf("got %v %v", own, rest)
	}
	if got := tunnelHostAddr("0.0.0.0"); got != "127.0.0.1" {
		t.Fatalf("tunnelHostAddr(0.0.0.0) = %s", got)
	}
}
