package apidserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// queryManagedPostgresDatabase runs one read-only statement as the
// database's agent query login (ADR-20261117215858 §2). The read-only login
// and READ ONLY transaction are the boundary; the scope, one query per
// database at a time and the audit record are the controls. Neither the SQL
// text nor the rows are logged.
func (s *server) queryManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, managedpostgres.QueryMaxSQLBytes+4096)
	var req api.ManagedPostgresQueryRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := managedpostgres.ValidateQuerySQL(req.SQL); err != nil {
		api.WriteProblem(w, api.ErrValidation("sql must be 1 to 65536 bytes without NUL"))
		return
	}
	if req.MaxRows < 0 || req.MaxRows > managedpostgres.QueryMaxRows {
		api.WriteProblem(w, api.ErrValidation("max_rows must be from 1 to 1000"))
		return
	}
	database, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	result, err := s.managedPostgres.QueryReadOnly(r.Context(), database, req.SQL, req.MaxRows)
	sum := sha256.Sum256([]byte(req.SQL))
	audit := map[string]any{"database_id": database.ID, "sql_sha256": hex.EncodeToString(sum[:]), "sql_bytes": len(req.SQL), "row_count": result.RowCount, "truncated": result.Truncated, "outcome": "ok"}
	var queryErr *managedpostgres.QueryError
	switch {
	case errors.As(err, &queryErr):
		audit["outcome"], audit["sqlstate"] = "query_failed", queryErr.SQLState
	case err != nil:
		audit["outcome"] = "error"
	}
	s.audit.Emit(r.Context(), "postgres.query_executed", &acct.ID, audit)
	switch {
	case queryErr != nil:
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodePostgresQueryFailed, "Query failed", queryErr.Message+" (SQLSTATE "+queryErr.SQLState+")").
			WithHint("The statement runs read-only as a login that can read the public schema, for at most 5 seconds. Fix the statement and try again."))
		return
	case errors.Is(err, managedpostgres.ErrUnsupported):
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodePostgresQueryUnsupported, "Queries unsupported", "this database's backend cannot run read-only queries"))
		return
	case err != nil:
		managedPostgresProblem(w, err)
		return
	}
	columns := make([]api.ManagedPostgresQueryColumn, 0, len(result.Columns))
	for _, c := range result.Columns {
		columns = append(columns, api.ManagedPostgresQueryColumn{Name: c.Name, Type: c.Type})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.ManagedPostgresQueryResponse{DatabaseID: database.ID, Columns: columns, Rows: result.Rows, RowCount: result.RowCount, Truncated: result.Truncated, DurationMS: result.Duration.Milliseconds()})
}
