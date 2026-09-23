package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// VersionAffinityKeyMaxBytes bounds the customer-controlled value hashed on
	// every request. Keys are opaque UTF-8/byte strings after surrounding ASCII
	// whitespace is removed; callers should use a stable user or tenant id.
	VersionAffinityKeyMaxBytes = 256

	versionAffinityKeyMissing = "missing"
	versionAffinityKeyValid   = "valid"
	versionAffinityKeyInvalid = "invalid"

	versionAffinitySurfacePublic  = "public"
	versionAffinitySurfaceService = "service"
)

// versionAffinityResolver is shared by the public response-cache path and the
// node-local service proxy. Implementations return the deployment cohort only;
// target availability is handled by the ordinary picker/wake path afterwards.
type versionAffinityResolver interface {
	AffinityDeployment(appID, key string) (deploymentID string, ok bool)
}

// versionAffinityPicker combines rollout affinity with best-effort session
// affinity. preferredInstanceID is honored only when it belongs to the keyed
// deployment, preventing an old session cookie from crossing rollout cohorts.
type versionAffinityPicker interface {
	PickForVersionKey(appID, key, preferredInstanceID string) PickResult
}

type versionAffinityDeploymentContextKey struct{}
type managedVersionCookieProtectionContextKey struct{}

func withManagedVersionCookieProtection(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), managedVersionCookieProtectionContextKey{}, true))
}

func managedVersionCookieProtected(ctx context.Context) bool {
	return ctx != nil && ctx.Value(managedVersionCookieProtectionContextKey{}) == true
}

// A __Host- prefix constrains a browser's cookie attributes, not which
// same-host server emitted Set-Cookie. Protect the gateway-owned name only
// for apps that opted into managed version affinity.
func guestSetsManagedVersionCookie(ctx context.Context, name, value string) bool {
	if !managedVersionCookieProtected(ctx) ||
		!strings.EqualFold(strings.TrimSpace(name), "Set-Cookie") {
		return false
	}
	cookieName, _, hasValue := strings.Cut(value, "=")
	return hasValue && strings.TrimSpace(cookieName) == api.ManagedVersionAffinityCookieName
}

func withVersionAffinityDeployment(ctx context.Context, deploymentID string) context.Context {
	if deploymentID == "" {
		return ctx
	}
	return context.WithValue(ctx, versionAffinityDeploymentContextKey{}, deploymentID)
}

func versionAffinityKeyFromRequest(r *http.Request) (string, string) {
	if r == nil {
		return "", versionAffinityKeyMissing
	}
	values, present := r.Header[http.CanonicalHeaderKey(api.VersionKeyHeader)]
	if !present || len(values) == 0 {
		return "", versionAffinityKeyMissing
	}
	// Multiple field lines are ambiguous at intermediaries. Reject them rather
	// than allowing different hops to hash different joined representations.
	if len(values) != 1 {
		return "", versionAffinityKeyInvalid
	}
	key, ok := normalizeVersionAffinityKey(values[0])
	if !ok {
		return "", versionAffinityKeyInvalid
	}
	return key, versionAffinityKeyValid
}

// versionAffinityKeyFromPublicRequest uses an explicitly supplied header first.
// When the app opts into a browser cookie source, it hashes that cookie into
// a non-secret key before forwarding the request to the guest. The derived
// header lets the existing picker, cache partition, and service propagation
// all use the same cohort without exposing the cookie value as a new header.
func versionAffinityKeyFromPublicRequest(r *http.Request, cookieName string) (string, string) {
	if r == nil || cookieName == "" {
		return versionAffinityKeyFromRequest(r)
	}
	if _, present := r.Header[http.CanonicalHeaderKey(api.VersionKeyHeader)]; present {
		return versionAffinityKeyFromRequest(r)
	}
	var cookieValue string
	cookieCount := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name != cookieName {
			continue
		}
		cookieCount++
		if cookieCount > 1 {
			return "", versionAffinityKeyInvalid
		}
		cookieValue = cookie.Value
	}
	if cookieCount == 0 {
		return "", versionAffinityKeyMissing
	}
	value, ok := normalizeVersionAffinityKey(cookieValue)
	if !ok {
		return "", versionAffinityKeyInvalid
	}
	return setCookieVersionAffinityKey(r, cookieName, value), versionAffinityKeyValid
}

func setCookieVersionAffinityKey(r *http.Request, cookieName, value string) string {
	digest := sha256.Sum256([]byte("gregale-version-cookie\x00" + cookieName + "\x00" + value))
	key := hex.EncodeToString(digest[:])
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set(api.VersionKeyHeader, key)
	return key
}

