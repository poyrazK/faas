package managedpostgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read-only queries (ADR-20261117215858 §2). apid runs one customer
// statement as the database's agent query login: a read_only credential with
// a fixed identity, so it carries exactly the privilege contract read-only
// bindings verify. The statement runs in a READ ONLY transaction with short
// timeouts, its rows are capped, and the transaction is rolled back.

// AgentQueryIdentity is the stable identity of every database's agent query
// login. Each query gives it a new password that only apid holds.
const AgentQueryIdentity = "gregale-agent-query"

// Query limits. The handler validates requests against them before a
// provider is called; providers enforce the row and byte caps while reading.
const (
	QueryMaxSQLBytes       = 64 * 1024
	QueryDefaultRows       = 100
	QueryMaxRows           = 1000
	QueryMaxValueBytes     = 1 << 20
	QueryStatementTimeout  = 5 * time.Second
	QueryLockTimeout       = time.Second
	QueryIdleInTransaction = 10 * time.Second
	// queryCallTimeout bounds the whole call: issuing the login, waking a
	// suspended compute, connecting and the statement.
	queryCallTimeout = 30 * time.Second
)

// ReadOnlyQueryRequest is one statement against one database.
type ReadOnlyQueryRequest struct {
	ProviderResourceID string
	SQL                string
	MaxRows            int
	MaxBytes           int
}

// QueryColumn is one result column: its name and PostgreSQL type name.
type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ReadOnlyQueryResult holds the rows read, each value in its PostgreSQL text
// form or nil for NULL. Truncated is true when the row or byte cap stopped
// reading before the result ended.
type ReadOnlyQueryResult struct {
	Columns   []QueryColumn `json:"columns"`
	Rows      [][]*string   `json:"rows"`
	RowCount  int           `json:"row_count"`
	Truncated bool          `json:"truncated"`
	Duration  time.Duration `json:"-"`
}

// QueryError is an error PostgreSQL reported for the statement: a syntax
// error, a missing privilege, a write refused by the read-only transaction
// or a timeout. It is safe to show the caller; connection and credential
// failures are never QueryErrors.
type QueryError struct {
	SQLState string
	Message  string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("query failed: %s (SQLSTATE %s)", e.Message, e.SQLState)
}

// ReadOnlyQuerier is a provider that runs read-only statements as a
// database's agent query login.
type ReadOnlyQuerier interface {
	QueryReadOnly(context.Context, ReadOnlyQueryRequest) (ReadOnlyQueryResult, error)
}

// DataAPICatalogPreviewer supports the fixed metadata-only inspection without
// enabling arbitrary SQL queries on a backend.
type DataAPICatalogPreviewer interface {
	PreviewDataAPICatalog(context.Context, string) (ReadOnlyQueryResult, error)
}

func (s *Service) PreviewDataAPICatalog(ctx context.Context, database Database) (ReadOnlyQueryResult, error) {
	if database.State != StateReady || database.ProviderResourceID == "" {
		return ReadOnlyQueryResult{}, ErrNotReady
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ReadOnlyQueryResult{}, err
	}
	if backend.Capabilities.SupportsCredentialAccess(CredentialReadOnly) != nil {
		return ReadOnlyQueryResult{}, ErrUnsupported
	}
	previewer, previewOK := backend.Provider.(DataAPICatalogPreviewer)
	query, queryOK := backend.Provider.(ReadOnlyQuerier)
	if !previewOK && !queryOK {
		return ReadOnlyQueryResult{}, ErrUnsupported
	}
	if _, busy := s.queriesInFlight.LoadOrStore(database.ID, struct{}{}); busy {
		return ReadOnlyQueryResult{}, ErrQueryBusy
	}
	defer s.queriesInFlight.Delete(database.ID)
	callCtx, cancel := context.WithTimeout(ctx, queryCallTimeout)
	defer cancel()
	started := time.Now()
	var result ReadOnlyQueryResult
	if previewOK {
		result, err = previewer.PreviewDataAPICatalog(callCtx, database.ProviderResourceID)
	} else {
		result, err = query.QueryReadOnly(callCtx, ReadOnlyQueryRequest{ProviderResourceID: database.ProviderResourceID, SQL: api.DataAPICatalogPreviewSQL, MaxRows: QueryMaxRows, MaxBytes: QueryMaxValueBytes})
	}
	result.Duration = time.Since(started)
	return result, err
}

var (
	// ErrQueryBusy means another query of the same database is running.
	ErrQueryBusy = fmt.Errorf("%w: a query of this database is already running; retry when it ends", ErrConflict)
	// ErrNotReady means the database is not ready to accept queries.
	ErrNotReady = fmt.Errorf("%w: the database is not ready", ErrConflict)
)

// ValidateQuerySQL checks a statement before it reaches a database: present,
// bounded, valid text without NUL. The read-only login and transaction are
// the boundary; this only refuses input no database should see.
func ValidateQuerySQL(sql string) error {
	if strings.TrimSpace(sql) == "" || len(sql) > QueryMaxSQLBytes || strings.ContainsRune(sql, 0) {
		return fmt.Errorf("sql must be 1 to %d bytes without NUL: %w", QueryMaxSQLBytes, ErrInvalid)
	}
	return nil
}

// QueryReadOnly runs one statement against a ready database. It returns
// ErrUnsupported when the database's backend cannot run queries, and
// ErrQueryBusy while another query of the database runs.
func (s *Service) QueryReadOnly(ctx context.Context, database Database, sql string, maxRows int) (ReadOnlyQueryResult, error) {
	if err := ValidateQuerySQL(sql); err != nil {
		return ReadOnlyQueryResult{}, err
	}
	if maxRows == 0 {
		maxRows = QueryDefaultRows
	}
	if maxRows < 1 || maxRows > QueryMaxRows {
		return ReadOnlyQueryResult{}, fmt.Errorf("max_rows must be from 1 to %d: %w", QueryMaxRows, ErrInvalid)
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return ReadOnlyQueryResult{}, ErrNotReady
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ReadOnlyQueryResult{}, err
	}
	querier, ok := backend.Provider.(ReadOnlyQuerier)
	if !ok || backend.Capabilities.SupportsCredentialAccess(CredentialReadOnly) != nil {
		return ReadOnlyQueryResult{}, ErrUnsupported
	}
	if _, busy := s.queriesInFlight.LoadOrStore(database.ID, struct{}{}); busy {
		return ReadOnlyQueryResult{}, ErrQueryBusy
	}
	defer s.queriesInFlight.Delete(database.ID)
	callCtx, cancel := context.WithTimeout(ctx, queryCallTimeout)
	defer cancel()
	started := time.Now()
	result, err := querier.QueryReadOnly(callCtx, ReadOnlyQueryRequest{ProviderResourceID: database.ProviderResourceID, SQL: sql, MaxRows: maxRows, MaxBytes: QueryMaxValueBytes})
	result.Duration = time.Since(started)
	return result, err
}
