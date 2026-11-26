package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// workloadObjectCredentials is POST /v1/workload/object-storage/credentials
// (ADR-20261122191423 slice 2): a live production instance trades its
// gregale:bindings token for a one-hour S3 credential on a bucket its app's
// `uses:` grants. The guest's loopback credential endpoint calls it and hands
// the answer to AWS SDKs unchanged. Grant resolution and issuing live in
// pkg/apid/object; this is the route's wiring.
func (s *server) workloadObjectCredentials() http.Handler {
	return middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			caller, instanceID, acct, problem := s.verifyBindingCaller(r)
			if problem != nil {
				api.WriteProblem(w, problem)
				return
			}
			if !s.platformFlagFor(platformflags.CrossAppBindings, appFlagSubject(acct, caller)) || !s.capabilityAllowedFor(r, acct) {
				writeMissingRoute(w, r)
				return
			}
			s.requireAPIRequestBudget(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
				s.issueWorkloadObjectCredential(w, r, acct, caller, instanceID)
			})(w, r, acct)
		}))
}

func (s *server) issueWorkloadObjectCredential(w http.ResponseWriter, r *http.Request, acct state.Account, caller state.App, instanceID string) {
	var req api.ObjectWorkloadCredentialRequest
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	grant, bucket, err := s.object.ResolveWorkloadGrant(r.Context(), acct, caller, req.Bucket)
	if err == nil {
		var resp api.ObjectWorkloadCredentialResponse
		if resp, err = s.object.IssueWorkloadCredential(r.Context(), acct, caller, instanceID, bucket, grant.Access, grant.Prefix, time.Now()); err == nil {
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	api.WriteProblem(w, workloadCredentialProblem(caller, req.Bucket, grant.App, err))
}

// reconcileWorkloadCredentials, after a project apply, rescopes or revokes
// the workload S3 credentials of changed apps to match their new `uses:`
// (ADR-20261122191423). Removed apps need nothing: a deleted caller's
// credentials stop resolving. A failure is logged; the credentials then
// lapse within the hour at the latest.
func (s *server) reconcileWorkloadCredentials(ctx context.Context, changed []state.App) {
	for _, app := range changed {
		// Grants resolve against the app's resource account, as when the
		// credential was issued.
		acct, err := state.AppResourceAccount(ctx, s.store, app)
		if err == nil {
			err = s.object.ReconcileWorkloadCredentials(ctx, acct, app)
		}
		if err != nil {
			s.log.Warn("revoke ungranted workload credentials", "app_id", app.ID, "err", err)
		}
	}
}

// grantedBucketNames lists the buckets caller's uses: grants, as a request
// may name them.
func grantedBucketNames(caller state.App) []string {
	var names []string
	if caller.Manifest.Uses != nil {
		for _, grant := range caller.Manifest.Uses.Normalized().Buckets {
			names = append(names, grant.Bucket)
		}
	}
	return names
}

func workloadCredentialProblem(caller state.App, bucket, owner string, err error) *api.Problem {
	service := caller.WorkloadName
	if service == "" {
		service = caller.Slug
	}
	switch {
	case errors.Is(err, object.ErrWorkloadBucketAmbiguous):
		return api.NewProblem(http.StatusBadRequest, api.CodeWorkloadBucketRequired, "Bucket required",
			fmt.Sprintf("app %q holds several bucket grants; name one with ?bucket= on the credential endpoint: %s", caller.Slug, strings.Join(grantedBucketNames(caller), ", ")))
	case errors.Is(err, object.ErrWorkloadBucketNotGranted):
		return api.NewProblem(http.StatusForbidden, CodeBindingNotGranted, "Binding not granted",
			fmt.Sprintf("app %q does not declare bucket %q; add it to project.services.%s.uses.buckets (with the app that owns it) in gregale.yaml and run `%s`", caller.Slug, bucket, service, bindingApplyCommand))
	case errors.Is(err, object.ErrWorkloadBucketNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeWorkloadBucketNotFound, "Granted bucket not found",
			fmt.Sprintf("app %q grants a bucket of %q that does not exist or is not ready in production", caller.Slug, owner))
	case errors.Is(err, object.ErrWorkloadCredentialLimit):
		return api.NewProblem(http.StatusTooManyRequests, "workload_credential_limit", "Too many workload credentials",
			fmt.Sprintf("this instance already holds %d unexpired credentials for the bucket; reuse one until it nears expiry", object.MaxLiveWorkloadCredentials))
	default:
		return api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
			"could not issue a workload credential; retry shortly")
	}
}
