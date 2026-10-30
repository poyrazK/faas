package apidserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// idempotentInRequestedEnvironment scopes Idempotency-Key receipts to the
// environment a JSON body names. Requests without one keep the legacy
// method/path receipt; a named environment uses its owned ID, so a stage
// request never replays a production receipt or stores one production replays.
// The body is buffered up to maxBytes and handed back unchanged; the handler
// still enforces its own decode limits.
func (s *server) idempotentInRequestedEnvironment(maxBytes func(state.Account) int64, next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if r.Header.Get("Idempotency-Key") == "" || r.Body == nil {
			next(w, r, acct)
			return
		}
		limit := maxBytes(acct)
		if limit <= 0 {
			limit = httpkit.DefaultJSONBodyMaxBytes
		}
		buffered, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(buffered), r.Body), r.Body}
		var selector struct {
			Environment string `json:"environment"`
		}
		// Production, named or not, keeps the legacy receipt, so a retry that
		// adds or drops "environment":"production" still replays.
		if err != nil || int64(len(buffered)) > limit || json.Unmarshal(buffered, &selector) != nil || state.IsProductionScope(selector.Environment) {
			s.idempotent(next)(w, r, acct)
			return
		}
		app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		// An unregistered name is refused by the handler; it still never shares
		// the production receipt. Any other lookup failure refuses the request,
		// so a retry cannot land under a second receipt.
		scope := "requested " + selector.Environment
		if app.ProjectID != "" {
			env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, app.ProjectID, selector.Environment)
			switch {
			case err == nil:
				scope = env.ID
			case !errors.Is(err, state.ErrNotFound):
				api.WriteProblem(w, api.ErrCapacity("could not resolve the requested environment"))
				return
			}
		}
		s.idempotentInEnvironment(scope, next)(w, r, acct)
	}
}

func queueSendBodyLimit(acct state.Account) int64 {
	return int64(api.MustLimitsFor(acct.Plan).MaxSourceBytesPerInvocation)
}

func defaultJSONBodyLimit(state.Account) int64 { return httpkit.DefaultJSONBodyMaxBytes }
