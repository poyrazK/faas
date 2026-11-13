package main

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/local"
)

// gregale dev --local (ADR-20261107210000).
var MatchFunctionRoute = matchFunctionRoute

const (
	DevLocalTokenHeader = devLocalTokenHeader
	DevLocalTriggerPath = devLocalTriggerPath
)

// PgRelayServeForTest runs the loopback Postgres TLS relay.
func PgRelayServeForTest(ctx context.Context, ln net.Listener, upstream, host, sslMode string, roots *x509.CertPool) {
	pgRelay{upstream: upstream, host: host, sslMode: sslMode, roots: roots}.serve(ctx, ln)
}

// LocalPostgresURLForTest points a binding URL at a loopback port.
func LocalPostgresURLForTest(dsn string, port int) (string, error) {
	target, err := parsePostgresConnectTarget(dsn)
	if err != nil {
		return "", err
	}
	return localPostgresURL(target, port), nil
}

// Offline stand-ins (slice 3).
var (
	OfflinePostgresRunArgsForTest = func(projectDir string) []string { return postgresRunArgs(offlineNames(projectDir)) }
	OfflineS3RunArgsForTest       = func(projectDir string) []string { return s3RunArgs(offlineNames(projectDir)) }
	OfflinePostgresURLForTest     = offlinePostgresURL
	OfflineBucketEnvForTest       = offlineBucketEnv
	OfflineOptionsForTest         = func(projectDir string, postgres bool, buckets []string) (bool, []string, error) {
		opts, err := offlineOptionsFor(projectDir, postgres, buckets, false)
		return opts.Postgres, opts.Buckets, err
	}
)

// DevLocalEnvForTest layers the local session's environment.
var DevLocalEnvForTest = devLocalEnv

// LocalPythonEnvironmentForTest resolves the function's interpreter and env.
var LocalPythonEnvironmentForTest = localPythonEnvironment

// DevLocalPreflightForTest validates the same workload and environment as startup.
func DevLocalPreflightForTest(ctx context.Context, root, command string, env []string) error {
	fns, app, err := planDevLocalWorkload(root, command)
	if err != nil {
		return err
	}
	return devLocalPreflight(ctx, root, fns, app, env)
}

// PlanDevLocalWorkloadForTest reports the selected local execution contract.
func PlanDevLocalWorkloadForTest(root, command string) (string, string, bool, error) {
	fns, app, err := planDevLocalWorkload(root, command)
	if err != nil || app == nil {
		return "", "", len(fns) > 0, err
	}
	return app.command, app.framework, false, nil
}

func DevLocalAppFrameworkReloadForTest(root, command string) (bool, error) {
	plan, err := planDevLocalApp(root, command)
	return plan.frameworkReload, err
}

// DevLocalServerForTest plans and prepares the project's functions and
// returns the local server's handler and its trigger token.
func DevLocalServerForTest(t *testing.T, projectDir string) (http.Handler, string, error) {
	t.Helper()
	fns, err := planLocalFunctions(projectDir)
	if err != nil {
		return nil, "", err
	}
	cache := t.TempDir()
	for _, fn := range fns {
		if err := fn.prepare(context.Background(), cache, os.Environ()); err != nil {
			return nil, "", err
		}
	}
	srv, err := newDevLocalServer(fns, projectDir)
	if err != nil {
		return nil, "", err
	}
	srv.out = io.Discard
	srv.port = devLocalDefaultPort
	t.Cleanup(func() {
		for _, fn := range fns {
			local.Reload(fn.process())
		}
	})
	return srv, srv.token, nil
}

// DevLocalListeningServerForTest is DevLocalServerForTest on a real loopback
// listener, as cmdDevLocal runs it, so functions can call their siblings.
// It returns the server's base URL.
func DevLocalListeningServerForTest(t *testing.T, projectDir string) (string, error) {
	t.Helper()
	fns, err := planLocalFunctions(projectDir)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cache := t.TempDir()
	for _, fn := range fns {
		fn.devPort = port
		if err := fn.prepare(context.Background(), cache, os.Environ()); err != nil {
			_ = ln.Close()
			return "", err
		}
	}
	srv, err := newDevLocalServer(fns, projectDir)
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	srv.out = io.Discard
	srv.port = port
	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		_ = httpSrv.Close()
		for _, fn := range fns {
			local.Reload(fn.process())
		}
	})
	return "http://127.0.0.1:" + strconv.Itoa(port), nil
}

// FunctionLimitsLine exposes functionLimitsLine.
var FunctionLimitsLine = functionLimitsLine
