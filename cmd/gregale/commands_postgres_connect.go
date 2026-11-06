package main

// `gregale postgres connect` (ADR-20261106105015 §3): open psql on a laptop
// against a managed database without exposing its credentials anywhere new.
//
// Managed credentials only exist sealed in a bound app's environment, so the
// command borrows that app: one task VM reads the binding's environment key,
// a second relays a local port to the database host through the existing
// port-forward built-in (ADR-1037). psql gets the original host for TLS
// verification and SNI and connects to the tunnel through PGHOSTADDR; the
// password travels in PGPASSWORD, never argv or the terminal.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskmux"
)

const postgresConnectUsage = "usage: gregale postgres connect <database> [--app SLUG] [--env KEY] [--access read_write|migration|read_only] [--port N] [--address A] [--proxy] [--wait-timeout D] [-- psql-args...]"

// postgresConnectAccessOrder is the default binding preference. Ad-hoc
// schema work (indexes, migrations) needs write access; a read-only binding
// is used only when it is the one the database has.
var postgresConnectAccessOrder = []string{"read_write", "migration", "read_only"}

// postgresConnectDSNLimit bounds what the env read may return.
const postgresConnectDSNLimit = 8 << 10

func cmdPostgresConnect(args []string) int {
	fs := newFlagSet("postgres-connect", flag.ContinueOnError)
	appSlug := fs.String("app", "", "bound app whose credentials to use")
	envKey := fs.String("env", "", "binding environment key (default: the chosen binding's)")
	access := fs.String("access", "", "binding access mode to use")
	port := fs.Int("port", 0, "local port (default: a free port)")
	address := fs.String("address", "127.0.0.1", "local address to listen on")
	proxy := fs.Bool("proxy", false, "keep the tunnel open without starting psql")
	waitTimeout := fs.Duration("wait-timeout", appExecInteractiveStartupWait, "maximum time to wait for each task VM to start")
	own, psqlArgs := splitPassthroughArgs(args)
	flags, positionals := splitArgsForFlags(own, "proxy")
	if err := fs.Parse(flags); err != nil || len(positionals) != 1 || *port < 0 || *port > 65535 {
		PrintUsage(osStderr, postgresConnectUsage, "postgres")
		return 1
	}
	if *access != "" && !slices.Contains(postgresConnectAccessOrder, *access) {
		return printErr("Invalid flags", fmt.Errorf("--access must be one of %s", strings.Join(postgresConnectAccessOrder, ", ")))
	}
	psqlPath := ""
	if !*proxy {
		path, err := exec.LookPath("psql")
		if err != nil {
			return printErr("psql not found", errors.New("install the PostgreSQL client, or use --proxy and connect with another tool"))
		}
		psqlPath = path
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	databaseID, err := postgresDatabaseID(ctx, client, positionals[0])
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	bindings, err := client.ListManagedPostgresBindings(ctx, databaseID)
	if err != nil {
		return printErr("Could not list PostgreSQL bindings", err)
	}
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list apps", err)
	}
	binding, slug, err := choosePostgresConnectBinding(bindings.Items, apps, *appSlug, *envKey, *access)
	if err != nil {
		return printErr("No usable binding", fmt.Errorf("%w; attach one with `gregale postgres attach %s <app>`", err, positionals[0]))
	}
	key := binding.EnvironmentKey
	PrintProgress(osStderr, "Using %s's %s binding (%s)", slug, key, binding.Access)

	dsn, err := readAppTaskEnv(ctx, client, slug, key, *waitTimeout)
	if err != nil {
		return printErr("Could not read the binding from "+slug, err)
	}
	target, err := parsePostgresConnectTarget(dsn)
	if err != nil {
		return printErr("Unusable binding value", err)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(*address, strconv.Itoa(*port)))
	if err != nil {
		return printErr("Could not listen locally", err)
	}
	defer func() { _ = ln.Close() }()
	session, closeSession, err := openAppTaskStreamSession(ctx, client, slug, []string{api.AppTaskPortForwardCommand, target.hostPort}, *waitTimeout)
	if err != nil {
		return printErr("Could not start the tunnel", err)
	}
	defer closeSession()
	forwardDone := make(chan error, 1)
	forwardCtx, cancelForward := context.WithCancel(ctx)
	defer cancelForward()
	go func() { forwardDone <- apptaskmux.Forward(forwardCtx, ln, session, session.stdout) }()

	localPort := ln.Addr().(*net.TCPAddr).Port
	if *proxy {
		PrintOK(osStderr, "Tunnel to %s open on %s (Ctrl-C to stop)", target.host, ln.Addr())
		_, _ = fmt.Fprintf(osStderr, "Connect with host=%s hostaddr=%s port=%d dbname=%s user=%s sslmode=%s; the password is the one in %s on %s.\n",
			target.host, tunnelHostAddr(*address), localPort, target.database, target.user, target.sslMode, key, slug)
		err := <-forwardDone
		if ctx.Err() == nil && err != nil {
			return printErr("Tunnel ended unexpectedly", err)
		}
		return 0
	}

	cmd := exec.CommandContext(context.Background(), psqlPath, psqlArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, osStdout, osStderr
	cmd.Env = append(os.Environ(), target.libpqEnv(tunnelHostAddr(*address), localPort)...)
	// psql owns Ctrl-C (it cancels the running query): stop cancelling ctx
	// on it, and keep this process alive by catching it, so the tunnel
	// stays up until psql exits.
	stop()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	runErr := cmd.Run()
	cancelForward()
	_ = ln.Close()
	_ = session.CloseStdin()
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	if runErr != nil {
		return printErr("psql failed", runErr)
	}
	return 0
}

// splitPassthroughArgs splits at the first bare "--".
func splitPassthroughArgs(args []string) ([]string, []string) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], args[i+1:]
	}
	return args, nil
}

