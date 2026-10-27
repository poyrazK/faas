package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountDeadLetterAppSlugs resolves the optional app identity carried by an
// account-wide event. Account-owned job runs intentionally have no app_id, so
// those rows simply retain an empty app_slug in the wire response.
func (s *server) accountDeadLetterAppSlugs(r *http.Request, acct state.Account) (map[string]string, error) {
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(apps))
	for _, app := range apps {
		out[app.ID] = app.Slug
	}
	return out, nil
}

func accountDeadLetterEventResponse(ev state.DeadLetterEvent, appSlugs map[string]string) api.DeadLetterEvent {
	out := deadLetterEventResponse(ev)
	if ev.AppID != "" {
		out.AppSlug = appSlugs[ev.AppID]
	}
	return out
}

func accountDeadLetterNotFound(id string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Dead-letter event not found", "no dead-letter event with id "+strconv.Quote(id)+" belongs to this account.")
}

// deadLetterAccess decides, once per app and request, whether the account
// view may show an app's dead letters. Rows are keyed by the creating
// account, so it applies the account inventories' rule (appaccess.VisibleApps):
// a company app is hidden from a creator who left the company and from a key
// bound to another one. Account-owned job events carry no app, and an app
// whose row is gone keeps the account scope it had.
type deadLetterAccess struct {
	s     *server
	acct  state.Account
	actor state.Account
	seen  map[string]bool
}

func (s *server) deadLetterAccess(r *http.Request, acct state.Account) *deadLetterAccess {
	return &deadLetterAccess{s: s, acct: acct, actor: actorAccount(r, acct), seen: map[string]bool{"": true}}
}

// visible takes the request's context, which carries the principal whose
// key binding appaccess checks.
func (a *deadLetterAccess) visible(ctx context.Context, appID string) (bool, error) {
	if v, ok := a.seen[appID]; ok {
		return v, nil
	}
	app, err := a.s.store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		a.seen[appID] = true
		return true, nil
	}
	if err != nil {
		return false, err
	}
	shown, err := appaccess.VisibleAppsContext(ctx, a.s.store, a.actor, []state.App{app})
	if err != nil {
		return false, err
	}
	a.seen[appID] = len(shown) == 1
	return a.seen[appID], nil
}

// visibleEvent reads one account event and refuses one this principal may not
// see with ErrNotFound, the same answer as another account's event.
func (a *deadLetterAccess) visibleEvent(ctx context.Context, eventID string) (state.DeadLetterEvent, error) {
	ev, err := a.s.store.DeadLetterEventByAccountID(ctx, a.acct.ID, eventID)
	if err != nil {
		return state.DeadLetterEvent{}, err
	}
	if ok, err := a.visible(ctx, ev.AppID); err != nil || !ok {
		if err == nil {
			err = state.ErrNotFound
		}
		return state.DeadLetterEvent{}, err
	}
	return ev, nil
}

// visibleEvents pages the account's dead letters and returns up to limit this
// principal may see (only ones replay can redrive when replayable is set).
// Collecting before acting keeps the keyset cursor on rows a purge has not
// removed.
func (a *deadLetterAccess) visibleEvents(ctx context.Context, limit int, replayable bool) ([]state.DeadLetterEvent, error) {
	var out []state.DeadLetterEvent
	before := ""
	for range deadLetterAccountScanPages {
		page, err := a.s.store.ListDeadLetterEventsForAccount(ctx, a.acct.ID, deadLetterEventsMaxLimit, before)
		if err != nil {
			return nil, err
		}
		for _, ev := range page {
			if replayable && (ev.ReplayedAt != nil || !state.DeadLetterReplayable(ev.Source)) {
				continue
			}
			if ok, err := a.visible(ctx, ev.AppID); err != nil {
				return nil, err
			} else if ok {
				if out = append(out, ev); len(out) == limit {
					return out, nil
				}
			}
		}
		if len(page) < deadLetterEventsMaxLimit {
			return out, nil
		}
		before = page[len(page)-1].ID
	}
	return out, nil
}

// deadLetterAccountScanPages bounds how far a batch looks past events it may
// not touch.
const deadLetterAccountScanPages = 50

func parseAccountDeadLetterLimit(w http.ResponseWriter, r *http.Request, operation string) (int, bool) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > deadLetterEventsMaxLimit {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest,
				api.CodeBadRequest, "Invalid dead-letter "+operation+" limit",
				"limit must be between 1 and 200"))
			return 0, false
		}
		limit = n
	}
	return limit, true
}

