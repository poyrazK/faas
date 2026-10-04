package globaldirectory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Recovery struct {
	Service *DelegationService
	OnError func()
}

func (w Recovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(api.GlobalRecoveryInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.Once(ctx); err != nil && w.OnError != nil {
			w.OnError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (w Recovery) Once(ctx context.Context) error {
	if w.Service == nil || !w.Service.Keys.Available() {
		return ErrDelegationSealed
	}
	work, err := w.Service.Journal.ClaimRecovery(ctx, uuid.NewString())
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	failures := make(chan error, len(work))
	// Claim at most the execution concurrency: queued apps cannot outlive a lease.
	for _, job := range work {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- w.app(ctx, job)
		}()
	}
	wg.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (w Recovery) app(ctx context.Context, job RecoveryWork) error {
	c := w.Service
	workCtx, cancel := context.WithTimeout(ctx, api.GlobalRecoveryAppTimeout)
	defer cancel()
	grants, listErr := c.Journal.ListRecoveryDelegations(workCtx, job.AccountID, job.GlobalAppID)
	// A durable cursor gives every retained grant a bounded turn during outages.
	sort.Slice(grants, func(i, j int) bool { return grants[i].ID < grants[j].ID })
	start := sort.Search(len(grants), func(i int) bool { return grants[i].ID > job.CursorID })
	n := min(len(grants), api.GlobalRecoveryDelegationsPerApp)
	batch := make([]Delegation, 0, n)
	for i := range n {
		batch = append(batch, grants[(start+i)%len(grants)])
	}
	if n > 0 {
		job.CursorID = batch[n-1].ID
	}
	// Revocation and deployment have separate workers within the same app deadline.
	revoked := make(chan error, 1)
	go func() { revoked <- w.grants(workCtx, batch) }()
	var releaseErr error
	if job.ReleaseID != "" {
		if releaseErr = c.authorize(workCtx, job.AccountID); releaseErr == nil {
			app, err := c.Store.AppBySlug(workCtx, job.AccountID, job.Slug)
			if err == nil && app.ID == job.GlobalAppID {
				r := Reconciler{Store: c.Store, Verifier: c.Verifier, Delegations: c}
				_, releaseErr = r.Resume(workCtx, job.AccountID, job.Slug, job.ReleaseID, nil)
			} else if !errors.Is(err, ErrNotFound) {
				releaseErr = err
			}
		} else if errors.Is(releaseErr, ErrDelegationOwnerInactive) {
			releaseErr = nil
		}
	}
	grantErr := <-revoked
	var finishErr error
	// Cancellation retains the lease. A fresh wake is never delayed by old work.
	if ctx.Err() == nil {
		finishErr = c.Journal.FinishRecovery(ctx, job)
	}
	return errors.Join(listErr, releaseErr, grantErr, finishErr)
}
func (w Recovery) grants(ctx context.Context, batch []Delegation) error {
	jobs := make(chan Delegation, len(batch))
	failures := make(chan error, len(batch))
	for _, g := range batch {
		jobs <- g
	}
	close(jobs)
	var wg sync.WaitGroup
	for range api.GlobalRecoveryConcurrentRevocations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				failures <- w.grant(ctx, g)
			}
		}()
	}
	wg.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (w Recovery) grant(ctx context.Context, g Delegation) error {
	c := w.Service
	if !g.ExpiresAt.After(time.Now()) {
		return c.Journal.SetDelegationStatus(ctx, g.AccountID, g.GlobalAppID, g.ID, DelegationExpired)
	}
	if g.Status != DelegationRevoking {
		authErr := c.authorize(ctx, g.AccountID)
		if authErr != nil && !errors.Is(authErr, ErrDelegationOwnerInactive) {
			return authErr
		}
		identityErr := c.Journal.CheckDelegation(ctx, g.AccountID, g.GlobalAppID, g.ID)
		stale := errors.Is(identityErr, ErrStaleCell) || errors.Is(identityErr, ErrNotFound)
		if identityErr != nil && !stale {
			return identityErr
		}
		if errors.Is(authErr, ErrDelegationOwnerInactive) || stale {
			if err := c.Journal.SetDelegationStatus(ctx, g.AccountID, g.GlobalAppID, g.ID, DelegationRevoking); err != nil {
				return err
			}
			g.Status = DelegationRevoking
		}
	}
	if g.Status == DelegationRevoking {
		return c.Revoke(ctx, g)
	}
	return nil
}
