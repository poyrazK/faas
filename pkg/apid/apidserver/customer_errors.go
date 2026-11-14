package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/customererrors"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
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
		if note := middleware.AccountNoteFrom(r.Context()); note != nil {
			note.Reached = true
		}
		if strings.Contains(r.Pattern, " /v1/admin/") {
			next(w, r, acct)
			return
		}
		preset := w.Header().Get(api.ErrorCodeHeader) != ""
		next(w, r, acct)
		if !preset {
			s.recordCustomerError(r, acct.ID, w.Header().Get(api.ErrorCodeHeader))
		}
	}
}

// recordCredentialRefusals wraps an authenticated /v1 chain outside the auth
// middleware (ADR-20261114090000). The middleware notes the account a
// credential resolved to; when it then refuses the request itself (a revoked
// or expired key, a suspended account, a key limited to other projects), the
// request never reaches recordCustomerErrors, so this layer records the
// refusal for the noted account. A credential that names no account (an
// unknown key, no credentials) is still not recorded.
func (s *server) recordCredentialRefusals(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Pattern, " /v1/admin/") {
			next(w, r)
			return
		}
		note := &middleware.AccountNote{}
		// Mutate the request in place: the auth middleware stamps the
		// principal through this pointer for the layers outside it
		// (pkg/auth's pointer-mutation contract), so it must stay the
		// request those layers hold.
		*r = *r.WithContext(middleware.WithAccountNote(r.Context(), note))
		preset := w.Header().Get(api.ErrorCodeHeader) != ""
		next(w, r)
		if preset || note.Reached || note.AccountID == "" {
			return
		}
		s.recordCustomerError(r, note.AccountID, w.Header().Get(api.ErrorCodeHeader))
	}
}

// recordCustomerError counts code and hands it to the recorder for
// accountID. Codes that are not well-formed problem codes are ignored.
func (s *server) recordCustomerError(r *http.Request, accountID, code string) {
	if !customererrors.ValidCode(code) {
		return
	}
	// ADR-20261113123000: the per-code counter needs no database.
	s.countCustomerError(code)
	if s.customerErrorSink == nil {
		return
	}
	event := customererrors.Event{
		AccountID: accountID,
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
