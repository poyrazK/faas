//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const postgresBindingProbeTimeout = 5 * time.Second

type postgresBindingProbeConn interface {
	QueryRow(context.Context, string) error
	Close(context.Context) error
}

type postgresBindingProbeConnectFunc func(context.Context, *pgx.ConnConfig) (postgresBindingProbeConn, error)

type pgxPostgresBindingProbeConn struct {
	conn *pgx.Conn
}

func (c pgxPostgresBindingProbeConn) QueryRow(ctx context.Context, query string) error {
	var result int
	if err := c.conn.QueryRow(ctx, query).Scan(&result); err != nil {
		return err
	}
	if result != 1 {
		return errPostgresBindingProbeUnexpectedResult
	}
	return nil
}

func (c pgxPostgresBindingProbeConn) Close(ctx context.Context) error {
	return c.conn.Close(ctx)
}

var errPostgresBindingProbeUnexpectedResult = errors.New("unexpected result from read-only connectivity query")

func isPostgresBindingProbeCommand(req apptaskproto.Request) bool {
	return len(req.Command) > 0 && req.Command[0] == api.AppTaskPostgresBindingProbeCommand
}

func executePostgresBindingProbeCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	return executePostgresBindingProbeCommandWithConnector(ctx, req, manifest, secrets, apiEnv, stdout, connectPostgresBindingProbe)
}

func executePostgresBindingProbeCommandWithConnector(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer, connect postgresBindingProbeConnectFunc) (apptaskproto.Result, error) {
	report := api.PostgresBindingProbeReport{
		Environment:   api.PostgresBindingProbeCheck{Status: "not_checked"},
		Configuration: api.PostgresBindingProbeCheck{Status: "not_checked"},
		Connection:    api.PostgresBindingProbeCheck{Status: "not_checked"},
		Query:         api.PostgresBindingProbeCheck{Status: "not_checked"},
	}
	if req.CommandShell || len(req.Command) != 2 {
		report.Error = "invalid platform PostgreSQL binding probe request"
		return writePostgresBindingProbeReport(stdout, report)
	}
	key := req.Command[1]
	if api.ValidateEnvKey(key) != nil {
		report.Environment = api.PostgresBindingProbeCheck{Status: "failed", Detail: "environment key is invalid"}
		report.Error = "environment key is invalid"
		return writePostgresBindingProbeReport(stdout, report)
	}
	report.EnvironmentKey = key
	databaseURL := appTaskEnvValue(BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv), key)
	if databaseURL == "" {
		report.Environment = api.PostgresBindingProbeCheck{Status: "failed", Detail: "environment variable is missing from this deployment"}
		report.Error = "database connection environment variable is missing"
		return writePostgresBindingProbeReport(stdout, report)
	}
	report.Environment = api.PostgresBindingProbeCheck{Status: "passed", Detail: "environment variable is present in this deployment"}
	report = runPostgresBindingProbe(ctx, report, databaseURL, connect)
	return writePostgresBindingProbeReport(stdout, report)
}

func runPostgresBindingProbe(ctx context.Context, report api.PostgresBindingProbeReport, databaseURL string, connect postgresBindingProbeConnectFunc) api.PostgresBindingProbeReport {
	probeContext, cancel := context.WithTimeout(ctx, postgresBindingProbeTimeout)
	defer cancel()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		report.Configuration = api.PostgresBindingProbeCheck{Status: "failed", Detail: "database connection URL could not be parsed"}
		report.Connection = api.PostgresBindingProbeCheck{Status: "not_checked"}
		report.Query = api.PostgresBindingProbeCheck{Status: "not_checked"}
		report.Error = "database connection configuration is invalid"
		return report
	}
	report.Configuration = api.PostgresBindingProbeCheck{Status: "passed", Detail: "database connection URL parsed"}
	if connect == nil {
		connect = connectPostgresBindingProbe
	}
	conn, err := connect(probeContext, config)
	if err != nil {
		report.Connection = api.PostgresBindingProbeCheck{Status: "failed", Detail: "database connection failed; verify host, TLS settings, and credentials"}
		report.Query = api.PostgresBindingProbeCheck{Status: "not_checked"}
		report.Error = "database connection failed"
		return report
	}
	report.Connection = api.PostgresBindingProbeCheck{Status: "passed", Detail: "authenticated database connection established"}
	closeContext, cancelClose := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancelClose()
	defer func() { _ = conn.Close(closeContext) }()
	if err := conn.QueryRow(probeContext, "SELECT 1"); err != nil {
		report.Query = api.PostgresBindingProbeCheck{Status: "failed", Detail: "read-only connectivity query failed"}
		report.Error = "read-only connectivity query failed"
		return report
	}
	report.Query = api.PostgresBindingProbeCheck{Status: "passed", Detail: "read-only SELECT 1 succeeded"}
	return report
}

func connectPostgresBindingProbe(ctx context.Context, config *pgx.ConnConfig) (postgresBindingProbeConn, error) {
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return pgxPostgresBindingProbeConn{conn: conn}, nil
}

func writePostgresBindingProbeReport(stdout io.Writer, report api.PostgresBindingProbeReport) (apptaskproto.Result, error) {
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	if report.Passed() {
		exitCode := 0
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exitCode}, nil
	}
	exitCode := 1
	message := report.Error
	if message == "" {
		message = "one or more PostgreSQL binding probe checks failed"
	}
	return apptaskproto.Result{
		Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: "postgres_binding_probe_failed", FailureMessage: message,
	}, nil
}
