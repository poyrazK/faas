package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/customererrors"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
)

// configureCustomerErrors starts the recorder of control-plane problems
// returned to customers (ADR-20261112170000). Without a pool the recorder
// is nil and records nothing.
func (s *server) configureCustomerErrors(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	s.customerErrors = customererrors.New(pool, log)
	if s.customerErrors != nil {
		s.customerErrorSink = s.customerErrors.Record
		go s.customerErrors.Run(ctx)
	}
}

// recordCustomerErrors is the outermost account-handler layer of the
// authenticated /v1 chains. After the handler returns it reads the problem
// code api.WriteProblem stamped on the response headers and records it for
// the account. It does not wrap the ResponseWriter, so streaming, hijacking
// and HTML problem rendering behave exactly as before. Operator routes are
// not recorded: they are not customer traffic.
func (s *server) recordCustomerErrors(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if s.customerErrorSink == nil || strings.Contains(r.Pattern, " /v1/admin/") {
			next(w, r, acct)
			return
		}
		preset := w.Header().Get(api.ErrorCodeHeader) != ""
		next(w, r, acct)
		if preset {
			return
		}
		code := w.Header().Get(api.ErrorCodeHeader)
		if code == "" {
			return
		}
		event := customererrors.Event{
			AccountID: acct.ID,
			Route:     r.Pattern,
			Code:      code,
			AppSlug:   r.PathValue("slug"),
			RequestID: r.Header.Get(api.RequestIDHeader),
		}
		if spanContext := trace.SpanFromContext(r.Context()).SpanContext(); spanContext.IsValid() {
			event.TraceID = spanContext.TraceID().String()
		}
		s.customerErrorSink(event)
	}
}