func (s *server) listAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "list")
	if !ok {
		return
	}
	events, err := s.store.ListDeadLetterEventsForAccount(r.Context(), acct.ID, limit, r.URL.Query().Get("before"))
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter events"))
		return
	}
	appSlugs, err := s.accountDeadLetterAppSlugs(r, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter apps"))
		return
	}
	access := s.deadLetterAccess(r, acct)
	out := api.AccountDeadLetterEventsResponse{Events: make([]api.DeadLetterEvent, 0, len(events))}
	for _, ev := range events {
		if ok, err := access.visible(r.Context(), ev.AppID); err != nil {
			api.WriteProblem(w, api.ErrInternal("account dead-letter app access"))
			return
		} else if ok {
			out.Events = append(out.Events, accountDeadLetterEventResponse(ev, appSlugs))
		}
	}
	// The cursor follows the page read, so hidden rows never end paging.
	if len(events) == limit && len(events) > 0 {
		out.NextBefore = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	ev, err := s.deadLetterAccess(r, acct).visibleEvent(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		api.WriteProblem(w, api.ErrInternal("account dead-letter event"))
		return
	}
	appSlugs, err := s.accountDeadLetterAppSlugs(r, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter apps"))
		return
	}
	writeJSON(w, http.StatusOK, accountDeadLetterEventResponse(ev, appSlugs))
}

func (s *server) replayAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	_, err := s.deadLetterAccess(r, acct).visibleEvent(r.Context(), eventID)
	var ev state.DeadLetterEvent
	if err == nil {
		ev, err = s.store.ReplayDeadLetterEventForAccount(r.Context(), acct.ID, eventID)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQReplay("account", "not_found")
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		s.ops.ObserveDLQReplay("account", deadLetterReplayFailure(err))
		api.WriteProblem(w, appevents.EventDeliveryReplayProblem(err, "account dead-letter replay"))
		return
	}
	s.ops.ObserveDLQReplay("account", "success")
	s.audit.Emit(r.Context(), "account.dlq.event_replayed", &acct.ID, map[string]any{
		"event_id": ev.ID, "source": ev.Source, "source_id": ev.SourceID,
		"app_id": ev.AppID, "account_scope": true,
	})
	appSlugs, _ := s.accountDeadLetterAppSlugs(r, acct)
	writeJSON(w, http.StatusAccepted, accountDeadLetterEventResponse(ev, appSlugs))
}

func (s *server) replayAllAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "replay")
	if !ok {
		return
	}
	replayed, err := s.replayVisibleAccountDeadLetters(r, acct, limit)
	if err != nil {
		s.ops.ObserveDLQReplay("account", "error")
		api.WriteProblem(w, appevents.EventDeliveryReplayProblem(err, "account dead-letter replay"))
		return
	}
	for i := 0; i < replayed; i++ {
		s.ops.ObserveDLQReplay("account", "success")
	}
	if replayed > 0 {
		s.audit.Emit(r.Context(), "account.dlq.event_replayed", &acct.ID, map[string]any{
			"count": replayed, "operation": "batch", "account_scope": true,
		})
	}
	writeJSON(w, http.StatusAccepted, api.AccountDeadLetterReplayAllResponse{Replayed: replayed})
}

func (s *server) deleteAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	_, err := s.deadLetterAccess(r, acct).visibleEvent(r.Context(), eventID)
	if err == nil {
		err = s.store.DeleteDeadLetterEventForAccount(r.Context(), acct.ID, eventID)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQPurge("account", "not_found")
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		s.ops.ObserveDLQPurge("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter purge"))
		return
	}
	s.ops.ObserveDLQPurge("account", "success")
	s.audit.Emit(r.Context(), "account.dlq.purged", &acct.ID, map[string]any{
		"event_id": eventID, "count": 1, "account_scope": true,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) purgeAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "purge")
	if !ok {
		return
	}
	purged, err := s.purgeVisibleAccountDeadLetters(r, acct, limit)
	if err != nil {
		s.ops.ObserveDLQPurge("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter purge"))
		return
	}
	for i := 0; i < purged; i++ {
		s.ops.ObserveDLQPurge("account", "success")
	}
	if purged > 0 {
		s.audit.Emit(r.Context(), "account.dlq.purged", &acct.ID, map[string]any{
			"count": purged, "operation": "batch", "account_scope": true,
		})
	}
	writeJSON(w, http.StatusOK, api.AccountDeadLetterPurgeResponse{Purged: purged})
}

// replayVisibleAccountDeadLetters replays up to limit unreplayed events this
// principal may see, one at a time. A row another request replayed or purged
// meanwhile is skipped.
func (s *server) replayVisibleAccountDeadLetters(r *http.Request, acct state.Account, limit int) (int, error) {
	events, err := s.deadLetterAccess(r, acct).visibleEvents(r.Context(), limit, true)
	if err != nil {
		return 0, err
	}
	replayed := 0
	for _, ev := range events {
		if _, err := s.store.ReplayDeadLetterEventForAccount(r.Context(), acct.ID, ev.ID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			return replayed, err
		}
		replayed++
	}
	return replayed, nil
}

// purgeVisibleAccountDeadLetters purges up to limit events this principal may
// see, one at a time.
func (s *server) purgeVisibleAccountDeadLetters(r *http.Request, acct state.Account, limit int) (int, error) {
	events, err := s.deadLetterAccess(r, acct).visibleEvents(r.Context(), limit, false)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, ev := range events {
		if err := s.store.DeleteDeadLetterEventForAccount(r.Context(), acct.ID, ev.ID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			return purged, err
		}
		purged++
	}
	return purged, nil
}