// versionAffinityKeyFromManagedRequest returns a newly minted token only when
// the request has neither an explicit key nor a managed cookie. Invalid or
// duplicate cookies fail open to unkeyed routing instead of silently rotating.
func versionAffinityKeyFromManagedRequest(r *http.Request) (key, outcome, newToken string) {
	if r == nil {
		return "", versionAffinityKeyMissing, ""
	}
	if _, present := r.Header[http.CanonicalHeaderKey(api.VersionKeyHeader)]; present {
		key, outcome = versionAffinityKeyFromRequest(r)
		return key, outcome, ""
	}
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == api.ManagedVersionAffinityCookieName {
			count++
			value = cookie.Value
		}
	}
	if count > 1 {
		return "", versionAffinityKeyInvalid, ""
	}
	if count == 1 {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != value {
			return "", versionAffinityKeyInvalid, ""
		}
		return setCookieVersionAffinityKey(r, api.ManagedVersionAffinityCookieName, value), versionAffinityKeyValid, ""
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", versionAffinityKeyMissing, ""
	}
	newToken = hex.EncodeToString(token)
	return setCookieVersionAffinityKey(r, api.ManagedVersionAffinityCookieName, newToken), versionAffinityKeyValid, newToken
}

// The managed cookie is a routing primitive, not application session state.
// Remove only this reserved cookie before cache eligibility and guest proxying;
// all customer cookies remain, so the ordinary cache safety gate still applies.
func stripManagedVersionAffinityCookie(r *http.Request) {
	if r == nil {
		return
	}
	var kept []string
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" || strings.TrimSpace(strings.SplitN(part, "=", 2)[0]) == api.ManagedVersionAffinityCookieName {
				continue
			}
			kept = append(kept, part)
		}
	}
	r.Header.Del("Cookie")
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

func normalizeVersionAffinityKey(raw string) (string, bool) {
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > VersionAffinityKeyMaxBytes {
		return "", false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] == 0x7f {
			return "", false
		}
	}
	return key, true
}

// setPickerWeights installs both routing orders. The existing percent-desc
// order drives cursor traffic unchanged. Affinity uses deployment-id order so
// changing percentages does not reorder buckets and unnecessarily reshuffle
// users between two revisions during a progressive rollout.
func setPickerWeights(picker *appPicker, weights []deploymentWeight) {
	picker.weights = weights
	picker.cum = buildCumulativeWeights(weights)
	picker.affinityWeights = append(picker.affinityWeights[:0], weights...)
	sort.Slice(picker.affinityWeights, func(i, j int) bool {
		return picker.affinityWeights[i].DeploymentID < picker.affinityWeights[j].DeploymentID
	})
	picker.affinityCum = buildCumulativeWeights(picker.affinityWeights)

	var namespace strings.Builder
	for _, weight := range picker.affinityWeights {
		namespace.WriteString(weight.DeploymentID)
		namespace.WriteByte(0)
	}
	picker.affinityNamespace = namespace.String()
}

func affinityDeployment(appID, key string, picker *appPicker) (string, bool) {
	if picker == nil || len(picker.affinityWeights) == 0 || len(picker.affinityCum) != len(picker.affinityWeights) {
		return "", false
	}
	total := picker.affinityCum[len(picker.affinityCum)-1]
	if total <= 0 {
		return "", false
	}
	digest := sha256.Sum256([]byte(appID + "\x00" + picker.affinityNamespace + "\x00" + key))
	slot := int(binary.BigEndian.Uint64(digest[:8]) % uint64(total))
	for i, upper := range picker.affinityCum {
		if slot < upper {
			return picker.affinityWeights[i].DeploymentID, true
		}
	}
	return picker.affinityWeights[len(picker.affinityWeights)-1].DeploymentID, true
}

func versionAffinityDeploymentForRequest(backend Backend, appID string, r *http.Request) string {
	if r != nil {
		if deploymentID, _ := r.Context().Value(versionAffinityDeploymentContextKey{}).(string); deploymentID != "" {
			return deploymentID
		}
	}
	key, outcome := versionAffinityKeyFromRequest(r)
	if outcome != versionAffinityKeyValid {
		return ""
	}
	resolver, ok := backend.(versionAffinityResolver)
	if !ok {
		return ""
	}
	deploymentID, _ := resolver.AffinityDeployment(appID, key)
	return deploymentID
}
