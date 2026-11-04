// Package synthetics runs customer-defined synthetic HTTP checks
// (ADR-748): meterd's runner picks the checks that are due, requests each
// app's own public hostname through the normal edge, and records the outcome.
package synthetics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Error classes recorded per run; mirrors synthetic_check_runs_error_chk.
const (
	ErrorStatus  = "status"
	ErrorTimeout = "timeout"
	ErrorDNS     = "dns"
	ErrorConnect = "connect"
	ErrorTLS     = "tls"
	ErrorOther   = "other"
)

// bodyReadLimit bounds how much of a response is read: enough to time the
// response, never enough for a hostile app to hold the runner.
const bodyReadLimit = 64 << 10

// Runner executes due checks. A pass starts the due checks a probe slot is
// free for and returns without waiting for them (ADR-20261104142830): a
// slow or hanging app holds only its own probes, never the pass. Concurrency
// bounds simultaneous probes so a burst of due checks cannot flood the edge
// from the control plane, and PerAccount bounds one account's share of them,
// so one account's checks can never hold every slot. The most overdue check
// starts first, so a check waiting for a slot is not passed over.
type Runner struct {
	Store       state.SyntheticRunStore
	Client      *http.Client // nil = NewClient()
	AppsDomain  string
	Concurrency int
	PerAccount  int // 0 = api.SyntheticCheckProbesPerAccount
	Log         *slog.Logger
	Now         func() time.Time
	// Released decides whether a due check is still probed: its account's
	// plan and the synthetic-checks capability flag (ADR-20261028101615).
	// nil probes every due check.
	Released func(ctx context.Context, check state.RunnableSyntheticCheck) bool

	mu       sync.Mutex
	inFlight map[string]bool // check id → probing
	accounts map[string]int  // account id → probes in flight
	finished RunStats        // outcomes of probes finished since the last pass
	wg       sync.WaitGroup
}

// NewClient returns the probe client: no redirects (a 3xx is the result),
// no cookie jar, and a dedicated transport so probe connections never mix
// with other meterd traffic.
func NewClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        16,
			IdleConnTimeout:     30 * time.Second,
			ForceAttemptHTTP2:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// RunStats summarises one pass. Due counts the due checks not already
// being probed; Started of them got a slot and Deferred wait for one.
// OK, Failed and RecordErrors count the probes that finished since the
// previous pass.
type RunStats struct {
	Due, Started, Deferred, OK, Failed, RecordErrors int
	// NotReleased counts due checks not probed because Released refused them.
	NotReleased int
	Purged      int64
	// Backlog is how late the most overdue deferred check is.
	Backlog time.Duration
}

// RunOnce starts the due checks there are probe slots for, reports the
// probes finished since the previous pass, and purges runs past retention.
func (r *Runner) RunOnce(ctx context.Context) (RunStats, error) {
	var stats RunStats
	now := r.now()
	checks, err := r.Store.ListRunnableSyntheticChecks(ctx)
	if err != nil {
		return r.collect(stats), err
	}
	var due []state.RunnableSyntheticCheck
	for _, c := range checks {
		if !Due(c, now) || r.probing(c.ID) {
			continue
		}
		if r.Released != nil && !r.Released(ctx, c) {
			stats.NotReleased++
			continue
		}
		due = append(due, c)
	}
	stats.Due = len(due)
	// Most overdue first: never run, then the longest past its interval.
	sort.SliceStable(due, func(i, j int) bool { return dueAt(due[i]).Before(dueAt(due[j])) })
	for _, c := range due {
		if !r.start(ctx, c) {
			stats.Deferred++
			stats.Backlog = max(stats.Backlog, now.Sub(dueAt(c)))
			continue
		}
		stats.Started++
	}
	stats = r.collect(stats)
	stats.Purged, err = r.Store.PurgeSyntheticCheckRunsBefore(ctx, now.Add(-api.SyntheticCheckRunRetentionDays*24*time.Hour))
	return stats, err
}

// Wait blocks until every started probe has finished and been recorded.
func (r *Runner) Wait() { r.wg.Wait() }

// dueAt is when a check fell due; a check that never ran is due from its
// creation.
func dueAt(c state.RunnableSyntheticCheck) time.Time {
	if c.LastRunAt.IsZero() {
		return c.CreatedAt
	}
	return c.LastRunAt.Add(time.Duration(c.IntervalSeconds) * time.Second)
}

func (r *Runner) probing(checkID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight[checkID]
}

