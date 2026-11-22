package apidserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// workloadObjectCredentials is POST /v1/workload/object-storage/credentials
// (ADR-20261122191423 slice 2): a live production instance trades its
// gregale:bindings token for a one-hour S3 credential on a bucket its app's
// `uses:` grants. The guest's loopback credential endpoint calls it and hands
// the answer to AWS SDKs unchanged.
func (s *server) workloadObjectCredentials() http.Handler {
	return middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			caller, instanceID, acct, problem := s.verifyBindingCaller(r)
			if problem == nil && !s.platformFlagFor(platformflags.CrossAppBindings, appFlagSubject(acct, caller)) {
				problem = bindingUnauthorized("binding tokens are not enabled for this account")
			}
			var req api.ObjectWorkloadCredentialRequest
			if problem == nil && !decodeJSONLimit(w, r, &req, 4096) {
				return
			}
			var grant capgrant.BucketGrant
			if problem == nil {
				grant, problem = workloadBucketGrant(caller, req.Bucket)
			}
			var bucket state.ObjectBucket
			if problem == nil {
				bucket, problem = s.workloadGrantBucket(r, acct, caller, grant)
			}
			if problem != nil {
				api.WriteProblem(w, problem)
				return
			}
			resp, err := s.object.IssueWorkloadCredential(r.Context(), acct, caller, instanceID, bucket, grant.Access, grant.Prefix, time.Now())
			if err != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
					"could not issue a workload credential; retry shortly"))
				return
			}
			writeJSON(w, http.StatusOK, resp)
		}))
}

// workloadBucketGrant picks the caller's grant for bucket, or its only bucket
// grant when bucket is empty.
func workloadBucketGrant(caller state.App, bucket string) (capgrant.BucketGrant, *api.Problem) {
	var grants []capgrant.BucketGrant
	if caller.Manifest.Uses != nil {
		grants = caller.Manifest.Uses.Normalized().Buckets
	}
	if bucket == "" && len(grants) == 1 {
		return grants[0], nil
	}
	if bucket == "" && len(grants) > 1 {
		return capgrant.BucketGrant{}, api.ErrValidation(fmt.Sprintf("app %q holds %d bucket grants; name the bucket", caller.Slug, len(grants)))
	}
	for _, grant := range grants {
		if strings.EqualFold(grant.Bucket, bucket) {
			return grant, nil
		}
	}
	return capgrant.BucketGrant{}, bindingNotGranted(caller, capgrant.KindBuckets, caller, bucket)
}

// workloadGrantBucket resolves the grant's owner app (self, a service of the
// caller's project, or an app slug of the same resource account) and its
// production bucket.
func (s *server) workloadGrantBucket(r *http.Request, acct state.Account, caller state.App, grant capgrant.BucketGrant) (state.ObjectBucket, *api.Problem) {
	missing := api.NewProblem(http.StatusNotFound, "workload_bucket_not_found", "Granted bucket not found",
		fmt.Sprintf("app %q grants bucket %q of %q, which does not exist or is not ready in production", caller.Slug, grant.Bucket, grant.App))
	owner, ok := s.workloadGrantOwner(r, acct, caller, grant.App)
	if !ok {
		return state.ObjectBucket{}, missing
	}
	bucket, err := s.object.WorkloadBucket(r.Context(), acct, owner, grant.Bucket)
	if errors.Is(err, object.ErrWorkloadBucketNotFound) {
		return state.ObjectBucket{}, missing
	}
	if err != nil {
		return state.ObjectBucket{}, api.ErrInternal("could not load the bucket; retry shortly")
	}
	return bucket, nil
}

func (s *server) workloadGrantOwner(r *http.Request, acct state.Account, caller state.App, name string) (state.App, bool) {
	ctx := r.Context()
	if name == capgrant.Self {
		return caller, true
	}
	if caller.ProjectID != "" {
		if apps, err := s.store.AppsForProject(ctx, caller.AccountID, caller.ProjectID); err == nil {
			for _, app := range apps {
				if strings.EqualFold(app.WorkloadName, name) && app.Status != state.AppDeleted {
					return app, true
				}
			}
		}
	}
	app, err := s.store.AppBySlug(ctx, name)
	if err != nil || app.Status == state.AppDeleted {
		return state.App{}, false
	}
	owner, err := state.AppResourceAccount(ctx, s.store, app)
	return app, err == nil && owner.ID == acct.ID
}
