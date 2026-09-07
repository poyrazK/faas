package gateway

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
)

const wakePageHeader = "x-faas-wake-page"

// wakePageVisit is retained until the detached wake has a scheduler-issued
// ID. Keeping the request ID lets the wake timeline point back to the exact
// browser request without copying URL/query data into the event payload.
type wakePageVisit struct {
	requestID string
	accountID string
	servedAt  time.Time
}

type wakePageCycle struct {
	wakeID  string
	pending []wakePageVisit
}

// acceptsWakePage limits the retry document to browser-like navigations.
// Accept is the compatibility fallback because older browsers and
// non-browser clients may not send Fetch Metadata headers. When those
// headers are present, an explicit fetch/XHR request must not receive an
// HTML interstitial merely because it also advertises text/html.
func acceptsWakePage(r *http.Request) bool {
	if r == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) || !api.AcceptsHTML(r) {
		return false
	}
	if mode := strings.TrimSpace(strings.ToLower(r.Header.Get("Sec-Fetch-Mode"))); mode != "" && mode != "navigate" {
		return false
	}
	if dest := strings.TrimSpace(strings.ToLower(r.Header.Get("Sec-Fetch-Dest"))); dest != "" && dest != "document" {
		return false
	}
	return true
}

// writeWakePage renders the browser-only 200 response used while a detached
// wake is still booting. The page deliberately contains no app data; the
// browser fetches the original URL again once the wake is routable.
func writeWakePage(w http.ResponseWriter, wakeID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "Accept, Sec-Fetch-Mode, Sec-Fetch-Dest")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set(wakePageHeader, "1")
	if wakeID != "" {
		w.Header().Set("x-faas-wake-id", wakeID)
	}
	w.WriteHeader(http.StatusOK)

	pageID := html.EscapeString(wakeID)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="1">
  <meta name="faas-wake-id" content="%s">
  <title>Gregale — Waking up</title>
  <style>
    :root { color-scheme: dark; font-family: system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #101318; color: #eef1f5; }
    main { width: min(32rem, calc(100%% - 3rem)); padding: 2.5rem; border: 1px solid #303743; border-radius: 1rem; background: #171b22; box-shadow: 0 1rem 3rem #0005; text-align: center; }
    .mark { color: #8ab4ff; font-weight: 700; letter-spacing: .04em; }
    h1 { margin: 1.25rem 0 .75rem; font-size: 1.7rem; }
    p { color: #b8c0cc; line-height: 1.55; }
  </style>
</head>
<body>
  <main data-faas-page="waking-up">
    <div class="mark">Gregale</div>
    <h1>Waking up your app…</h1>
    <p>This usually takes a few seconds. We’ll continue automatically.</p>
    <noscript><p>JavaScript is disabled; this page refreshes automatically.</p></noscript>
  </main>
  <script>
  (() => {
    const retry = async () => {
      const meta = document.querySelector('meta[name="faas-wake-id"]');
      const wakeID = meta ? meta.content : "";
      const headers = {};
      if (wakeID) headers["x-faas-wake-id"] = wakeID;
      try {
        const response = await fetch(window.location.href, {
          headers,
          credentials: "same-origin",
          cache: "no-store"
        });
        const isWakePage = response.headers.get("x-faas-wake-page") === "1";
        if (!isWakePage) {
          // Let the browser render the app response in its normal
          // navigation context. Avoid copying an arbitrary app body
          // through document.write (and preserve non-HTML responses).
          window.location.replace(window.location.href);
          return;
        }
        const nextWakeID = response.headers.get("x-faas-wake-id");
        if (nextWakeID && meta) meta.content = nextWakeID;
      } catch (_) {
        // The meta refresh remains as the connectivity fallback.
      }
      window.setTimeout(retry, 750);
    };
    window.setTimeout(retry, 250);
  })();
  </script>
</body>
</html>
`, pageID)
}

func (h *Handler) beginWakePageCycle(appID string) {
	if h == nil || appID == "" {
		return
	}
	h.wakePageMu.Lock()
	defer h.wakePageMu.Unlock()
	if h.wakePageCycles == nil {
		h.wakePageCycles = make(map[string]*wakePageCycle)
	}
	if cycle, ok := h.wakePageCycles[appID]; ok && cycle.wakeID == "" {
		// An empty-ID cycle is active (or waiting for the first scheduler
		// result). Concurrent followers must not clear its pending visits.
		return
	}
	h.wakePageCycles[appID] = &wakePageCycle{}
}

func (h *Handler) noteWakePageServed(ctx context.Context, appID, accountID, requestID string, servedAt time.Time) {
	if h == nil || appID == "" {
		return
	}
	visit := wakePageVisit{requestID: requestID, accountID: accountID, servedAt: servedAt.UTC()}
	h.wakePageMu.Lock()
	if h.wakePageCycles == nil {
		h.wakePageCycles = make(map[string]*wakePageCycle)
	}
	cycle := h.wakePageCycles[appID]
	if cycle == nil {
		cycle = &wakePageCycle{}
		h.wakePageCycles[appID] = cycle
	}
	if cycle.wakeID == "" {
		// A browser may refresh the page repeatedly while a long boot is
		// running. Keep one timeline row per wake generation rather than
		// turning that refresh loop into an event flood.
		if len(cycle.pending) > 0 {
			h.wakePageMu.Unlock()
			return
		}
		cycle.pending = append(cycle.pending, visit)
		h.wakePageMu.Unlock()
		return
	}
	resolvedWakeID := cycle.wakeID
	h.wakePageMu.Unlock()
	h.emitWakePageVisit(ctx, appID, resolvedWakeID, visit)
}

// finishWakePageCycle is called by the wake leader, not by the timed-out
// browser waiter. A successful wake flushes all pages with the real wake ID;
// a failed wake drops them so a later retry starts a fresh generation.
func (h *Handler) finishWakePageCycle(ctx context.Context, appID, wakeID string) {
	if h == nil || appID == "" {
		return
	}
	h.wakePageMu.Lock()
	cycle := h.wakePageCycles[appID]
	if cycle == nil {
		h.wakePageMu.Unlock()
		return
	}
	if wakeID == "" {
		delete(h.wakePageCycles, appID)
		h.wakePageMu.Unlock()
		return
	}
	cycle.wakeID = wakeID
	pending := append([]wakePageVisit(nil), cycle.pending...)
	cycle.pending = nil
	h.wakePageMu.Unlock()
	for _, visit := range pending {
		h.emitWakePageVisit(ctx, appID, wakeID, visit)
	}
}

func (h *Handler) emitWakePageVisit(ctx context.Context, appID, wakeID string, visit wakePageVisit) {
	if h == nil || h.wakePageAudit == nil || wakeID == "" {
		return
	}
	// The page event is often flushed after the original HTTP request has
	// returned, so callers pass a detached context. Keep the best-effort audit
	// write bounded rather than allowing a stalled database to pin the wake
	// leader goroutine indefinitely.
	eventCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var subject *string
	if visit.accountID != "" {
		accountID := visit.accountID
		subject = &accountID
	}
	h.wakePageAudit.Emit(eventCtx, events.WakePageServed, subject, map[string]any{
		"wake_id":    wakeID,
		"app_id":     appID,
		"request_id": visit.requestID,
		"served_at":  visit.servedAt,
	})
}