func tunnelHostAddr(address string) string {
	if address == "" || address == "0.0.0.0" || address == "::" {
		return "127.0.0.1"
	}
	return address
}

// choosePostgresConnectBinding picks one ready binding whose app the caller
// can see, honoring --app, --env and --access, else the first by
// postgresConnectAccessOrder.
func choosePostgresConnectBinding(bindings []api.ManagedPostgresBinding, apps []api.AppResponse, appSlug, envKey, access string) (api.ManagedPostgresBinding, string, error) {
	slugByID := make(map[string]string, len(apps))
	for _, app := range apps {
		slugByID[app.ID] = app.Slug
	}
	type candidate struct {
		binding api.ManagedPostgresBinding
		slug    string
	}
	var candidates []candidate
	for _, binding := range bindings {
		slug := slugByID[binding.AppID]
		if binding.State != "ready" || slug == "" || !slices.Contains(postgresConnectAccessOrder, binding.Access) {
			continue
		}
		if (appSlug != "" && slug != appSlug) || (envKey != "" && binding.EnvironmentKey != envKey) || (access != "" && binding.Access != access) {
			continue
		}
		candidates = append(candidates, candidate{binding, slug})
	}
	if len(candidates) == 0 {
		return api.ManagedPostgresBinding{}, "", errors.New("no ready read_write, migration or read_only binding matches")
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		return slices.Index(postgresConnectAccessOrder, a.binding.Access) - slices.Index(postgresConnectAccessOrder, b.binding.Access)
	})
	return candidates[0].binding, candidates[0].slug, nil
}

// readAppTaskEnv runs `printenv KEY` in a fresh task VM of slug. The value
// never reaches the terminal.
func readAppTaskEnv(ctx context.Context, client *api.Client, slug, key string, waitTimeout time.Duration) (string, error) {
	session, closeSession, err := openAppTaskStreamSession(ctx, client, slug, []string{"printenv", key}, waitTimeout)
	if err != nil {
		return "", err
	}
	defer closeSession()
	_ = session.CloseStdin()
	out, readErr := io.ReadAll(io.LimitReader(session.stdout, postgresConnectDSNLimit+1))
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	terminal, waitErr := session.Wait(waitCtx)
	if waitErr != nil {
		return "", waitErr
	}
	if terminal.Failure != nil {
		return "", fmt.Errorf("%s (%s)", terminal.Failure.Message, terminal.Failure.Code)
	}
	if terminal.ExitCode != nil && *terminal.ExitCode == 127 {
		return "", errors.New("the app image has no printenv; deploy an image with coreutils or busybox to use this command")
	}
	if terminal.ExitCode != nil && *terminal.ExitCode != 0 {
		return "", fmt.Errorf("%s is not set in the app's environment; redeploy after attaching", key)
	}
	if readErr != nil && !errors.Is(readErr, io.ErrClosedPipe) {
		return "", readErr
	}
	if len(out) > postgresConnectDSNLimit {
		return "", errors.New("the value is larger than a connection string")
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

type postgresConnectTarget struct {
	host, hostPort, user, password, database, sslMode string
	params                                            url.Values
}

// parsePostgresConnectTarget accepts a postgres:// URL. Errors never quote
// the value: it carries a password.
func parsePostgresConnectTarget(dsn string) (postgresConnectTarget, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return postgresConnectTarget{}, errors.New("the binding is not a postgres:// URL")
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	target := postgresConnectTarget{
		host:     parsed.Hostname(),
		hostPort: net.JoinHostPort(parsed.Hostname(), port),
		user:     parsed.User.Username(),
		database: strings.TrimPrefix(parsed.Path, "/"),
		params:   parsed.Query(),
	}
	target.password, _ = parsed.User.Password()
	target.sslMode = target.params.Get("sslmode")
	if target.sslMode == "" {
		target.sslMode = "prefer"
	}
	if !api.ValidPortForwardTarget(target.hostPort) {
		return postgresConnectTarget{}, errors.New("the binding host is not a host:port")
	}
	return target, nil
}

// libpqEnv points libpq at the tunnel while keeping the original host for
// certificate verification and SNI (PGHOST + PGHOSTADDR).
func (t postgresConnectTarget) libpqEnv(hostAddr string, localPort int) []string {
	env := []string{
		"PGHOST=" + t.host,
		"PGHOSTADDR=" + hostAddr,
		"PGPORT=" + strconv.Itoa(localPort),
		"PGUSER=" + t.user,
		"PGPASSWORD=" + t.password,
		"PGDATABASE=" + t.database,
		"PGSSLMODE=" + t.sslMode,
		"PGAPPNAME=gregale-postgres-connect",
	}
	if options := t.params.Get("options"); options != "" {
		env = append(env, "PGOPTIONS="+options)
	}
	return env
}
