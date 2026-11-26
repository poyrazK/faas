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
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

// verificationQueryPoll is how often an idle worker looks for queued checks.
const verificationQueryPoll = 500 * time.Millisecond

func (s *server) runVerificationQueryWorker(ctx context.Context) {
	queries, ok := s.store.(state.VerificationQueryStore)
	if !ok {
		return
	}
	// slots bounds the checks this apid runs at once; a slow check never
	// holds back the others.
	slots := make(chan struct{}, api.VerificationQueryWorkers)
	ticker := time.NewTicker(verificationQueryPoll)
	defer ticker.Stop()
	lastSweep := time.Time{}
	for {
		if free := cap(slots) - len(slots); free > 0 {
			s.startVerificationQueries(ctx, queries, free, slots, nil)
		}
		if time.Since(lastSweep) > time.Minute {
			lastSweep = time.Now()
			if _, err := queries.DeleteExpiredVerificationQueries(ctx, time.Now().Add(-api.VerificationQueryRetention)); err != nil && ctx.Err() == nil {
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

// runVerificationQueriesOnce claims and runs queued checks and waits for
// them; tests use it.
func (s *server) runVerificationQueriesOnce(ctx context.Context, queries state.VerificationQueryStore) {
	var wg sync.WaitGroup
	s.startVerificationQueries(ctx, queries, api.VerificationQueryWorkers, make(chan struct{}, api.VerificationQueryWorkers), &wg)
	wg.Wait()
}

func (s *server) startVerificationQueries(ctx context.Context, queries state.VerificationQueryStore, limit int, slots chan struct{}, wg *sync.WaitGroup) {
	claimed, err := queries.ClaimVerificationQueries(ctx, time.Now(), limit)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("verification queries: claim", "err", err)
		}
		return
	}
	for _, q := range claimed {
		slots <- struct{}{}
		if wg != nil {
			wg.Add(1)
		}
		go func() {
			defer func() { <-slots }()
			if wg != nil {
				defer wg.Done()
			}
			s.completeVerificationQuery(ctx, queries, s.runVerificationQuery(ctx, q))
		}()
	}
}

// completeVerificationQuery records a result. When the result cannot be
// written it records a coded error instead, so imaged gets a verdict rather
// than polling a row that stays running.
func (s *server) completeVerificationQuery(ctx context.Context, queries state.VerificationQueryStore, done state.VerificationQuery) {
	err := queries.CompleteVerificationQuery(ctx, done)
	if err == nil || ctx.Err() != nil || errors.Is(err, state.ErrNotFound) {
		return
	}
	s.log.Warn("verification queries: complete", "query_id", done.ID, "err", err)
	failed := state.VerificationQuery{ID: done.ID, ErrorCode: verifycontract.CodeQueryUnavailable, Error: "the query result could not be recorded"}
	if err := queries.CompleteVerificationQuery(ctx, failed); err != nil && ctx.Err() == nil {
		s.log.Warn("verification queries: record failure", "query_id", done.ID, "err", err)
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
	busySince := time.Now()
	for {
		result, err = s.managedPostgres.QueryReadOnly(queryCtx, database, q.Query, managedpostgres.QueryMaxRows)
		if !errors.Is(err, managedpostgres.ErrQueryBusy) {
			break
		}
		// One query per database runs at a time; another (an agent's or a
		// parallel deploy's) is running. Wait briefly, so a claim never
		// outlives api.VerificationQueryStaleClaim.
		if time.Since(busySince) > api.VerificationQueryBusyWait {
			return fail(verifycontract.CodeQueryUnavailable, "another query of the database was still running")
		}
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
			if len(first) > api.VerificationFirstValueMaxBytes {
				// Cut on a character boundary: a split UTF-8 sequence is
				// text PostgreSQL refuses to store.
				first = strings.ToValidUTF8(first[:api.VerificationFirstValueMaxBytes], "")
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
