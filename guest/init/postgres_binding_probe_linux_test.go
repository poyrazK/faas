//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

type fakePostgresBindingProbeConn struct {
	queryErr error
	queries  []string
	closed   bool
}

func (c *fakePostgresBindingProbeConn) QueryRow(_ context.Context, query string) error {
	c.queries = append(c.queries, query)
	return c.queryErr
}

func (c *fakePostgresBindingProbeConn) Close(context.Context) error {
	c.closed = true
	return nil
}

func TestExecutePostgresBindingProbeCommandRunsOnlySelectOneAndRedactsURL(t *testing.T) {
	databaseURL := "postgres://canary-user:canary-password@db.internal:5432/app?sslmode=require"
	conn := &fakePostgresBindingProbeConn{}
	connectCalls := 0
	connect := func(ctx context.Context, config *pgx.ConnConfig) (postgresBindingProbeConn, error) {
		connectCalls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("connection context has no deadline")
		}
		if config.Host != "db.internal" || config.User != "canary-user" || config.Password != "canary-password" || config.Database != "app" {
			t.Fatalf("parsed database config = host:%q user:%q password:%q database:%q", config.Host, config.User, config.Password, config.Database)
		}
		return conn, nil
	}
	var stdout strings.Builder
	result, err := executePostgresBindingProbeCommandWithConnector(context.Background(), apptaskproto.Request{
		Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"},
	}, api.AppManifest{}, map[string]string{"DATABASE_URL": databaseURL}, nil, &stdout, connect)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded || connectCalls != 1 || !conn.closed {
		t.Fatalf("result=%+v connect calls=%d closed=%v", result, connectCalls, conn.closed)
	}
	if len(conn.queries) != 1 || conn.queries[0] != "SELECT 1" {
		t.Fatalf("queries = %v, want exactly SELECT 1", conn.queries)
	}
	var report api.PostgresBindingProbeReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("decode report: %v; stdout=%s", err, stdout.String())
	}
	if !report.Passed() || report.EnvironmentKey != "DATABASE_URL" {
		t.Fatalf("report = %+v", report)
	}
	if strings.Contains(stdout.String(), "canary-password") || strings.Contains(stdout.String(), "db.internal") || strings.Contains(stdout.String(), "canary-user") {
		t.Fatalf("report exposed connection details: %s", stdout.String())
	}
}

func TestPostgresBindingProbeReportsSafeConfigurationConnectionAndQueryFailures(t *testing.T) {
	const secretURL = "postgres://user:secret-value@private-db.internal:5432/app?sslmode=require"
	tests := []struct {
		name        string
		databaseURL string
		connectErr  error
		queryErr    error
		wantStage   string
		wantError   string
	}{
		{
			name:        "configuration",
			databaseURL: "postgres://user:secret-value@private-db.internal/app?sslmode=invalid",
			wantStage:   "failed", wantError: "database connection configuration is invalid",
		},
		{
			name:        "connection",
			databaseURL: secretURL, connectErr: errors.New(secretURL),
			wantStage: "failed", wantError: "database connection failed",
		},
		{
			name:        "query",
			databaseURL: secretURL, queryErr: errors.New(secretURL),
			wantStage: "failed", wantError: "read-only connectivity query failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &fakePostgresBindingProbeConn{queryErr: tt.queryErr}
			connectCalls := 0
			connect := func(context.Context, *pgx.ConnConfig) (postgresBindingProbeConn, error) {
				connectCalls++
				if tt.connectErr != nil {
					return nil, tt.connectErr
				}
				return conn, nil
			}
			report := runPostgresBindingProbe(context.Background(), api.PostgresBindingProbeReport{
				EnvironmentKey: "DATABASE_URL",
				Environment:    api.PostgresBindingProbeCheck{Status: "passed"},
			}, tt.databaseURL, connect)
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "private-db.internal") {
				t.Fatalf("report exposed URL details: %s", encoded)
			}
			if report.Error != tt.wantError {
				t.Fatalf("report error = %q, want %q", report.Error, tt.wantError)
			}
			switch tt.name {
			case "configuration":
				if report.Configuration.Status != tt.wantStage || report.Connection.Status != "not_checked" || connectCalls != 0 {
					t.Fatalf("report=%+v connect calls=%d", report, connectCalls)
				}
			case "connection":
				if report.Configuration.Status != "passed" || report.Connection.Status != tt.wantStage || report.Query.Status != "not_checked" || connectCalls != 1 {
					t.Fatalf("report=%+v connect calls=%d", report, connectCalls)
				}
			case "query":
				if report.Connection.Status != "passed" || report.Query.Status != tt.wantStage || !conn.closed {
					t.Fatalf("report=%+v closed=%v", report, conn.closed)
				}
			}
		})
	}
}

func TestExecuteAppTaskCommandHandlesMissingPostgresBindingEnvironment(t *testing.T) {
	var stdout strings.Builder
	result, err := executeAppTaskCommand(context.Background(), apptaskproto.Request{
		Command: []string{api.AppTaskPostgresBindingProbeCommand, "GREGALE_TEST_MISSING_DATABASE_URL"},
	}, api.AppManifest{}, nil, nil, &stdout, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusFailed {
		t.Fatalf("result = %+v", result)
	}
	var report api.PostgresBindingProbeReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("decode report: %v; stdout=%s", err, stdout.String())
	}
	if report.Environment.Status != "failed" || report.Configuration.Status != "not_checked" ||
		report.Error != "database connection environment variable is missing" {
		t.Fatalf("report = %+v", report)
	}
}
