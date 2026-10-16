// adr:1362 — create from an explicit review; reconcile without admitting work.
package bindingrun

import (
	"context"
	"encoding/hex"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type CreationOptions struct {
	// Nil uses the default bounded status read retry budget; zero disables extra reads.
	StatusReadRetries  *int
	OnProgress         func(Progress) error
	RunID              string
	TTLSeconds         int
	MaxConcurrency     int
	ObjectStorageWrite bool
	QueuePublish       bool
}

// CreationError preserves the caller's identity and original cause. Attempted
// means create was invoked; it does not establish whether creation committed.
type CreationError struct {
	RunID     string
	Attempted bool
	Err       error
}

func (e *CreationError) Error() string {
	return fmt.Sprintf("verification creation %s failed; inspect this run ID: %v", e.RunID, e.Err)
}
func (e *CreationError) Unwrap() error { return e.Err }

type CreationClient interface {
	CreateBindingVerificationRun(context.Context, string, api.CreateBindingVerificationRunRequest) (api.BindingVerificationRun, error)
	GetBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error)
	CreateProjectBindingVerificationRun(context.Context, string, string, api.CreateProjectBindingVerificationRunRequest) (api.ProjectBindingVerificationRun, error)
	GetProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error)
}

// Clone the mutable selection fields without changing nil slices or timestamps.
func freezeAppCreationReview(r api.BindingVerificationReview) api.BindingVerificationReview {
	r.ServiceTargets = maps.Clone(r.ServiceTargets)
	r.Plan = slices.Clone(r.Plan)
	for i := range r.Plan {
		p := &r.Plan[i]
		p.Operations = slices.Clone(p.Operations)
		if p.QueueEnvironment != nil {
			v := *p.QueueEnvironment
			p.QueueEnvironment = &v
		}
		if p.CheckedAt != nil {
			v := *p.CheckedAt
			p.CheckedAt = &v
		}
	}
	return r
}
func freezeGraphCreationReview(r api.ProjectBindingVerificationReview) api.ProjectBindingVerificationReview {
	r.ReviewDigests = maps.Clone(r.ReviewDigests)
	r.Members = maps.Clone(r.Members)
	for n, m := range r.Members {
		r.Members[n] = freezeAppCreationReview(m)
	}
	m := &r.Manifest
	m.Deployments = maps.Clone(m.Deployments)
	m.ExpectedFallbackDeployments = maps.Clone(m.ExpectedFallbackDeployments)
	m.ExpectedPolicyRevisions = maps.Clone(m.ExpectedPolicyRevisions)
	m.ServiceTargets = maps.Clone(m.ServiceTargets)
	for n, t := range m.ServiceTargets {
		m.ServiceTargets[n] = maps.Clone(t)
	}
	if m.ExpectedActiveReleaseID != nil {
		v := *m.ExpectedActiveReleaseID
		m.ExpectedActiveReleaseID = &v
	}
	return r
}
func validDigest(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func validateCreation(o CreationOptions) error {
	if o.StatusReadRetries != nil && (*o.StatusReadRetries < 0 || *o.StatusReadRetries > api.BindingVerificationRunnerMaxStatusReadRetries) {
		return fmt.Errorf("status read retries must be between zero and eight")
	}
	if !validID(o.RunID) || o.TTLSeconds < 1 || o.TTLSeconds > api.BindingVerificationRunMaxTTLSeconds || o.MaxConcurrency < 0 || o.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency {
		return fmt.Errorf("invalid run UUID, lifetime or concurrency")
	}
	return nil
}
func validateReview(r api.BindingVerificationReview, app string, o CreationOptions) error {
	if !validID(canonicalDeploymentID(r.DeploymentID)) || r.App != app || app == "" || r.Scope == "" || !r.Ready || len(r.Blockers) != 0 || !validDigest(r.ReviewDigest) || r.ReviewedAt.IsZero() || r.ReviewedAt.After(time.Now()) || len(r.Plan) > api.BindingVerificationRunMaxSteps || r.ObjectStorageWriteRequired && !o.ObjectStorageWrite || r.QueuePublishRequired && !o.QueuePublish || r.Policy.Scope != r.Scope || r.Inventory.VerificationDeploymentID != r.DeploymentID || !maps.Equal(r.Inventory.ServiceTargets, r.ServiceTargets) {
		return fmt.Errorf("review selection, readiness or explicit consent was not confirmed")
	}
	return nil
}
func validateProjectReview(r api.ProjectBindingVerificationReview, p, env string, o CreationOptions) error {
	if r.Project != p || r.Environment != env || p == "" || env == "" || r.Manifest.Project != p || r.Manifest.Environment != env || !r.Ready || len(r.Blockers) != 0 || !validDigest(r.ReviewDigest) || r.ReviewedAt.IsZero() || r.ReviewedAt.After(time.Now()) || r.ObjectStorageWrite != o.ObjectStorageWrite || r.QueuePublish != o.QueuePublish || r.ObjectStorageWriteRequired && !o.ObjectStorageWrite || r.QueuePublishRequired && !o.QueuePublish || len(r.Members) == 0 || len(r.Members) > api.ProjectReleaseSetMaxMembers || len(r.Members) != len(r.Manifest.Deployments) || len(r.ReviewDigests) != len(r.Members) {
		return fmt.Errorf("graph review selection, readiness or explicit consent was not confirmed")
	}
	for name, m := range r.Members {
		if e := validateReview(m, name, o); e != nil {
			return e
		}
		if m.Scope != env || canonicalDeploymentID(m.DeploymentID) != canonicalDeploymentID(r.Manifest.Deployments[name]) || m.ReviewDigest != r.ReviewDigests[name] || m.Policy.Revision != r.Manifest.ExpectedPolicyRevisions[name] || !maps.Equal(m.ServiceTargets, r.Manifest.ServiceTargets[name]) {
			return fmt.Errorf("graph member selection was not confirmed")
		}
	}
	return nil
}
func validCreated(r api.BindingVerificationRun, review api.BindingVerificationReview, o CreationOptions) bool {
	if r.ID != o.RunID || r.App != review.App || canonicalDeploymentID(r.DeploymentID) != canonicalDeploymentID(review.DeploymentID) || r.Scope != review.Scope || r.ReviewDigest != review.ReviewDigest || r.ObjectStorageWrite != o.ObjectStorageWrite || r.QueuePublish != o.QueuePublish || api.BindingVerificationConcurrency(r.MaxConcurrency) != api.BindingVerificationConcurrency(o.MaxConcurrency) || r.Policy.Revision != review.Policy.Revision || !maps.Equal(r.Inventory.ServiceTargets, review.ServiceTargets) || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now()) || r.ExpiresAt.Sub(r.CreatedAt) != time.Duration(o.TTLSeconds)*time.Second || len(r.Steps) != len(review.Plan) {
		return false
	}
	for i, s := range r.Steps {
		if !reflect.DeepEqual(s.Plan, review.Plan[i]) {
			return false
		}
	}
	return true
}
func validProjectCreated(r api.ProjectBindingVerificationRun, review api.ProjectBindingVerificationReview, o CreationOptions) bool {
	if r.ID != o.RunID || r.Project != review.Project || r.Environment != review.Environment || r.ReviewDigest != review.ReviewDigest || !reflect.DeepEqual(r.Manifest, review.Manifest) || r.ObjectStorageWrite != o.ObjectStorageWrite || r.QueuePublish != o.QueuePublish || api.BindingVerificationConcurrency(r.MaxConcurrency) != api.BindingVerificationConcurrency(o.MaxConcurrency) || len(r.Members) != len(review.Members) {
		return false
	}
	for name, m := range r.Members {
		expected, ok := review.Members[name]
		child := o
		child.RunID = m.ID
		if !ok || !validID(m.ID) || !validCreated(m, expected, child) || m.GraphRunID != r.ID || !m.CreatedAt.Equal(r.CreatedAt) || !m.ExpiresAt.Equal(r.ExpiresAt) {
			return false
		}
	}
	return true
}

