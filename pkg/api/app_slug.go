package api

import (
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

var appSlugRE = lazyregexp.New(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

var reservedAppSlugs = map[string]struct{}{
	"account": {}, "admin": {}, "api": {}, "assets": {}, "billing": {},
	"cdn": {}, "console": {}, "dashboard": {}, "docs": {}, "help": {},
	"login": {}, "logout": {}, "mail": {}, "ns": {}, "operations": {},
	"security": {}, "signup": {}, "static": {}, "status": {}, "support": {},
	"www": {},
	// Hosts the platform zone serves (ADR-20261026100000): object storage,
	// the AI gateway, the edge, the installer, identity, the registry and the
	// edge origins.
	"s3": {}, "ai": {}, "edge": {}, "get": {}, "identity": {}, "registry": {},
	"origin": {}, "app-edge": {},
}

// ValidAppSlug is the canonical public app-name contract shared by direct
// app creation and project workload admission.
func ValidAppSlug(slug string) bool {
	return appSlugRE.MatchString(slug) && !strings.HasPrefix(slug, "tag-")
}

// IsPlatformReservedSlug reports names held for hosts the platform zone
// serves (IsReservedAppSlug without the preview forms). Such a label is never
// an app under any apps domain (ADR-20261026100000).
func IsPlatformReservedSlug(slug string) bool {
	_, ok := reservedAppSlugs[slug]
	return ok
}

// IsReservedAppSlug reports names held for Gregale-owned services. Existing
// rows remain readable so operators can migrate an accidental collision, but
// every public allocation path must reject these names.
func IsReservedAppSlug(slug string) bool {
	_, ok := reservedAppSlugs[slug]
	return ok || IsPreviewAppSlug(slug) || isOperatorReservedSlug(slug)
}

// operatorReservedSlugs holds the cell's own reserved labels
// (SetOperatorReservedAppSlugs).
var operatorReservedSlugs atomic.Pointer[map[string]struct{}]

// SetOperatorReservedAppSlugs reserves this cell's extra app names: the
// labels of the host names edgednsd manages under the apps domain, such as a
// direct-entry pilot host (ADR-20261107180000). A customer app with such a
// name would be served at that platform-managed host. Like the fixed list,
// it binds only allocation: an app that already has the name keeps
// routing. apid calls it once at start with its configured list.
func SetOperatorReservedAppSlugs(slugs []string) {
	set := make(map[string]struct{}, len(slugs))
	for _, s := range slugs {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			set[s] = struct{}{}
		}
	}
	operatorReservedSlugs.Store(&set)
}

func isOperatorReservedSlug(slug string) bool {
	set := operatorReservedSlugs.Load()
	if set == nil {
		return false
	}
	_, ok := (*set)[slug]
	return ok
}

// PreviewAppSlugMaxLen is the longest PR preview slug: one DNS label. The
// preview of a 40-character app is longer than ValidAppSlug allows.
const PreviewAppSlugMaxLen = 63

// ValidPreviewAppSlug reports a pr-<N>-<parent> slug whose parent is a valid
// app slug and that fits one DNS label.
func ValidPreviewAppSlug(slug string) bool {
	if len(slug) > PreviewAppSlugMaxLen || !IsPreviewAppSlug(slug) {
		return false
	}
	_, parent, _ := strings.Cut(strings.TrimPrefix(slug, "pr-"), "-")
	return ValidAppSlug(parent)
}

// IsPreviewAppSlug reports the pr-<N>-<parent> shape PR previews are
// allocated under (and that pr-<N>-<parent>.<apps domain> routes to). Only
// preview provisioning may create it: an ordinary app holding
// "pr-7-shop" made the shop app's PR #7 preview fail with "slug taken" and
// left the preview hostname serving someone else's app.
func IsPreviewAppSlug(slug string) bool {
	rest, ok := strings.CutPrefix(slug, "pr-")
	if !ok {
		return false
	}
	digits, parent, ok := strings.Cut(rest, "-")
	if !ok || digits == "" || parent == "" || (digits[0] == '0' && len(digits) > 1) {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}
