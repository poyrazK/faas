package main

import (
	"context"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
		{ID: "session", AppID: "a1", EnvironmentKey: "SESSION_DATABASE_URL", Access: "session", State: "ready"},
		{ID: "hidden", AppID: "other-account", EnvironmentKey: "DATABASE_URL", Access: "read_write", State: "ready"},
	}
	for _, tc := range []struct {
		name, app, env, access, wantID, wantSlug string
		wantErr                                  bool
	}{
		{name: "prefers read_write", wantID: "rw", wantSlug: "store"},
		{name: "app filter", app: "gateway", wantID: "mig", wantSlug: "gateway"},
		{name: "access filter", access: "read_only", wantID: "ro", wantSlug: "gateway"},
		{name: "session access filter", access: "session", wantID: "session", wantSlug: "gateway"},
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
}

func TestTunnelEndpointIsNumeric(t *testing.T) {
	for _, tc := range []struct {
		addr *net.TCPAddr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4zero, Port: 1}, "127.0.0.1"},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 1}, "::1"},
		{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}, "127.0.0.1"},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 1}, "::1"},
	} {
		if got, port := tunnelEndpoint(tc.addr); got != tc.want || port != 1 {
			t.Errorf("tunnelEndpoint(%v) = %s:%d, want %s", tc.addr, got, port, tc.want)
		}
	}
	// --address localhost: the listener's bound IP, never the hostname.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if host, _ := tunnelEndpoint(ln.Addr()); net.ParseIP(host) == nil {
		t.Fatalf("PGHOSTADDR %q is not an IP", host)
	}
}

// Review finding 1: stopping the signal context cancelled the tunnel's
// context, so the listener closed before psql dialed it.
func TestRunOverTunnelKeepsTheTunnelUpAfterSignalStop(t *testing.T) {
	database, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	go func() {
		for {
			conn, err := database.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// A relay that stops serving when its context ends, like apptaskmux.Forward.
	forward := func(ctx context.Context) error {
		go func() { <-ctx.Done(); _ = ln.Close() }()
		for {
			client, err := ln.Accept()
			if err != nil {
				return err
			}
			upstream, err := net.Dial("tcp", database.Addr().String())
			if err != nil {
				return err
			}
			go func() { _, _ = io.Copy(upstream, client) }()
			go func() { _, _ = io.Copy(client, upstream) }()
		}
	}
	_, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	hostAddr, port := tunnelEndpoint(ln.Addr())
	err = runOverTunnel(stop, forward, func() error {
		time.Sleep(20 * time.Millisecond) // psql starts after the signal context stopped
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(hostAddr, strconv.Itoa(port)), time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("select 1")); err != nil {
			return err
		}
		reply := make([]byte, 8)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, err = io.ReadFull(conn, reply)
		return err
	})
	if err != nil {
		t.Fatalf("psql could not use the tunnel: %v", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("tunnel still listening after psql exited")
	}
}