func CreateApp(ctx context.Context, c CreationClient, app string, r api.BindingVerificationReview, o CreationOptions) (api.BindingVerificationRun, error) {
	return createApp(ctx, c, app, r, o, false)
}

// ReconcileApp performs exactly one GET, including for expired or terminal receipts.
func ReconcileApp(ctx context.Context, c CreationClient, app string, r api.BindingVerificationReview, o CreationOptions) (api.BindingVerificationRun, error) {
	return createApp(ctx, c, app, r, o, true)
}
func createApp(ctx context.Context, c CreationClient, app string, r api.BindingVerificationReview, o CreationOptions, read bool) (api.BindingVerificationRun, error) {
	var out api.BindingVerificationRun
	e := ctx.Err()
	if e == nil {
		e = validateCreation(o)
	}
	if e == nil {
		e = validateReview(r, app, o)
	}
	if e != nil {
		return out, &CreationError{RunID: o.RunID, Err: e}
	}
	r = freezeAppCreationReview(r)
	if o.StatusReadRetries != nil {
		n := *o.StatusReadRetries
		o.StatusReadRetries = &n
	}
	began := time.Now()
	if read {
		out, e = c.GetBindingVerificationRun(ctx, app, o.RunID)
	} else {
		out, e = c.CreateBindingVerificationRun(ctx, app, api.CreateBindingVerificationRunRequest{ID: o.RunID, DeploymentID: r.DeploymentID, ServiceTargets: maps.Clone(r.ServiceTargets), ReviewDigest: r.ReviewDigest, TTLSeconds: o.TTLSeconds, MaxConcurrency: o.MaxConcurrency, ObjectStorageWrite: o.ObjectStorageWrite, QueuePublish: o.QueuePublish})
	}

	if !read && e != nil && statusReadRetryable(e) {
		recovered, err := reconcileCreation(ctx, e, began, o, func(ctx context.Context, _ bool) (snapshot, error) {
			v, err := c.GetBindingVerificationRun(ctx, app, o.RunID)
			return snapshot{app: &v}, err
		}, func(s snapshot) error {
			if !validCreated(*s.app, r, o) || s.app.GraphRunID != "" {
				return fmt.Errorf("server returned another receipt or reviewed selection")
			}
			return nil
		})
		e = err
		if e == nil {
			out = *recovered.app
		}
	}
	if e == nil && (!validCreated(out, r, o) || out.GraphRunID != "") {
		e = fmt.Errorf("server returned another receipt or reviewed selection")
	}
	if e != nil {
		return api.BindingVerificationRun{}, &CreationError{RunID: o.RunID, Attempted: !read, Err: e}
	}
	return out, nil
}
func CreateProject(ctx context.Context, c CreationClient, p, env string, r api.ProjectBindingVerificationReview, o CreationOptions) (api.ProjectBindingVerificationRun, error) {
	return createProject(ctx, c, p, env, r, o, false)
}
func ReconcileProject(ctx context.Context, c CreationClient, p, env string, r api.ProjectBindingVerificationReview, o CreationOptions) (api.ProjectBindingVerificationRun, error) {
	return createProject(ctx, c, p, env, r, o, true)
}
func createProject(ctx context.Context, c CreationClient, p, env string, r api.ProjectBindingVerificationReview, o CreationOptions, read bool) (api.ProjectBindingVerificationRun, error) {
	var out api.ProjectBindingVerificationRun
	e := ctx.Err()
	if e == nil {
		e = validateCreation(o)
	}
	if e == nil {
		e = validateProjectReview(r, p, env, o)
	}
	if e != nil {
		return out, &CreationError{RunID: o.RunID, Err: e}
	}
	r = freezeGraphCreationReview(r)
	if o.StatusReadRetries != nil {
		n := *o.StatusReadRetries
		o.StatusReadRetries = &n
	}
	began := time.Now()
	if read {
		out, e = c.GetProjectBindingVerificationRun(ctx, p, env, o.RunID)
	} else {
		out, e = c.CreateProjectBindingVerificationRun(ctx, p, env, api.CreateProjectBindingVerificationRunRequest{ID: o.RunID, Manifest: r.Manifest, ReviewDigests: maps.Clone(r.ReviewDigests), ReviewDigest: r.ReviewDigest, TTLSeconds: o.TTLSeconds, MaxConcurrency: o.MaxConcurrency, ObjectStorageWrite: o.ObjectStorageWrite, QueuePublish: o.QueuePublish})
	}

	if !read && e != nil && statusReadRetryable(e) {
		recovered, err := reconcileCreation(ctx, e, began, o, func(ctx context.Context, _ bool) (snapshot, error) {
			v, err := c.GetProjectBindingVerificationRun(ctx, p, env, o.RunID)
			return snapshot{graph: &v}, err
		}, func(s snapshot) error {
			if !validProjectCreated(*s.graph, r, o) {
				return fmt.Errorf("server returned another graph receipt or reviewed selection")
			}
			return nil
		})
		e = err
		if e == nil {
			out = *recovered.graph
		}
	}
	if e == nil && !validProjectCreated(out, r, o) {
		e = fmt.Errorf("server returned another graph receipt or reviewed selection")
	}
	if e != nil {
		return api.ProjectBindingVerificationRun{}, &CreationError{RunID: o.RunID, Attempted: !read, Err: e}
	}
	return out, nil
}
