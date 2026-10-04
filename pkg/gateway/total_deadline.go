// adr: 531
package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

// This header exists only on the protected public-to-internal transport.
// Public ingress always replaces client claims; internal consumes and strips
// it before customer policy or guest forwarding can see it.
const trafficStartedHeader = "X-Faas-Traffic-Started-At"

type totalDeadlineBudgetKey struct{}

func stampTrafficStart(r *http.Request) {
	started, ok := StartTimeFromContext(r.Context())
	if !ok {
		started = time.Now()
	}
	r.Header.Set(trafficStartedHeader, strconv.FormatInt(started.UnixNano(), 10))
	r.Header.Del(trafficdeadline.Header)
}

// TrustedTrafficIngress belongs exclusively on the private compute listener.
// Its public proxy peer owns the timestamp. Never mount it on a public or
// guest service-proxy listener.
func TrustedTrafficIngress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		values := r.Header.Values(trafficStartedHeader)
		r.Header.Del(trafficStartedHeader)
		if len(values) > 0 {
			if len(values) != 1 {
				api.WriteProblem(w, api.ErrInternal("ambiguous platform ingress timestamp"))
				return
			}
			ns, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || ns <= 0 {
				api.WriteProblem(w, api.ErrInternal("invalid platform ingress timestamp"))
				return
			}
			if candidate := time.Unix(0, ns); candidate.Before(started) {
				started = candidate
			}
		}
		next.ServeHTTP(w, r.WithContext(WithStartTime(r.Context(), started)))
	})
}

func rememberBudgetCancel(r *http.Request, ctx context.Context, cancel context.CancelFunc) {
	prior, _ := r.Context().Value(requestBudgetCancelKey{}).(context.CancelFunc)
	ctx = context.WithValue(ctx, requestBudgetCancelKey{}, context.CancelFunc(func() {
		cancel()
		if prior != nil {
			prior()
		}
	}))
	*r = *r.WithContext(ctx)
}

// applyTotalDeadline runs immediately after owner resolution, before auth,
// cache, upload and wake. Pin its rule for the later execution budget too.
func (h *Handler) applyTotalDeadline(w http.ResponseWriter, r *http.Request, app App) bool {
	if _, pinned := r.Context().Value(totalDeadlineBudgetKey{}).(EdgeRuleBudgetResolved); pinned {
		if requestBudgetExpired(r.Context()) {
			writeRequestBudgetExceededForRequest(w, r)
			return true
		}
		return false
	}
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchBudget(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil || rule.AccountID != app.AccountID || rule.TotalDeadlineMs <= 0 {
		return false
	}
	if h.trafficDeadlines == nil {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrUnavailable)
		return true
	}
	pinned := *rule
	ctx := context.WithValue(r.Context(), totalDeadlineBudgetKey{}, pinned)
	started, _ := StartTimeFromContext(ctx)
	limits, _ := api.LimitsFor(app.Plan)
	ctx, cancel, _ := reqbudget.WithStarted(ctx, started, time.Duration(rule.TotalDeadlineMs)*time.Millisecond,
		limits.RequestBudgetMaxDuration(), "forward", r.Method+":"+r.URL.Path)
	rememberBudgetCancel(r, ctx, cancel)
	*r = *r.WithContext(newManagedDeadlineChain(r.Context(), app.AccountID))
	if requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceededForRequest(w, r)
		return true
	}
	return false
}
