package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type HostingDomainLease struct {
	Domain api.HostingDomain
	Token  string
}
type HostingDomainStore interface {
	RecoverHostingDomain(context.Context, string, string, string, api.RecoverHostingDomainRequest, time.Time) (api.HostingDomain, error)
	CreateHostingDomain(context.Context, api.HostingDomain) (api.HostingDomain, error)
	ListHostingDomains(context.Context, string, string) ([]api.HostingDomain, error)
	DeleteHostingDomain(context.Context, string, string, string, int64) (api.HostingDomain, error)
	ClaimHostingDomains(context.Context, time.Time, string) ([]HostingDomainLease, error)
	SaveHostingDomain(context.Context, HostingDomainLease, string, api.HostingDomainDetails) (HostingDomainLease, error)
	AcknowledgeHostingDomain(context.Context, HostingDomainLease, time.Time) error
	FinishHostingDomain(context.Context, HostingDomainLease, time.Time, string) error
	ReleaseHostingDomainBatch(context.Context, string) error
	PurgeHostingDomain(context.Context, HostingDomainLease, time.Time) error
}

func validHostingDomainDetails(d api.HostingDomainDetails) bool {
	data, err := json.Marshal(d)
	if err != nil || len(data) > api.HostingDomainDetailsMaxBytes || len(d.Records) > api.HostingDomainRecordsMax {
		return false
	}
	if d.TLSStatus != "pending" && d.TLSStatus != "active" && d.TLSStatus != "failed" {
		return false
	}
	if d.AccessAUD != "" && !hosting.ValidDigest(d.AccessAUD) {
		return false
	}
	if d.AccessID != "" && uuid.Validate(d.AccessID) != nil || d.ProviderID != "" && uuid.Validate(d.ProviderID) != nil {
		return false
	}
	for _, r := range d.Records {
		if r.Type != "TXT" && r.Type != "CNAME" || len(r.Name) < 1 || len(r.Name) > 253 || len(r.Value) < 1 || len(r.Value) > api.HostingDomainRecordMaxBytes {
			return false
		}
	}
	return true
}

func validHostingDomainInput(d api.HostingDomain) bool {
	host, err := hosting.NormalizeDomain(d.Hostname, nil)
	return err == nil && host == d.Hostname && uuid.Validate(d.ID) == nil && uuid.Validate(d.AccountID) == nil && uuid.Validate(d.SiteID) == nil && hosting.ValidDigest(d.Challenge) && hosting.ValidDigest(d.ProviderOriginID)
}

func validHostingDomainError(code string) bool {
	return code == "" || code == "dns_pending" || code == "provider_unavailable" || code == "provider_conflict" || code == "publication_unavailable" || code == "origin_unavailable"
}

func validHostingDomainState(status string, d api.HostingDomainDetails) bool {
	if !validHostingDomainDetails(d) {
		return false
	}
	switch status {
	case "pending_dns", "pending_certificate", "deleting":
		return true
	case "active":
		return d.VerifiedAt != nil && !d.VerifiedAt.IsZero() && d.ProviderID != "" && d.AccessID != "" && hosting.ValidDigest(d.AccessAUD) && d.TLSStatus == "active"
	default:
		return false
	}
}

func hostingDomainNext(d api.HostingDomain, at time.Time, code string) time.Time {
	if code == "" && d.State == "active" && d.PublishedGeneration == d.Generation {
		return at.Add(api.HostingDomainCheckInterval)
	}
	return at.Add(api.HostingDomainRetryInterval)
}

func cloneHostingDomain(d api.HostingDomain) api.HostingDomain {
	d.Details.Records = append([]api.HostingDNSRecord{}, d.Details.Records...)
	if d.PublishedAt != nil {
		at := *d.PublishedAt
		d.PublishedAt = &at
	}
	if d.Details.VerifiedAt != nil {
		at := *d.Details.VerifiedAt
		d.Details.VerifiedAt = &at
	}
	if d.Details.CreateStartedAt != nil {
		at := *d.Details.CreateStartedAt
		d.Details.CreateStartedAt = &at
	}
	if d.Details.ValidationRetriedAt != nil {
		at := *d.Details.ValidationRetriedAt
		d.Details.ValidationRetriedAt = &at
	}
	return d
}
