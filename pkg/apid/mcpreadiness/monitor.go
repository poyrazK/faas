package mcpreadiness

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpcatalog"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// MonitorRunner uses deployment preview origins and never accepts a target URL
// from configuration. Leases and bounded batches are shared across apid replicas.
type MonitorRunner struct {
	Handler    *Handler
	Identities func(context.Context) []*age.X25519Identity
	Log        *slog.Logger
}

func (r *MonitorRunner) Run(ctx context.Context) {
	if r.Handler.Client != nil {
		defer r.Handler.Client.CloseIdleConnections()
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil && r.Log != nil {
			r.Log.Warn("MCP readiness monitoring unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *MonitorRunner) Tick(ctx context.Context) error {
	source, ok := r.Handler.Store.(state.MCPReadinessMonitorStore)
	if !ok {
		return errors.New("readiness monitoring store unavailable")
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	jobs, err := source.ClaimMCPReadinessMonitors(claimCtx)
	cancel()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.check(ctx, source, job); err != nil && ctx.Err() == nil && !errors.Is(err, state.ErrConflict) && r.Log != nil {
				r.Log.Warn("MCP readiness monitoring report unavailable")
			}
		}()
	}
	wg.Wait()
	return nil
}
func (r *MonitorRunner) check(ctx context.Context, source state.MCPReadinessMonitorStore, job state.MCPReadinessMonitorJob) error {
	work, cancel := context.WithDeadline(ctx, job.LeaseUntil.Add(-5*time.Second))
	defer cancel()
	// Ownership, workload and subject are checked before unsealing any credential.
	app, err := r.Handler.Store.AppByID(work, job.Subject.AppID)
	if err != nil {
		return err
	}
	deployment, err := r.Handler.Store.DeploymentByID(work, job.Subject.DeploymentID)
	if err != nil {
		return err
	}
	if app.AccountID != job.Subject.AccountID || app.Status != state.AppActive || !app.AcceptsRequestInvocations() || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || deployment.AppID != app.ID || deployment.Status != state.DeployLive || deployment.TrafficPercent <= 0 {
		return state.ErrConflict
	}
	current, err := r.Handler.Store.(state.MCPReadinessStore).MCPReadinessSubject(work, job.Subject.AccountID, job.Subject.DeploymentID)
	if err != nil {
		return err
	}
	if current.Policy.Revision != job.Subject.Policy.Revision || current.ArtifactSHA256 != job.Subject.ArtifactSHA256 {
		return state.ErrConflict
	}
	probeCtx, probeCancel := context.WithTimeout(work, 25*time.Second)
	defer probeCancel()
	var token string
	var credential *mcpcatalog.ReadinessOAuthCredential
	if job.Role.Monitor.ManagedOAuth() {
		ref := job.Role.Monitor.OAuthForScope(deployment.Scope)
		if ref == nil {
			err = state.ErrMCPReadinessOAuthChanged
		} else {
			observer := mcpcatalog.ObserverRunner{Store: r.Handler.Store, Client: r.Handler.Client, Identities: r.Identities}
			credential, err = observer.AcquireReadinessOAuth(probeCtx, job.Subject.AccountID, job.Subject.AppID, *ref, job.Subject.Policy.Endpoint)
			if credential != nil {
				token = credential.Token
				defer func() {
					release, stop := context.WithTimeout(context.WithoutCancel(work), 5*time.Second)
					defer stop()
					_ = credential.Release(release)
				}()
			}
		}
	} else {
		token, err = r.token(probeCtx, job, deployment)
	}
	if errors.Is(err, state.ErrMCPReadinessOAuthBusy) {
		return state.ErrConflict
	}
	evidence := api.MCPReadinessEvidence{Role: job.Role.Name, ProtocolVersion: job.Role.ProtocolVersion, CatalogFormat: job.Role.CatalogFormat, Checks: []api.MCPReadinessCheck{}}
	reason := ""
	if err != nil {
		reason = "credential_unavailable"
		if job.Role.Monitor.OAuthConnection != nil {
			reason = readinessOAuthFailure(err)
		}
	} else if r.Handler.Endpoint == nil {
		reason = "preview_unavailable"
	} else {
		endpoint, endpointErr := r.Handler.Endpoint(probeCtx, app, deployment)
		if endpointErr != nil {
			reason = "preview_unavailable"
		} else {
			endpoint, endpointErr = (mcphosting.Config{Endpoint: job.Subject.Policy.Endpoint}).URL(endpoint)
			if endpointErr != nil {
				reason = "preview_unavailable"
			} else {
				evidence = probeEvidence(probeCtx, r.Handler.Client, endpoint, token, job.Role)
				if !evidence.Passed {
					reason = "protocol_failure"
					for _, check := range evidence.Checks {
						if check.Name == "expected_catalog" && check.Status == "failed" {
							reason = "catalog_drift"
						}
					}
				}
			}
		}
	}
	if reason != "" && len(evidence.Checks) == 0 {
		evidence.Checks = append(evidence.Checks, api.MCPReadinessCheck{Name: "monitor", Status: "failed", Detail: reason})
	}
	// Parent cancellation leaves the lease for retry; it never records a false failure.
	if work.Err() != nil {
		return work.Err()
	}
	report, cancelReport := context.WithTimeout(work, 5*time.Second)
	defer cancelReport()
	return source.CompleteMCPReadinessMonitor(report, job, evidence, reason)
}
func probeEvidence(ctx context.Context, httpClient *http.Client, endpoint, token string, role api.MCPReadinessRole) api.MCPReadinessEvidence {
	e := api.MCPReadinessEvidence{Role: role.Name, ProtocolVersion: role.ProtocolVersion, CatalogFormat: role.CatalogFormat, Checks: []api.MCPReadinessCheck{}}
	client, err := mcphosting.NewClient(endpoint, token, role.ProtocolVersion)
	if err != nil || httpClient == nil {
		e.Checks = append(e.Checks, api.MCPReadinessCheck{Name: "transport", Status: "failed", Detail: "probe transport unavailable"})
		return e
	}
	client.HTTP = httpClient
	result := mcphosting.CheckReadinessDigest(ctx, client, role.CatalogFormat, role.CatalogSHA256)
	e.Passed = result.OK
	e.ProtocolVersion = result.ProtocolVersion
	e.CatalogFormat = result.CatalogFormat
	e.CatalogSHA256 = result.CatalogSHA256
	for _, c := range result.Checks {
		e.Checks = append(e.Checks, api.MCPReadinessCheck{Name: c.Name, Status: c.Status, Detail: c.Detail})
	}
	return e
}
func (r *MonitorRunner) token(ctx context.Context, job state.MCPReadinessMonitorJob, d state.Deployment) (string, error) {
	ref := job.Role.Monitor.SecretRef
	if ref == "" {
		return "", nil
	}
	if api.ValidateMCPReadinessMonitor(*job.Role.Monitor) != nil || r.Identities == nil {
		return "", errors.New("credential unavailable")
	}
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	scope := d.Scope
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	secret, err := r.Handler.Store.GetAppSecretInScope(ctx, job.Subject.AccountID, job.Subject.AppID, scope, key)
	if err != nil || secret == nil || secret.AccountID != job.Subject.AccountID || secret.AppID != job.Subject.AppID || secret.Scope != scope || secret.Key != key || secret.ManagedCredentialRef != "" || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
		return "", errors.New("credential unavailable")
	}
	values, err := secretbox.OpenMulti(r.Identities(ctx), secret.Ciphertext)
	if err != nil || values[key] == "" || !api.ValidMCPProbeToken(values[key]) {
		return "", errors.New("credential unavailable")
	}
	return values[key], nil
}

func readinessOAuthFailure(err error) string {
	if errors.Is(err, state.ErrMCPReadinessOAuthReconnect) {
		return "oauth_reconnect_required"
	}
	if errors.Is(err, state.ErrMCPReadinessOAuthChanged) || errors.Is(err, state.ErrNotFound) {
		return "oauth_connection_changed"
	}
	return "oauth_credential_unavailable"
}
