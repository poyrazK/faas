package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithInternalCIConfig attaches boot-time internal admission policy.
func (s *server) WithInternalCIConfig(cfg ci.Config) *server { s.ciConfig = cfg; return s }

func (s *server) registerInternalCI(mux *http.ServeMux) {
	routes := map[string]ciapi.Action{
		"GET /v1/internal/ci/health":                                               ciapi.Handler.Health,
		"GET /v1/internal/ci/capabilities":                                         ciapi.Handler.Capabilities,
		"POST /v1/internal/ci/validate":                                            ciapi.Handler.Validate,
		"PUT /v1/internal/ci/pipelines/{name}":                                     ciapi.Handler.PutRevision,
		"GET /v1/internal/ci/pipelines":                                            ciapi.Handler.ListRevisions,
		"GET /v1/internal/ci/pipelines/{name}":                                     ciapi.Handler.ListRevisions,
		"GET /v1/internal/ci/revisions/{id}":                                       ciapi.Handler.Revision,
		"POST /v1/internal/ci/runs":                                                ciapi.Handler.Submit,
		"GET /v1/internal/ci/runs":                                                 ciapi.Handler.ListRuns,
		"GET /v1/internal/ci/runs/{id}":                                            ciapi.Handler.Run,
		"POST /v1/internal/ci/runs/{id}/cancel":                                    ciapi.Handler.Cancel,
		"GET /v1/internal/ci/runs/{id}/events":                                     ciapi.Handler.Events,
		"PUT /v1/internal/ci/github/bindings/{name}":                               ciapi.Handler.PutGitHubBinding,
		"GET /v1/internal/ci/github/bindings":                                      ciapi.Handler.ListGitHubBindings,
		"DELETE /v1/internal/ci/github/bindings/{name}":                            ciapi.Handler.DisableGitHubBinding,
		"POST /v1/internal/ci/executions":                                          ciapi.Handler.SubmitExecution,
		"PUT /v1/internal/ci/profiles/{name}":                                      ciapi.Handler.PutExecutionProfile,
		"GET /v1/internal/ci/profiles":                                             ciapi.Handler.ListExecutionProfiles,
		"GET /v1/internal/ci/profiles/{id}":                                        ciapi.Handler.ExecutionProfile,
		"DELETE /v1/internal/ci/profiles/{id}":                                     ciapi.Handler.DisableExecutionProfile,
		"PUT /v1/internal/ci/github/automation/{binding}":                          ciapi.Handler.PutAutomationPolicy,
		"GET /v1/internal/ci/github/automation":                                    ciapi.Handler.ListAutomationPolicies,
		"DELETE /v1/internal/ci/github/automation/{binding}":                       ciapi.Handler.DisableAutomationPolicy,
		"GET /v1/internal/ci/github/automation/{binding}/events":                   ciapi.Handler.ListAutomationEvents,
		"GET /v1/internal/ci/github/automation/{binding}/events/{id}":              ciapi.Handler.AutomationEventStatus,
		"POST /v1/internal/ci/github/automation/{binding}/events/{id}/retry":       ciapi.Handler.RetryAutomationEvent,
		"GET /v1/internal/ci/github/automation/{binding}/checks":                   ciapi.Handler.ListSourceCheckDeliveries,
		"GET /v1/internal/ci/github/automation/{binding}/events/{id}/check":        ciapi.Handler.SourceCheckDelivery,
		"POST /v1/internal/ci/github/automation/{binding}/events/{id}/check/retry": ciapi.Handler.RetrySourceCheckDelivery,
		"GET /v1/internal/ci/executions":                                           ciapi.Handler.ListExecutions,
		"GET /v1/internal/ci/checks":                                               ciapi.Handler.ListCheckDeliveries,
		"GET /v1/internal/ci/checks/{id}":                                          ciapi.Handler.CheckDelivery,
		"POST /v1/internal/ci/checks/{id}/retry":                                   ciapi.Handler.RetryCheckDelivery,
		"GET /v1/internal/ci/executions/{id}":                                      ciapi.Handler.Execution,
		"GET /v1/internal/ci/executions/{id}/reports":                              ciapi.Handler.TestReports,
		"POST /v1/internal/ci/executions/{id}/cancel":                              ciapi.Handler.CancelExecution,
		"POST /v1/internal/ci/executions/{id}/rerun":                               ciapi.Handler.RerunExecution,
		"POST /v1/internal/ci/executions/{id}/archive":                             ciapi.Handler.ArchiveExecution,
		"GET /v1/internal/ci/archives":                                             ciapi.Handler.ExecutionArchives,
		"GET /v1/internal/ci/archives/{id}":                                        ciapi.Handler.ExecutionArchive,
		"GET /v1/internal/ci/executions/{id}/attempts/{attempt}/logs":              ciapi.Handler.ExecutionLogs,
	}
	for pattern, action := range routes {
		handler := s.requireScope(api.ScopesAdminOnly...)(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			provider, ok := s.store.(interface{ CIStore() ci.Store })
			if !ok {
				httpkit.NotFound(w, "CI is not available")
				return
			}
			action(ciapi.Handler{Config: s.ciConfig, Store: provider.CIStore(), Audit: s.audit.Emit, VerifyGitHub: s.verifyCIGitHubRepository, ExecutionReady: s.internalCIExecutionReady()}, w, r, acct)
		})
		protected := strings.Contains(pattern, "/ci/health") || strings.Contains(pattern, "/ci/executions") || strings.Contains(pattern, "/ci/profiles") || strings.Contains(pattern, "/ci/github/automation") || strings.Contains(pattern, "/ci/checks") || strings.Contains(pattern, "/ci/archives")
		if !strings.HasPrefix(pattern, "GET ") || protected {
			handler = s.requireMFA(handler)
		}
		authenticated := s.authLimited(s.internalCIGate(handler))
		if protected {
			mux.HandleFunc(pattern, ciruntime.SecureTransport(s.ciRuntimeOrigin, ci.ValidRuntimeOrigin(s.ciRuntimeOrigin), authenticated))
		} else {
			mux.HandleFunc(pattern, authenticated)
		}
	}
}

func (s *server) verifyCIGitHubRepository(ctx context.Context, account string, installationID, repositoryID int64, repository string) error {
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	acct, err := s.store.AccountByID(ctx, account)
	if errors.Is(err, state.ErrNotFound) || err == nil && !acct.MayDeploy() {
		return ci.ErrGitHubAccess
	}
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	if _, err := s.store.GitHubInstallForAccountInstallation(ctx, account, installationID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return ci.ErrGitHubAccess
		}
		return ci.ErrGitHubUnavailable
	}
	if s.githubd == nil {
		return ci.ErrGitHubUnavailable
	}
	repos, err := s.githubd.ListInstallableRepos(ctx, account, installationID)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	for _, repo := range repos {
		if repo.ID == repositoryID && strings.EqualFold(repo.FullName, repository) {
			return nil
		}
	}
	return ci.ErrGitHubAccess
}

func (s *server) internalCIGate(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.ciConfig.Allows(acct.ID) {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r, acct)
	}
}
