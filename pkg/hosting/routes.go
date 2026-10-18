package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Route struct {
	Version         int      `json:"version"`
	PreviewAllowed  bool     `json:"preview_allowed,omitempty"`
	Enabled         bool     `json:"enabled"`
	Hostname        string   `json:"hostname"`
	SiteID          string   `json:"site_id"`
	OriginID        string   `json:"origin_id"`
	ActiveReleaseID string   `json:"active_release_id"`
	ReadyReleases   []string `json:"ready_releases"`
	Revision        int64    `json:"revision"`
	ExpiresAt       int64    `json:"expires_at"`
	Generation      int64    `json:"generation,omitempty"`
	Deleted         bool     `json:"deleted"`
}

type RoutePublisher interface {
	Put(context.Context, Route) error
}

// KV publishes expiring routing read models. PostgreSQL remains authoritative;
// a crash or missed notify is repaired by a complete, paginated reconciliation.
type KV struct {
	account, namespace, token string
	client                    *http.Client
}

func NewKV(account, namespace, token string) (*KV, error) {
	if !hexID(account) || !hexID(namespace) || token == "" {
		return nil, errors.New("hosting requires a Cloudflare account, KV namespace, and scoped token")
	}
	return &KV{account: account, namespace: namespace, token: token, client: &http.Client{Timeout: api.HostingStorageTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func hexID(v string) bool {
	if len(v) != 32 {
		return false
	}
	for _, c := range v {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return false
	}
	return true
}

func (p *KV) Put(ctx context.Context, route Route) error {
	data, err := json.Marshal(route)
	if err != nil {
		return err
	}
	endpoint := "https://api.cloudflare.com/client/v4/accounts/" + p.account + "/storage/kv/namespaces/" + p.namespace + "/values/" + url.PathEscape("site:"+route.Hostname)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	r, err := p.client.Do(req)
	if err != nil {
		return errors.New("hosting route publication unavailable")
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return errors.New("hosting route publication rejected")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&result); err != nil || !result.Success {
		return errors.New("hosting route publication not acknowledged")
	}
	return nil
}

func NewRoute(site api.HostingSite, releases []api.HostingRelease, enabled bool, now time.Time) Route {
	r := Route{Version: 1, Enabled: enabled && site.Status == "active" && !site.Suspended, Hostname: site.Hostname, SiteID: site.ID, OriginID: site.OriginID, ActiveReleaseID: site.ActiveReleaseID, Revision: site.Revision, ExpiresAt: now.Add(api.HostingRouteLease).Unix(), ReadyReleases: []string{}}
	for _, release := range releases {
		if release.SiteID == site.ID && release.State == "ready" && (release.FirstPromotedAt != nil || release.ID == site.ActiveReleaseID) {
			r.ReadyReleases = append(r.ReadyReleases, release.ID)
		}
	}
	sort.Strings(r.ReadyReleases)
	return r
}

func NewDurableRoute(site api.HostingSite, releases []api.HostingRelease, generation int64) Route {
	r := NewRoute(site, releases, true, time.Time{})
	r.Version, r.Generation, r.ExpiresAt = 2, generation, 0
	r.Deleted = site.Status == "deleting"
	r.PreviewAllowed = site.Status == "active" && !site.Suspended
	r.Enabled = r.Enabled && r.ActiveReleaseID != "" && !r.Deleted
	return r
}
