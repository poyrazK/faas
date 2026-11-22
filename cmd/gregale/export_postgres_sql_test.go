// Exports for the external `postgres query` tests (see export_test.go).

package main

import (
	"context"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

type (
	PostgresSQLResult = postgresSQLResult
	PostgresSQLOutput = postgresSQLOutput
)

const PostgresSQLQueryLimit = postgresSQLQueryLimit

var (
	PostgresSQLQueryText     = postgresSQLQueryText
	PostgresSQLJSONValue     = postgresSQLJSONValue
	PostgresSQLColumnKeys    = postgresSQLColumnKeys
	PreparePostgresSQLConfig = preparePostgresSQLConfig
	PostgresSQLReadOnlyOrder = postgresSQLReadOnlyOrder
	PostgresSQLWriteOrder    = postgresSQLWriteOrder
)

func ChoosePostgresBinding(bindings []api.ManagedPostgresBinding, apps []api.AppResponse, order []string) (api.ManagedPostgresBinding, error) {
	binding, _, err := choosePostgresBinding(bindings, apps, "", "", "", order)
	return binding, err
}

func ChoosePostgresConnectBindingDefault(bindings []api.ManagedPostgresBinding, apps []api.AppResponse) (api.ManagedPostgresBinding, error) {
	binding, _, err := choosePostgresConnectBinding(bindings, apps, "", "", "")
	return binding, err
}

func postgresSQLTestOptions(write bool, maxRows int, timeout time.Duration) postgresSQLOptions {
	return postgresSQLOptions{write: write, maxRows: maxRows, timeout: timeout}
}

// PostgresSQLConfig is the pgx config `postgres query` builds for a binding
// URL dialled through a tunnel at hostAddr:port.
func PostgresSQLConfig(dsn, hostAddr string, port int) (*pgx.ConnConfig, error) {
	target, err := parsePostgresConnectTarget(dsn)
	if err != nil {
		return nil, err
	}
	return target.pgxConfig(hostAddr, port, postgresSQLTestOptions(false, 1, time.Second))
}

func PostgresSQLError(err error, write bool, timeout time.Duration) error {
	return postgresSQLError(err, postgresSQLTestOptions(write, 1, timeout))
}

func RunPostgresSQL(ctx context.Context, conn *pgx.Conn, query string, write bool, maxRows int, timeout time.Duration) ([]PostgresSQLResult, error) {
	return runPostgresSQL(ctx, conn, query, postgresSQLTestOptions(write, maxRows, timeout))
}

func NewPostgresSQLResult(typeMap *pgtype.Map, fields []pgconn.FieldDescription, raw [][][]byte, command string, truncated bool) PostgresSQLResult {
	return newPostgresSQLResult(typeMap, fields, raw, command, truncated)
}

func RenderPostgresSQLResults(w io.Writer, out PostgresSQLOutput) {
	renderPostgresSQLResults(w, out)
}