// start probes c in the background when a global and an account slot are
// free, and reports whether it did.
func (r *Runner) start(ctx context.Context, c state.RunnableSyntheticCheck) bool {
	perAccount := r.PerAccount
	if perAccount <= 0 {
		perAccount = api.SyntheticCheckProbesPerAccount
	}
	r.mu.Lock()
	if r.inFlight == nil {
		r.inFlight, r.accounts = map[string]bool{}, map[string]int{}
	}
	if len(r.inFlight) >= max(r.Concurrency, 1) || r.accounts[c.AccountID] >= perAccount {
		r.mu.Unlock()
		return false
	}
	r.inFlight[c.ID] = true
	r.accounts[c.AccountID]++
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		run := r.Probe(ctx, c)
		err := r.Store.RecordSyntheticCheckRun(ctx, run)
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.inFlight, c.ID)
		if r.accounts[c.AccountID]--; r.accounts[c.AccountID] <= 0 {
			delete(r.accounts, c.AccountID)
		}
		switch {
		case err != nil && !errors.Is(err, state.ErrNotFound): // deleted mid-run is fine
			r.finished.RecordErrors++
			r.log().Warn("synthetics: record run", "check", c.ID, "err", err)
		case run.OK:
			r.finished.OK++
		default:
			r.finished.Failed++
		}
	}()
	return true
}

// collect moves the outcomes of finished probes into stats.
func (r *Runner) collect(stats RunStats) RunStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	stats.OK, stats.Failed, stats.RecordErrors = r.finished.OK, r.finished.Failed, r.finished.RecordErrors
	r.finished = RunStats{}
	return stats
}

// Due reports whether a check should run at now: never run, or its interval
// has elapsed since the last run.
func Due(c state.RunnableSyntheticCheck, now time.Time) bool {
	return c.LastRunAt.IsZero() || !now.Before(c.LastRunAt.Add(time.Duration(c.IntervalSeconds)*time.Second))
}

// URL is the address a check requests: the app's own hostname plus the
// validated origin-relative path.
func URL(c state.RunnableSyntheticCheck, appsDomain string) string {
	return "https://" + api.AppHostname(c.AppSlug, appsDomain) + c.Path
}

// Probe performs one request and classifies the outcome.
func (r *Runner) Probe(ctx context.Context, c state.RunnableSyntheticCheck) state.SyntheticCheckRun {
	started := r.now()
	run := state.SyntheticCheckRun{CheckID: c.ID, StartedAt: started.UTC().Truncate(time.Millisecond)}
	pctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, c.Method, URL(c, r.AppsDomain), nil)
	if err != nil {
		run.ErrorClass = ErrorOther
		return run
	}
	req.Header.Set("User-Agent", "Gregale-Synthetics/1 (+https://gregale.dev/docs/synthetic-checks)")
	// A request with a cookie is never answered from the edge cache, fresh
	// or stale (ADR-122 D3), so a probe checks the app, not a copy the edge
	// kept (ADR-20261104142830).
	req.AddCookie(&http.Cookie{Name: api.SyntheticProbeCookieName, Value: "1"})
	resp, err := r.client().Do(req)
	if err == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, bodyReadLimit))
		_ = resp.Body.Close()
		run.StatusCode = resp.StatusCode
	}
	run.LatencyMS = int(time.Since(started).Milliseconds())
	switch {
	case err != nil:
		// No response, or headers arrived but the body stalled or broke:
		// either way the run fails rather than credit a partial response.
		run.ErrorClass = Classify(err)
	case !StatusOK(run.StatusCode, c.ExpectedStatus):
		run.ErrorClass = ErrorStatus
	}
	run.OK = run.ErrorClass == ""
	return run
}

// StatusOK applies a check's expectation: an exact code, or any 2xx.
func StatusOK(got, expected int) bool {
	if expected != 0 {
		return got == expected
	}
	return got >= 200 && got < 300
}

// Classify maps a transport error to the bounded error vocabulary.
func Classify(err error) string {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var recordErr tls.RecordHeaderError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return ErrorTimeout
	case errors.As(err, &dnsErr):
		return ErrorDNS
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &recordErr):
		return ErrorTLS
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return ErrorConnect
	}
	return ErrorOther
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (r *Runner) client() *http.Client {
	if r.Client == nil {
		r.Client = NewClient()
	}
	return r.Client
}

func (r *Runner) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *Runner) log() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}
