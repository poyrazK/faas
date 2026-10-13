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

// Runner executes due checks. Concurrency bounds simultaneous probes so a
// burst of due checks cannot flood the edge from the control plane.
type Runner struct {
	Store       state.SyntheticRunStore
	Client      *http.Client // nil = NewClient()
	AppsDomain  string
	Concurrency int
	Log         *slog.Logger
	Now         func() time.Time
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

// RunStats summarises one pass.
type RunStats struct {
	Due, OK, Failed, RecordErrors int
	Purged                        int64
}

// RunOnce runs every due check once and purges runs past retention.
func (r *Runner) RunOnce(ctx context.Context) (RunStats, error) {
	var stats RunStats
	now := r.now()
	checks, err := r.Store.ListRunnableSyntheticChecks(ctx)
	if err != nil {
		return stats, err
	}
	var due []state.RunnableSyntheticCheck
	for _, c := range checks {
		if Due(c, now) {
			due = append(due, c)
		}
	}
	stats.Due = len(due)
	var mu sync.Mutex
	sem := make(chan struct{}, max(r.Concurrency, 1))
	var wg sync.WaitGroup
	for _, c := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func(c state.RunnableSyntheticCheck) {
			defer wg.Done()
			defer func() { <-sem }()
			run := r.Probe(ctx, c)
			err := r.Store.RecordSyntheticCheckRun(ctx, run)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil && !errors.Is(err, state.ErrNotFound): // deleted mid-run is fine
				stats.RecordErrors++
				r.log().Warn("synthetics: record run", "check", c.ID, "err", err)
			case run.OK:
				stats.OK++
			default:
				stats.Failed++
			}
		}(c)
	}
	wg.Wait()
	stats.Purged, err = r.Store.PurgeSyntheticCheckRunsBefore(ctx, now.Add(-api.SyntheticCheckRunRetentionDays*24*time.Hour))
	return stats, err
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
