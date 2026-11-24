package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Upgrade links (ADR-20261124094745): a consumer refused by its plan's
// limit gets the app's hosted upgrade page in the problem's upgrade_url.
// apid mints the link; the gateway reuses one per consumer for a while and
// leaves the field out when apid cannot answer quickly.

// ConsumerUpgradeLinker asks apid for a consumer's upgrade link:
// apidgrpc.OAuthConsumers. "" means the app offers no upgrade.
type ConsumerUpgradeLinker interface {
	UpgradeLink(ctx context.Context, accountID, appID, consumerID string) (string, time.Time, error)
}

// upgradeLinkTimeout bounds the apid call on a refusal.
const upgradeLinkTimeout = time.Second

type upgradeLinkEntry struct {
	url     string
	expires time.Time
}

type upgradeLinkCache struct {
	mu      sync.Mutex
	entries map[string]upgradeLinkEntry
}

// WithUpgradeLinks adds upgrade links to plan-limit refusals.
func (h *Handler) WithUpgradeLinks(l ConsumerUpgradeLinker) *Handler {
	h.upgradeLinks, h.upgradeLinkCache = l, &upgradeLinkCache{}
	return h
}

// consumerUpgradeURL returns the consumer's upgrade link, or "".
func (h *Handler) consumerUpgradeURL(ctx context.Context, app App, consumerID string) string {
	if h.upgradeLinks == nil || consumerID == "" {
		return ""
	}
	now := time.Now()
	c := h.upgradeLinkCache
	c.mu.Lock()
	e, ok := c.entries[consumerID]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.url
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upgradeLinkTimeout)
	defer cancel()
	link, linkExpires, err := h.upgradeLinks.UpgradeLink(ctx, app.AccountID, app.ID, consumerID)
	if err != nil {
		if h.log != nil {
			h.log.Warn("consumer upgrade link unavailable", "err", err, "app_id", app.ID)
		}
		return ""
	}
	expires := now.Add(api.ConsumerUpgradeLinkCacheTTL)
	// Keep a link well inside its own lifetime, so a cached one always opens.
	if link != "" && !linkExpires.IsZero() && linkExpires.Add(-time.Hour).Before(expires) {
		expires = linkExpires.Add(-time.Hour)
	}
	c.mu.Lock()
	if c.entries == nil || len(c.entries) >= oauthConsumerCacheMax {
		c.entries = map[string]upgradeLinkEntry{}
	}
	c.entries[consumerID] = upgradeLinkEntry{url: link, expires: expires}
	c.mu.Unlock()
	return link
}
