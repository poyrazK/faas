package hosting

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type RetentionReconciler struct {
	Store     state.HostingRetentionStore
	OriginID  string
	Enabled   func() bool
	Authorize func(context.Context, string) (bool, error)
	Audit     func(context.Context, string, string, []string)
	Now       func() time.Time
	after     string
}

// A bounded keyset pass resumes after the last inspected site, including failed
// sites. Disabling admission never prevents the existing artifact GC worker.
func (r *RetentionReconciler) RunOnce(ctx context.Context) error {
	if r.Enabled == nil || !r.Enabled() || r.Authorize == nil {
		return nil
	}
	work, cancel := context.WithTimeout(ctx, api.HostingRetentionPassTimeout)
	defer cancel()
	sites, err := r.Store.ListHostingRetentionSites(work, r.after, api.HostingRetentionSitesPerPass)
	if err != nil {
		return err
	}
	var failures []error
	for _, site := range sites {
		if work.Err() != nil {
			return errors.Join(append(failures, work.Err())...)
		}
		if !r.Enabled() {
			return errors.Join(failures...)
		}
		r.after = site.ID
		if site.OriginID != r.OriginID {
			continue
		}
		allowed, err := r.Authorize(work, site.AccountID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !allowed {
			continue
		}
		now := time.Now().UTC()
		if r.Now != nil {
			now = r.Now()
		}
		ids, err := r.Store.ApplyHostingRetention(work, site.AccountID, site.ID, r.OriginID, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if len(ids) > 0 && r.Audit != nil {
			r.Audit(work, site.AccountID, site.ID, ids)
		}
	}
	if len(sites) < api.HostingRetentionSitesPerPass {
		r.after = ""
	}
	return errors.Join(failures...)
}
