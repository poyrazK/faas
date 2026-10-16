package hosting

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sync/errgroup"
)

type DomainReconciler struct {
	Store state.HostingDomainStore
	Sites interface {
		GetHostingSite(context.Context, string, string) (api.HostingSite, error)
	}
	Provider  artifacts.DomainProvider
	Publisher artifacts.DomainRoutePublisher
	OriginID  string
	DNS       artifacts.DomainDNS
	Now       func() time.Time
}

func (r DomainReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r DomainReconciler) RunOnce(ctx context.Context) error {
	token := uuid.NewString()
	leases, err := r.Store.ClaimHostingDomains(ctx, r.now(), token)
	if err != nil {
		return err
	}
	defer func() {
		receipt, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostingRouteStatusReadTimeout)
		defer cancel()
		_ = r.Store.ReleaseHostingDomainBatch(receipt, token)
	}()
	work, cancel := context.WithTimeout(ctx, api.HostingDomainDispatchTimeout)
	defer cancel()
	deadline, _ := work.Deadline()
	var group errgroup.Group
	group.SetLimit(api.HostingDomainConcurrency)
	for _, lease := range leases {
		if work.Err() != nil || !time.Now().Before(deadline) {
			break
		}
		group.Go(func() error {
			if work.Err() != nil || !time.Now().Before(deadline) {
				return context.DeadlineExceeded
			}
			return r.reconcile(work, lease)
		})
	}
	return group.Wait()
}

func (r DomainReconciler) reconcile(ctx context.Context, lease state.HostingDomainLease) error {
	updated, code, err := r.apply(ctx, lease)
	receipt, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostingRouteStatusReadTimeout)
	defer cancel()
	// Fenced completion cannot postpone a deletion accepted during provider I/O.
	finishErr := r.Store.FinishHostingDomain(receipt, updated, r.now(), code)
	if errors.Is(finishErr, state.ErrConflict) && updated.Domain.State == "deleting" && err == nil {
		return nil
	}
	return errors.Join(err, finishErr)
}

func (r DomainReconciler) apply(ctx context.Context, lease state.HostingDomainLease) (state.HostingDomainLease, string, error) {
	d := lease.Domain
	site, err := r.Sites.GetHostingSite(ctx, d.AccountID, d.SiteID)
	if err != nil {
		return lease, "origin_unavailable", err
	}
	if site.OriginID != r.OriginID || !site.DurableRouting || r.Publisher == nil {
		return lease, "origin_unavailable", errors.New("hosting domain origin unavailable")
	}
	if d.State == "deleting" {
		return r.remove(ctx, lease, site)
	}
	if r.Provider == nil || d.ProviderOriginID != r.Provider.ID() {
		return lease, "origin_unavailable", errors.New("hosting domain provider configuration changed")
	}
	if site.Status != "active" {
		return lease, "origin_unavailable", errors.New("hosting site is deleting")
	}
	if d.Details.VerifiedAt == nil {
		dns := r.DNS
		if dns == nil {
			dns = net.DefaultResolver
		}
		lookup, cancel := context.WithTimeout(ctx, api.HostingDomainDNSTimeout)
		verified := artifacts.VerifyDomainDNS(lookup, dns, d.Hostname, d.Challenge, r.Provider.Target())
		cancel()
		if !verified {
			return lease, "dns_pending", nil
		}
		at := r.now()
		d.Details.VerifiedAt = &at
	}
	allowCreate := d.Details.ProviderID == "" && d.Details.CreateStartedAt == nil
	if allowCreate {
		at := r.now()
		d.Details.CreateStartedAt = &at
		// Persist before any provider create. A crash or lost receipt must not repeat it.
		prepared, saveErr := r.Store.SaveHostingDomain(ctx, lease, "pending_certificate", d.Details)
		if saveErr != nil {
			return lease, "provider_unavailable", saveErr
		}
		lease = prepared
		d = lease.Domain
	}
	details, ready, err := r.Provider.Ensure(ctx, d, allowCreate)
	if err != nil {
		code := "provider_unavailable"
		if errors.Is(err, artifacts.ErrDomainProviderConflict) {
			code = "provider_conflict"
		}
		status := lease.Domain.State
		if details.VerifiedAt != nil && (status == "pending_dns" || details.TLSStatus != "active" || details.AccessAUD == "") {
			status = "pending_certificate"
		}
		updated, saveErr := r.Store.SaveHostingDomain(ctx, lease, status, details)
		if saveErr == nil {
			if updated.Domain.State != "active" && updated.Domain.Generation != lease.Domain.Generation {
				if pubErr := r.publish(ctx, updated, site); pubErr != nil {
					return updated, "publication_unavailable", errors.Join(err, pubErr)
				}
			}
			return updated, code, err
		}
		return lease, code, errors.Join(err, saveErr)
	}
	status := "pending_certificate"
	if ready {
		status = "active"
	}
	updated, err := r.Store.SaveHostingDomain(ctx, lease, status, details)
	if err != nil {
		return lease, "provider_unavailable", err
	}
	if err = r.publish(ctx, updated, site); err != nil {
		return updated, "publication_unavailable", err
	}
	return updated, "", nil
}

func (r DomainReconciler) route(lease state.HostingDomainLease, site api.HostingSite) artifacts.DomainRoute {
	d := lease.Domain
	return artifacts.DomainRoute{Version: 1, ID: d.ID, Hostname: d.Hostname, SiteID: site.ID, CanonicalHostname: site.Hostname, OriginID: site.OriginID, AccessAUD: d.Details.AccessAUD, Generation: d.Generation, Enabled: d.State == "active", Deleted: d.State == "deleting"}
}
func (r DomainReconciler) publish(ctx context.Context, lease state.HostingDomainLease, site api.HostingSite) error {
	if err := r.Publisher.PutDomain(ctx, r.route(lease, site)); err != nil {
		return err
	}
	return r.Store.AcknowledgeHostingDomain(ctx, lease, r.now())
}
func (r DomainReconciler) remove(ctx context.Context, lease state.HostingDomainLease, site api.HostingSite) (state.HostingDomainLease, string, error) {
	if err := r.publish(ctx, lease, site); err != nil {
		return lease, "publication_unavailable", err
	}
	d := lease.Domain
	if d.PublishedGeneration != d.Generation || d.PublishedAt == nil || r.now().Sub(*d.PublishedAt) < api.HostingDomainRemovalGrace {
		return lease, "", nil
	}
	if r.Provider == nil || r.Provider.ID() != d.ProviderOriginID {
		return lease, "origin_unavailable", errors.New("hosting domain provider configuration changed")
	}
	if err := r.Provider.Remove(ctx, d); err != nil {
		code := "provider_unavailable"
		if errors.Is(err, artifacts.ErrDomainProviderConflict) {
			code = "provider_conflict"
		}
		return lease, code, err
	}
	err := r.Store.PurgeHostingDomain(ctx, lease, r.now())
	return lease, "", err
}
