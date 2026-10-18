package mcpcatalog

import (
	"context"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ObserverRunner runs bounded batches. Durable role leases provide crash recovery
// and prevent two apid processes from recording the same scheduled check.
type ObserverRunner struct {
	Endpoint   func(context.Context, state.App, state.Deployment) (string, error)
	Store      state.Store
	Domain     string
	Identities func(context.Context) []*age.X25519Identity
	Client     *http.Client
	// Probe is an optional test seam. Production always uses guarded catalog discovery.
	Probe func(context.Context, string, string, string) ([]byte, error)
	Log   *slog.Logger
}

func RunManagedObservers(ctx context.Context, store state.Store, domain string, identities func(context.Context) []*age.X25519Identity, log *slog.Logger, endpoints ...func(context.Context, state.App, state.Deployment) (string, error)) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = oci.EgressDialContext(nil)
	transport.MaxIdleConns = api.MCPCatalogObserverConcurrency * 2
	transport.IdleConnTimeout = time.Duration(api.MCPCatalogObserverLeaseSeconds) * time.Second
	defer transport.CloseIdleConnections()
	runner := ObserverRunner{Store: store, Domain: domain, Identities: identities, Log: log, Client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if len(endpoints) > 0 {
		runner.Endpoint = endpoints[0]
	}
	// Slow provider retirement must not delay ordinary catalog observation.
	var revocations sync.WaitGroup
	revocations.Add(1)
	go func() { defer revocations.Done(); runner.runRevocations(ctx) }()
	defer revocations.Wait()
	timer := time.NewTicker(time.Duration(api.MCPCatalogObserverPollSeconds) * time.Second)
	defer timer.Stop()
	for {
		if err := runner.Tick(ctx); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("managed MCP observer scheduling unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (r *ObserverRunner) Tick(ctx context.Context) error {
	source, ok := r.Store.(state.MCPCatalogObserverStore)
	if !ok {
		return errors.New("managed observer store unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	jobs, err := source.ListDueMCPCatalogObservers(readCtx, api.MCPCatalogObserverConcurrency)
	cancel()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.check(ctx, source, job); err != nil && ctx.Err() == nil && !errors.Is(err, state.ErrMCPCatalogMonitorChanged) && !errors.Is(err, state.ErrNotFound) && r.Log != nil {
				r.Log.Warn("managed MCP observer report unavailable")
			}
		}()
	}
	wg.Wait()
	return nil
}
func (r *ObserverRunner) check(ctx context.Context, source state.MCPCatalogObserverStore, job state.MCPCatalogObserverJob) error {
	claimCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	claimed, err := source.ClaimMCPCatalogObserver(claimCtx, job)
	cancel()
	if err != nil {
		return err
	}
	job = claimed
	if job.Snapshot.ObserverStatus == nil || job.Snapshot.ObserverStatus.LeaseUntil == nil {
		return state.ErrMCPCatalogMonitorChanged
	}
	workCtx, workCancel := context.WithDeadline(ctx, *job.Snapshot.ObserverStatus.LeaseUntil)
	defer workCancel()
	ctx = workCtx
	// Validate current ownership and workload before decrypting or making a request.
	readCtx, readCancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	app, appErr := r.Store.AppByID(readCtx, job.AppID)
	account, accountErr := r.Store.AccountByID(readCtx, job.AccountID)
	readCancel()
	if appErr != nil || accountErr != nil {
		return errors.New("observer ownership unavailable")
	}
	if app.AccountID != job.AccountID || !app.AcceptsRequestInvocations() || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || (app.Status != state.AppActive && app.Status != state.AppEvictedCold) || !account.Plan.CustomMetricsAllowed() {
		return state.ErrMCPCatalogMonitorChanged
	}
	cfg := job.Snapshot.Config
	// Credential renewal and catalog discovery share the configured source
	// timeout, leaving room to report within the durable lease.
	sourceCtx, sourceCancel := context.WithTimeout(ctx, time.Duration(cfg.Observer.TimeoutSeconds)*time.Second)
	defer sourceCancel()
	endpoint, endpointErr := managedObserverEndpoint(r.Domain, app.Slug, cfg.Observer.Endpoint)
	if scope := api.MCPConnectionScope(cfg.Scope); scope != state.DefaultEnvScope {
		endpointErr = state.ErrConflict
		deployment, e := state.ResolveEnvironmentDeployment(sourceCtx, r.Store, app.ID, scope)
		if e == nil && deployment.AppID == app.ID && api.MCPConnectionScope(deployment.Scope) == scope && r.Endpoint != nil {
			base, e := r.Endpoint(sourceCtx, app, deployment)
			if e == nil {
				endpoint, endpointErr = (mcphosting.Config{Endpoint: cfg.Observer.Endpoint}).URL(base)
			}
		}
	}
	var token string
	var tokenErr error
	if endpointErr == nil {
		token, tokenErr = r.token(sourceCtx, job)
	}

	reason := ""
	var observed []byte
	if endpointErr != nil {
		reason = "source_unavailable"
	} else if tokenErr != nil {
		reason = "credential_unavailable"
	} else {
		baseline, _, baselineErr := contract(cfg.Baseline)
		if baselineErr != nil || endpointErr != nil {
			reason = "comparison_unavailable"
		} else {
			probeCtx, probeCancel := context.WithTimeout(sourceCtx, time.Duration(cfg.Observer.TimeoutSeconds)*time.Second)
			if r.Probe != nil {
				observed, err = r.Probe(probeCtx, endpoint, token, baseline.ProtocolVersion)
			} else if r.Client == nil {
				err = errors.New("guarded observer transport unavailable")
			} else {
				observed, err = mcphosting.ObserveCatalog(probeCtx, endpoint, token, baseline.ProtocolVersion, r.Client)
			}
			probeCancel()
			if err != nil || len(observed) > api.MCPCatalogMonitorMaxRequestBytes {
				reason = "source_unavailable"
				observed = nil
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	receipt, problem := evaluateObservation(cfg, api.MCPCatalogObservationRequest{Role: job.Role, Revision: cfg.Revision, Observed: observed, Unavailable: reason != ""})
	if problem != nil {
		reason = "comparison_unavailable"
		receipt, _ = evaluateObservation(cfg, api.MCPCatalogObservationRequest{Role: job.Role, Revision: cfg.Revision, Unavailable: true})
	}
	failures := 0
	if job.Snapshot.ObserverStatus != nil {
		failures = job.Snapshot.ObserverStatus.ConsecutiveFailures
	}
	if reason != "" {
		failures++
	} else {
		failures = 0
	}
	reportCtx, reportCancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	defer reportCancel()
	_, err = source.CompleteMCPCatalogObserver(reportCtx, job, receipt, reason, observerDelay(cfg.Observer.IntervalSeconds, failures))
	return err
}
func (r *ObserverRunner) token(ctx context.Context, job state.MCPCatalogObserverJob) (string, error) {
	if job.Snapshot.Config.Observer.OAuth != nil {
		return r.oauthToken(ctx, job)
	}
	ref := job.Snapshot.Config.Observer.SecretRef
	if ref == "" {
		return "", nil
	}
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	if !strings.HasPrefix(ref, api.SecretRefPrefix) || api.ValidateEnvKey(key) != nil || r.Identities == nil {
		return "", errors.New("credential unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	defer cancel()
	secret, err := r.Store.GetAppSecretInScope(readCtx, job.AccountID, job.AppID, api.MCPConnectionScope(job.Snapshot.Config.Scope), key)
	if err != nil || secret == nil || secret.AccountID != job.AccountID || secret.AppID != job.AppID || secret.Scope != api.MCPConnectionScope(job.Snapshot.Config.Scope) || secret.Key != key || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" || secret.ManagedCredentialRef != "" {
		return "", errors.New("credential unavailable")
	}
	envelope, err := secretbox.OpenMulti(r.Identities(readCtx), secret.Ciphertext)
	if err != nil || envelope[key] == "" || strings.ContainsAny(envelope[key], "\r\n") {
		return "", errors.New("credential unavailable")
	}
	return envelope[key], nil
}
func managedObserverEndpoint(domain, slug, path string) (string, error) {
	// The target is always this app's platform origin; customer URLs, aliases and redirects are not accepted.
	base, err := url.Parse("https://" + slug + "." + domain)
	if !api.ValidAppSlug(slug) || err != nil || base.Hostname() != slug+"."+domain || base.Port() != "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid application origin")
	}
	endpoint, err := url.Parse(path)
	if err != nil || endpoint.Host != "" || endpoint.IsAbs() || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", errors.New("invalid application path")
	}
	base.Path = endpoint.Path
	base.RawPath = endpoint.RawPath
	return base.String(), nil
}
func observerDelay(interval, failures int) time.Duration {
	seconds := interval
	ceiling := api.MCPCatalogObserverMaxBackoffSeconds
	if seconds > ceiling {
		ceiling = seconds
	}
	for n := 0; n < failures && seconds < ceiling; n++ {
		seconds *= 2
		if seconds > ceiling {
			seconds = ceiling
		}
	}
	// Positive-only jitter never increases polling frequency above the configured cadence.
	return time.Duration(seconds)*time.Second + time.Duration(rand.IntN(seconds+1))*100*time.Millisecond
}

func (r *ObserverRunner) runRevocations(ctx context.Context) {
	timer := time.NewTicker(time.Duration(api.MCPCatalogObserverPollSeconds) * time.Second)
	defer timer.Stop()
	for {
		if err := r.TickRevocations(ctx); err != nil && ctx.Err() == nil && r.Log != nil {
			r.Log.Warn("managed MCP revocation scheduling unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
