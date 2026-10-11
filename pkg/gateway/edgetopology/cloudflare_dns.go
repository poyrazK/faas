package edgetopology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrDNSUnverified = errors.New("cloudflare DNS configuration unverified")

// CloudflareZoneRef is an explicitly reviewed provider zone, not discovered
// from token visibility, public DNS, application rows or a hostname filter.
type CloudflareZoneRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DNSRecordConfig inventories every configured record. Target is interpreted
// only for A/AAAA/CNAME/NS; other content and all metadata remain digest-only.
// Proxied and TTL describe provider configuration, not effective traffic paths
// or a cache-expiry/deletion lease. An empty Target is never safe exclusion.
type DNSRecordConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	ConfigSHA256 string `json:"config_sha256"`
	TTL          int    `json:"ttl"`
	Proxied      *bool  `json:"proxied,omitempty"`
	Target       string `json:"target,omitempty"`
}

// CloudflareDNSInventory is provider configuration evidence for ONE reviewed
// zone. It is not authoritative served-DNS, parent delegation, CDN/Worker/load
// balancer routing, external CNAME resolution or native origin/socket proof.
type CloudflareDNSInventory struct {
	Zone         CloudflareZoneRef `json:"zone"`
	ZoneSHA256   string            `json:"zone_sha256"`
	ConfigSHA256 string            `json:"config_sha256"`
	NameServers  []string          `json:"name_servers"`
	Records      []DNSRecordConfig `json:"records"`
	CheckedAt    time.Time         `json:"checked_at"`
}

type CloudflareDNSProbe struct {
	zone           CloudflareZoneRef
	baseURL, token string
	// collectionMaxBytes is api.RuntimeUpgradeDNSCollectionMaxBytes; tests
	// lower it so the budget is reached in a few pages, not ~21.
	collectionMaxBytes int64
}

// NewCloudflareDNSProbe uses only the official HTTPS API with normal TLS
// validation. Supply a zone-scoped DNS Read token; this adapter has
// no mutation methods, environment proxy, redirects, retry or response cache.
func NewCloudflareDNSProbe(zone CloudflareZoneRef, token string) (*CloudflareDNSProbe, error) {
	if !dnsProviderID(zone.ID) || !dnsName(zone.Name, false) || !dnsAPIToken(token) {
		return nil, fmt.Errorf("%w: canonical reviewed zone and private API token required", ErrDNSUnverified)
	}
	return &CloudflareDNSProbe{zone: zone, token: token, baseURL: "https://api.cloudflare.com/client/v4", collectionMaxBytes: int64(api.RuntimeUpgradeDNSCollectionMaxBytes)}, nil
}

// Collect brackets two complete unfiltered record scans with exact zone-detail
// reads. Equal results detect observed drift; they are not a provider spanning
// transaction, an ABA fence, a future lease or whole-platform completeness.
// Any error returns zero inventory, including failure in a later page/scan.
func (p *CloudflareDNSProbe) Collect(ctx context.Context) (CloudflareDNSInventory, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradePublicEdgeTopologyTimeout)
	defer cancel()
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: api.RuntimeUpgradeIngressProbeTimeout, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer t.CloseIdleConnections()
	budget := p.collectionMaxBytes
	before, nameservers, err := p.readZone(ctx, t, &budget)
	if err != nil {
		return CloudflareDNSInventory{}, err
	}
	records, err := p.readRecords(ctx, t, &budget)
	if err != nil {
		return CloudflareDNSInventory{}, err
	}
	again, err := p.readRecords(ctx, t, &budget)
	if err != nil {
		return CloudflareDNSInventory{}, err
	}
	after, _, err := p.readZone(ctx, t, &budget)
	if err != nil {
		return CloudflareDNSInventory{}, err
	}
	firstDigest := dnsInventoryDigest(before, records)
	if before != after || firstDigest != dnsInventoryDigest(after, again) || ctx.Err() != nil {
		return CloudflareDNSInventory{}, fmt.Errorf("%w: zone or complete record set changed during collection", ErrDNSUnverified)
	}
	return CloudflareDNSInventory{Zone: p.zone, ZoneSHA256: before, ConfigSHA256: firstDigest, NameServers: nameservers, Records: records, CheckedAt: time.Now().UTC()}, nil
}

