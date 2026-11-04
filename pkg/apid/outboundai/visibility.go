package outboundai

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Integrations are account-scoped, and company apps share their creator's
// AccountID, so an integration's logged calls include company apps' calls.
// A creator removed from a company keeps the integration but must not keep
// reading, rating or replaying those calls: the readers below withhold every
// call of an app the caller may no longer see.

// visibleCallers reports which callers the reader may see. A caller is an
// app ID or a named Gregale caller (runs, replay, ...), which is the
// account's own. An app that no longer exists leaves no company to answer
// to, so its calls stay with the integration's account.
func (s *Service) visibleCallers(r *http.Request, acct state.Account, callers []string) (map[string]bool, error) {
	actor := acct
	if a, _, ok := authmw.AccountFromContext(r); ok {
		actor = a
	}
	visible := make(map[string]bool, len(callers))
	for _, caller := range callers {
		if _, seen := visible[caller]; seen {
			continue
		}
		if _, err := uuid.Parse(caller); err != nil {
			visible[caller] = true
			continue
		}
		app, err := s.Host.Store().AppByID(r.Context(), caller)
		if errors.Is(err, state.ErrNotFound) {
			visible[caller] = true
			continue
		}
		if err != nil {
			return nil, err
		}
		apps, err := appaccess.VisibleApps(r, s.Host.Store(), actor, []state.App{app})
		if err != nil {
			return nil, err
		}
		visible[caller] = len(apps) == 1
	}
	return visible, nil
}

// contentReadable reports whether the reader may read captured prompts and
// answers: the content route's admin or secrets:read scopes. Replays and
// evaluations carry answer text, so they withhold it from other readers
// (ADR 20261027132936 amendment 1).
func (s *Service) contentReadable(r *http.Request) bool {
	if s.ContentReadable != nil {
		return s.ContentReadable(r)
	}
	return authmw.HasScope(r, api.ScopesAIContentReadSurface...)
}

// visibleEvaluationItems drops the samples of calls whose app the reader may
// no longer see, as the request log does. A sample whose call has left the
// log keeps no caller and stays with the account.
func (s *Service) visibleEvaluationItems(r *http.Request, acct state.Account, items []state.OutboundAIEvaluationItem) ([]state.OutboundAIEvaluationItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	store, ok := s.Host.Store().(state.OutboundAIRequestCallersStore)
	if !ok {
		return nil, errors.New("outbound AI request callers are unavailable")
	}
	logIDs := make([]string, 0, len(items))
	for _, item := range items {
		logIDs = append(logIDs, item.LogID)
	}
	byLog, err := store.OutboundAIRequestCallers(r.Context(), acct.ID, logIDs)
	if err != nil {
		return nil, err
	}
	callers := make([]string, 0, len(byLog))
	for _, caller := range byLog {
		callers = append(callers, caller)
	}
	visible, err := s.visibleCallers(r, acct, callers)
	if err != nil {
		return nil, err
	}
	kept := make([]state.OutboundAIEvaluationItem, 0, len(items))
	for _, item := range items {
		if caller, logged := byLog[item.LogID]; !logged || visible[caller] {
			kept = append(kept, item)
		}
	}
	return kept, nil
}

// requestVisible checks one logged call before a handler reads, rates or
// replays it. A withheld call reads as notFound; on false the problem is
// written. A call missing from the log is left to the handler's own read.
func (s *Service) requestVisible(w http.ResponseWriter, r *http.Request, acct state.Account, integrationID, requestID, notFound string) bool {
	store, ok := s.Host.Store().(state.OutboundAIRequestCallerStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI calls are unavailable")
		return false
	}
	caller, err := store.OutboundAIRequestCaller(r.Context(), acct.ID, integrationID, requestID)
	if errors.Is(err, state.ErrNotFound) {
		return true
	}
	var visible map[string]bool
	if err == nil {
		visible, err = s.visibleCallers(r, acct, []string{caller})
	}
	switch {
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI calls are unavailable")
		return false
	case !visible[caller]:
		httpkit.NotFound(w, notFound)
		return false
	}
	return true
}
