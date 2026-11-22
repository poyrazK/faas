package apidserver

// ADR-20261122220000: read-only SQL checks of verification contracts. imaged
// queues them in deployment_verification_queries; apid, which alone holds
// managed Postgres credentials, runs each as the database's agent query login
// (ADR-20261117215858 §2: READ ONLY transaction, short timeouts, capped rows)
// and writes back only the row count and the first value.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

const (
	verificationQueryPoll      = 500 * time.Millisecond
	verificationQueryBatch     = 8
	verificationQueryRetention = time.Hour
	verificationFirstValueMax  = 1024
)

func (s *server) runVerificationQueryWorker(ctx context.Context) {
	queries, ok := s.store.(state.VerificationQueryStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(verificationQueryPoll)
	defer ticker.Stop()
	lastSweep := time.Time{}
	for {
		s.runVerificationQueriesOnce(ctx, queries)
		if time.Since(lastSweep) > time.Minute {
			lastSweep = time.Now()
			if _, err := queries.DeleteExpiredVerificationQueries(ctx, time.Now().Add(-verificationQueryRetention)); err != nil && ctx.Err() == nil {
				s.log.Warn("verification queries: sweep expired rows", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) runVerificationQueriesOnce(ctx context.Context, queries state.VerificationQueryStore) {
	claimed, err := queries.ClaimVerificationQueries(ctx, time.Now(), verificationQueryBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("verification queries: claim", "err", err)
		}
		return
	}
	for _, q := range claimed {
		done := s.runVerificationQuery(ctx, q)
		if err := queries.CompleteVerificationQuery(ctx, done); err != nil && ctx.Err() == nil {
			s.log.Warn("verification queries: complete", "query_id", q.ID, "err", err)
		}
	}
}

func (s *server) runVerificationQuery(ctx context.Context, q state.VerificationQuery) state.VerificationQuery {
	fail := func(code, message string) state.VerificationQuery {
		q.ErrorCode, q.Error = code, message
		return q
	}
	if s.managedPostgres == nil {
		return fail(verifycontract.CodeQueryUnavailable, "managed Postgres is not available")
	}
	queryCtx, cancel := context.WithDeadline(ctx, q.ExpiresAt)
	defer cancel()
	database, err := s.managedPostgres.FindByName(queryCtx, q.AccountID, q.Database)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return fail(verifycontract.CodeDatabaseNotFound, fmt.Sprintf("no managed Postgres database named %q", q.Database))
	}
	if err != nil {
		return fail(verifycontract.CodeQueryUnavailable, "the database could not be looked up")
	}
	// Like the agent query tools, a check reads only a database bound to its
	// own app: whoever may change this app's contract must not reach another
	// app's database through it.
	bound, err := s.databaseBoundToApp(queryCtx, q.AccountID, database.ID, q.AppID)
	if err != nil {
		return fail(verifycontract.CodeQueryUnavailable, "the database's bindings could not be read")
	}
	if !bound {
		return fail(verifycontract.CodeDatabaseNotFound, fmt.Sprintf("no managed Postgres database named %q is bound to this app", q.Database))
	}
	var result managedpostgres.ReadOnlyQueryResult
	for {
		result, err = s.managedPostgres.QueryReadOnly(queryCtx, database, q.Query, managedpostgres.QueryMaxRows)
		if !errors.Is(err, managedpostgres.ErrQueryBusy) {
			break
		}
		// One query per database runs at a time; another (an agent's or a
		// parallel deploy's) is running. Wait for it within the check budget.
		select {
		case <-queryCtx.Done():
			return fail(verifycontract.CodeQueryUnavailable, "another query of the database kept running until the check's budget ran out")
		case <-time.After(200 * time.Millisecond):
		}
	}
	s.audit.Emit(ctx, "postgres.query_executed", &q.AccountID, verificationQueryAudit(database.ID, q, result, err))
	var queryErr *managedpostgres.QueryError
	switch {
	case errors.As(err, &queryErr):
		return fail(verifycontract.CodeQueryFailed, fmt.Sprintf("PostgreSQL refused the query (SQLSTATE %s)", queryErr.SQLState))
	case errors.Is(err, managedpostgres.ErrNotReady):
		return fail(verifycontract.CodeQueryUnavailable, "the database is not ready")
	case errors.Is(err, managedpostgres.ErrUnsupported):
		return fail(verifycontract.CodeQueryUnavailable, "the database does not support read-only queries")
	case err != nil:
		return fail(verifycontract.CodeQueryUnavailable, "the query could not run")
	}
	q.RowCount = result.RowCount
	if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		q.HasFirst = true
		if v := result.Rows[0][0]; v != nil {
			first := *v
			if len(first) > verificationFirstValueMax {
				first = first[:verificationFirstValueMax]
			}
			q.FirstValue = &first
		}
	}
	return q
}

// verificationQueryAudit records the query like the agent query API does,
// by the hash and size of its SQL, never its text or rows.
func verificationQueryAudit(databaseID string, q state.VerificationQuery, result managedpostgres.ReadOnlyQueryResult, err error) map[string]any {
	audit := managedPostgresQueryAudit(databaseID, q.Query, result, err)
	audit["source"] = "verification_contract"
	audit["app_id"] = q.AppID
	audit["deployment_id"] = q.DeploymentID
	return audit
}

// databaseBoundToApp reports whether the app holds a live binding to the
// database.
func (s *server) databaseBoundToApp(ctx context.Context, accountID, databaseID, appID string) (bool, error) {
	if s.managedPostgresBindings == nil {
		return false, nil
	}
	bindings, err := s.managedPostgresBindings.List(ctx, accountID, databaseID)
	if err != nil {
		return false, err
	}
	for _, b := range bindings {
		if b.AppID != appID {
			continue
		}
		switch b.State {
		case managedpostgres.BindingStateDeleting, managedpostgres.BindingStateRetiring, managedpostgres.BindingStateDeleted, managedpostgres.BindingStateFailed:
			continue
		}
		return true, nil
	}
	return false, nil
}