func (p *CloudflareDNSProbe) readZone(ctx context.Context, t *http.Transport, budget *int64) (string, []string, error) {
	envelope, err := p.get(ctx, t, "/zones/"+p.zone.ID, budget)
	if err != nil {
		return "", nil, err
	}
	nameservers, err := parseDNSZone(envelope["result"], p.zone)
	if err != nil {
		return "", nil, err
	}
	return configDigest(envelope["result"]), nameservers, nil
}

func (p *CloudflareDNSProbe) readRecords(ctx context.Context, t *http.Transport, budget *int64) ([]DNSRecordConfig, error) {
	records := make([]DNSRecordConfig, 0)
	ids := make(map[string]bool)
	pages, total := 1, -1
	for page := 1; page <= pages; page++ {
		path := "/zones/" + p.zone.ID + "/dns_records?order=name&direction=asc&include_shadow_metadata=true&per_page=" + strconv.Itoa(api.RuntimeUpgradeDNSPageSize) + "&page=" + strconv.Itoa(page)
		envelope, err := p.get(ctx, t, path, budget)
		if err != nil {
			return nil, err
		}
		rows, count, last, err := parseDNSPage(envelope, page)
		if err != nil || (total != -1 && total != count) {
			return nil, fmt.Errorf("%w: inconsistent complete pagination", ErrDNSUnverified)
		}
		pages, total = last, count
		for _, raw := range rows {
			record, err := parseDNSRecord(raw, p.zone)
			if err != nil {
				return nil, err
			}
			if ids[record.ID] || len(records) >= api.RuntimeUpgradeDNSRecordLimit {
				return nil, fmt.Errorf("%w: duplicate record or inventory bound exceeded", ErrDNSUnverified)
			}
			ids[record.ID] = true
			records = append(records, record)
		}
	}
	if len(records) != total {
		return nil, fmt.Errorf("%w: incomplete record inventory", ErrDNSUnverified)
	}
	slices.SortFunc(records, func(a, b DNSRecordConfig) int { return strings.Compare(a.ID, b.ID) })
	return records, nil
}

func (p *CloudflareDNSProbe) get(ctx context.Context, t *http.Transport, path string, budget *int64) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeIngressProbeTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, ErrDNSUnverified
	}
	r.Header.Set("Authorization", "Bearer "+p.token)
	r.Header.Set("Cache-Control", "no-cache")
	r.Header.Set("Accept", "application/json")
	resp, err := t.RoundTrip(r)
	if err != nil {
		// No provider bodies, token values or arbitrary transport error text.
		return nil, fmt.Errorf("%w: provider request failed", ErrDNSUnverified)
	}
	defer func() { _ = resp.Body.Close() }()
	limit := min(int64(api.RuntimeUpgradeDNSResponseMaxBytes), *budget)
	if limit < 1 || resp.StatusCode != http.StatusOK || resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: provider status or response budget", ErrDNSUnverified)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit || ctx.Err() != nil {
		return nil, fmt.Errorf("%w: bounded provider response required", ErrDNSUnverified)
	}
	*budget -= int64(len(body))
	return parseDNSEnvelope(body)
}

func dnsInventoryDigest(zoneDigest string, records []DNSRecordConfig) string {
	body, _ := json.Marshal(struct {
		Version string            `json:"version"`
		Zone    string            `json:"zone_sha256"`
		Records []DNSRecordConfig `json:"records"`
	}{"adr618/cloudflare-zone-config-v1", zoneDigest, records})
	return configDigest(body)
}
