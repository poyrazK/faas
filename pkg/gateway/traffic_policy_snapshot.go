// adr: 570
package gateway

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/maphash"
	"net/http"
	"reflect"
	"slices"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const TrafficPolicyRevisionHeader = "X-Gregale-Traffic-Policy"

type edgePolicySnapshot struct {
	entry    HostEntry
	revision string
}

type pinnedEdgePoliciesKey struct{}
type effectiveTrafficPolicyKey struct{}
type declaredRoutePolicyRevisionKey struct{}

// ImportedRoutePolicy is private, trusted hostname-resolver metadata. Its
// presence means scoped/explicit routes and this document were read together.
// Only a digest leaves the gateway; client/guest headers cannot supply it.
type ImportedRoutePolicy struct {
	// Compile before app sealing. The hostname fingerprint covers these bytes;
	// the compiled contract is pinned in context, so app JSON needs no raw doc.
	Document []byte `json:"-"`
	Found    bool
}

// DeclaredRoutePolicySnapshotter freezes imported/scoped route inputs before
// app sealing. Later matching and observation must use the returned context.
type DeclaredRoutePolicySnapshotter interface {
	PinDeclaredRoutePolicy(context.Context, App) (context.Context, App, string, error)
}

// EdgePolicySnapshotter is implemented by the production matcher. A verified
// empty policy is valid; a failed load must not become an unchecked rule miss.
type EdgePolicySnapshotter interface {
	PinHostPolicy(context.Context, string) (context.Context, error)
}

type EdgeOwnerPolicySnapshotter interface {
	RequiresOwnerPolicySnapshot() bool
}

// SealPolicy copies all nested compiled actions once, before publication to
// the shared cache. Cache metadata and later source mutations cannot alter it.
func (e *HostEntry) SealPolicy() error {
	if e.snapshot != nil {
		return nil
	}
	serializable := *e
	serializable.PathGlobErrs = nil // error interfaces are metadata, not compiled actions
	encoded, err := json.Marshal(serializable)
	if err != nil {
		return fmt.Errorf("encode compiled host policy: %w", err)
	}
	sealed := &edgePolicySnapshot{revision: policyDigest(encoded)}
	if err := json.Unmarshal(encoded, &sealed.entry); err != nil {
		return fmt.Errorf("copy compiled host policy: %w", err)
	}
	sealed.entry.PathGlobErrs = slices.Clone(e.PathGlobErrs)
	if err := rebindEdgeRuleConditions(&sealed.entry, e); err != nil {
		return err
	}
	e.snapshot = sealed
	return nil
}

// rebindEdgeRuleConditions shares each rule's compiled ADR-962 condition with
// its JSON copy. Programs are immutable; JSON carries only their identity for
// the digest, so a copied condition that is still unbound fails the seal
// closed instead of silently never matching. The whole condition is copied:
// every resolved kind has its own AppID, which hides the condition's AppID
// from encoding/json, and hit counts need it (rc.296 recorded "" and failed
// every edge-rule hit and event flush).
func rebindEdgeRuleConditions(dst, src *HostEntry) error {
	conditionType := reflect.TypeFor[EdgeRuleCondition]()
	dv, sv := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem()
	for i := range dv.NumField() {
		df, sf := dv.Field(i), sv.Field(i)
		if df.Kind() != reflect.Slice || !df.CanSet() || df.Type().Elem().Kind() != reflect.Struct {
			continue
		}
		field, ok := df.Type().Elem().FieldByName("EdgeRuleCondition")
		if !ok || !field.Anonymous || field.Type != conditionType {
			continue
		}
		if df.Len() != sf.Len() {
			return fmt.Errorf("copy compiled host policy: %s rule count changed", dv.Type().Field(i).Name)
		}
		for j := range df.Len() {
			copied := df.Index(j).FieldByIndex(field.Index).Addr().Interface().(*EdgeRuleCondition)
			*copied = sf.Index(j).FieldByIndex(field.Index).Interface().(EdgeRuleCondition)
			if !copied.Match.Bound() {
				return fmt.Errorf("copy compiled host policy: %s rule %d has an unbound condition", dv.Type().Field(i).Name, j)
			}
		}
	}
	return nil
}

// WithPinnedHostPolicy retains the sealed effective actions for this host.
// Only production loaders should call this; entries must have been sealed.
func WithPinnedHostPolicy(ctx context.Context, host string, entry *HostEntry) (context.Context, error) {
	if entry == nil || entry.snapshot == nil {
		return nil, errors.New("host policy is not sealed")
	}
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	copyPinned := make(map[string]*edgePolicySnapshot, len(pinned)+1)
	for key, value := range pinned {
		copyPinned[key] = value
	}
	copyPinned[host] = entry.snapshot
	return context.WithValue(ctx, pinnedEdgePoliciesKey{}, copyPinned), nil
}

// PinnedHostPolicy is the read-only matcher view. Matchers may copy selected
// rule values but must never mutate nested maps/slices in the sealed entry.
func PinnedHostPolicy(ctx context.Context, host string) (*HostEntry, bool) {
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	entry := pinned[host]
	if entry == nil {
		return nil, false
	}
	return &entry.entry, true
}

func PinnedHostPolicyRevision(ctx context.Context, host string) string {
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	if entry := pinned[host]; entry != nil {
		return entry.revision
	}
	return ""
}

// ValidatePinnedHostPolicies runs after owner resolution. A foreign account's
// free-form host match and broken preset must not take another tenant offline.
func ValidatePinnedHostPolicies(ctx context.Context, account string) error {
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	for _, snapshot := range pinned {
		for _, failure := range snapshot.entry.PathGlobErrs {
			owner := snapshot.entry.PolicyRuleOwners[failure.RuleID]
			if owner == "" || owner == account {
				return errors.New("compiled host policy contains unavailable owner rules")
			}
		}
	}
	return nil
}

const policyDigestPrefix = "traffic-v1:"

func policyDigest(encoded []byte) string {
	hash := sha256.Sum256(encoded)
	return policyDigestPrefix + hex.EncodeToString(hash[:])
}

func (h *Handler) pinHostTrafficPolicy(w http.ResponseWriter, r *http.Request, hosts ...string) bool {
	loader, ok := h.edgeRules.(EdgePolicySnapshotter)
	if !ok {
		return false
	}
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	ctx := r.Context()
	for _, host := range hosts {
		if _, pinned := PinnedHostPolicy(ctx, host); pinned {
			continue
		}
		var err error
		ctx, err = loader.PinHostPolicy(ctx, host)
		if err != nil {
			h.writeTrafficPolicyUnavailable(w, r)
			return true
		}
	}
	*r = *r.WithContext(ctx)
	return false
}

// App snapshots include host-specific tenant/auth/routing flags and the plan
// table used by the runtime. Only their digest leaves this private carrier.
// This is the unmemoized freeze; the handler uses its appFreezes cache.
func freezeTrafficApp(ctx context.Context, app App) (App, string, error) {
	return (*appFreezes)(nil).freeze(ctx, app)
}

// appFreezeCacheBytes bounds the memoized app policies one gateway holds. An
// entry costs about twice its JSON encoding (the bytes and the decoded copy);
// one larger than a sixteenth of the budget is sealed but not kept.
const appFreezeCacheBytes = 16 << 20

// appFreezes memoizes the app freeze by the app's exact JSON encoding, so a
// warm request encodes its app once instead of encoding, decoding, re-encoding
// and hashing the whole effective policy. The digest is unchanged: it resumes
// from a hash of the effective policy's App member. A nil cache memoizes nothing.
type appFreezes struct {
	seed  maphash.Seed
	mu    sync.Mutex
	bytes int
	order *list.List // of *appFreeze, most recently used first
	index map[uint64]*list.Element
}

// appFreeze is one sealed app policy. Every request with the same encoding
// shares frozen's nested slices, maps and pointers, so they are read-only,
// like a sealed HostEntry.
type appFreeze struct {
	key     uint64
	encoded []byte
	frozen  App
	limits  []byte      // the JSON of api.LimitsFor(frozen.Plan)
	prefix  hash.Cloner // sha256 over the effective policy up to its App member
}

func newAppFreezes() *appFreezes {
	return &appFreezes{seed: maphash.MakeSeed(), order: list.New(), index: make(map[uint64]*list.Element)}
}

func (c *appFreezes) freeze(ctx context.Context, app App) (App, string, error) {
	encoded, err := json.Marshal(app)
	if err != nil {
		return App{}, "", fmt.Errorf("encode app policy: %w", err)
	}
	sealed, err := c.seal(encoded)
	if err != nil {
		return App{}, "", err
	}
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	hosts := make(map[string]string, len(pinned))
	for host, entry := range pinned {
		hosts[host] = entry.revision
	}
	encodedHosts, err := json.Marshal(hosts)
	if err != nil {
		return App{}, "", fmt.Errorf("encode effective policy: %w", err)
	}
	digest, err := sealed.prefix.Clone()
	if err != nil {
		return App{}, "", fmt.Errorf("encode effective policy: %w", err)
	}
	// The rest of json.Marshal(struct{App json.RawMessage; Hosts
	// map[string]string; Limits api.Limits; DeclaredRoutes string
	// `json:",omitempty"`}), the document traffic-v1 digests.
	_, _ = digest.Write([]byte(`,"Hosts":`))
	_, _ = digest.Write(encodedHosts)
	_, _ = digest.Write([]byte(`,"Limits":`))
	_, _ = digest.Write(sealed.limits)
	if declared := declaredRoutePolicyRevision(ctx); declared != "" {
		encodedDeclared, err := json.Marshal(declared)
		if err != nil {
			return App{}, "", fmt.Errorf("encode effective policy: %w", err)
		}
		_, _ = digest.Write([]byte(`,"DeclaredRoutes":`))
		_, _ = digest.Write(encodedDeclared)
	}
	_, _ = digest.Write([]byte(`}`))
	return sealed.frozen, policyDigestPrefix + hex.EncodeToString(digest.Sum(nil)), nil
}

// seal returns the sealed policy for an app encoding, from the cache when an
// identical encoding is there.
func (c *appFreezes) seal(encoded []byte) (*appFreeze, error) {
	if c == nil {
		return sealAppFreeze(0, encoded)
	}
	key := maphash.Bytes(c.seed, encoded)
	c.mu.Lock()
	var cached *appFreeze
	if element, ok := c.index[key]; ok {
		c.order.MoveToFront(element)
		cached = element.Value.(*appFreeze)
	}
	c.mu.Unlock()
	if cached != nil && bytes.Equal(cached.encoded, encoded) {
		return cached, nil
	}
	sealed, err := sealAppFreeze(key, encoded)
	if err != nil {
		return nil, err
	}
	c.store(sealed)
	return sealed, nil
}

func sealAppFreeze(key uint64, encoded []byte) (*appFreeze, error) {
	sealed := &appFreeze{key: key, encoded: encoded}
	if err := json.Unmarshal(encoded, &sealed.frozen); err != nil {
		return nil, fmt.Errorf("copy app policy: %w", err)
	}
	limits, _ := api.LimitsFor(sealed.frozen.Plan)
	encodedLimits, err := json.Marshal(limits)
	if err != nil {
		return nil, fmt.Errorf("encode effective policy: %w", err)
	}
	sealed.limits = encodedLimits
	prefix, ok := sha256.New().(hash.Cloner)
	if !ok {
		return nil, errors.New("encode effective policy: sha256 state cannot be cloned")
	}
	// encoded is already compact, HTML-escaped JSON, which is exactly what
	// json.Marshal writes for a json.RawMessage member.
	_, _ = prefix.Write([]byte(`{"App":`))
	_, _ = prefix.Write(encoded)
	sealed.prefix = prefix
	return sealed, nil
}

func (c *appFreezes) store(sealed *appFreeze) {
	cost := sealed.cost()
	if cost > appFreezeCacheBytes/16 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.index[sealed.key]; ok {
		c.removeLocked(element)
	}
	c.index[sealed.key] = c.order.PushFront(sealed)
	c.bytes += cost
	for c.bytes > appFreezeCacheBytes {
		c.removeLocked(c.order.Back())
	}
}

func (c *appFreezes) removeLocked(element *list.Element) {
	sealed := c.order.Remove(element).(*appFreeze)
	delete(c.index, sealed.key)
	c.bytes -= sealed.cost()
}

func (f *appFreeze) cost() int {
	return 2*len(f.encoded) + len(f.limits) + 512
}

func (h *Handler) pinAppTrafficPolicy(w http.ResponseWriter, r *http.Request, app *App) bool {
	if _, productionSnapshot := h.edgeRules.(EdgePolicySnapshotter); !productionSnapshot && h.publicRoutingPolicy == nil {
		return false
	}
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	if h.pinResolvedOwnerPolicies(w, r, *app) {
		return true
	}
	*r = *r.WithContext(WithEdgeRuleScope(r.Context(), *app))
	if h.pinAppEdgePolicies(w, r, app.ID) {
		return true
	}
	// Validate after the app pins, so the app's own app-wide rules fail closed too.
	if err := ValidatePinnedHostPolicies(r.Context(), app.AccountID); err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	if h.applyTotalDeadline(w, r, *app) {
		return true
	}
	if loader, ok := h.declaredRoutes.(DeclaredRoutePolicySnapshotter); ok {
		ctx, resolved, revision, err := loader.PinDeclaredRoutePolicy(r.Context(), *app)
		if err != nil || ctx == nil || revision == "" || resolved.ID != app.ID || resolved.AccountID != app.AccountID {
			if requestBudgetExpired(r.Context()) {
				writeRequestBudgetExceededForRequest(w, r)
				return true
			}
			h.writeTrafficPolicyUnavailable(w, r)
			return true
		}
		*app = resolved
		*r = *r.WithContext(context.WithValue(ctx, declaredRoutePolicyRevisionKey{}, revision))
	}
	frozen, revision, err := h.appFreezes.freeze(r.Context(), *app)
	if err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	*app = frozen
	*r = *r.WithContext(context.WithValue(r.Context(), effectiveTrafficPolicyKey{}, revision))
	w.Header().Set(TrafficPolicyRevisionHeader, revision)
	return false
}

// pinAppEdgePolicies re-pins each host for the resolved app on the cached
// path: the app's app-wide rules (ADR-1191) join the host's set, so the pin is
// per (host, app). The loader refuses a generation change since the host pin.
// The authoritative owner policy already carries them (pinResolvedOwnerPolicies).
func (h *Handler) pinAppEdgePolicies(w http.ResponseWriter, r *http.Request, appID string) bool {
	ctx, err := h.pinAppEdgeHosts(r.Context(), appID)
	if err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	*r = *r.WithContext(ctx)
	return false
}

// pinAppEdgeHosts is pinAppEdgePolicies on a context: ctx must carry appID's
// edge-rule scope, and an error means the request fails closed.
func (h *Handler) pinAppEdgeHosts(ctx context.Context, appID string) (context.Context, error) {
	loader, ok := h.edgeRules.(EdgePolicySnapshotter)
	if !ok || appID == "" {
		return ctx, nil
	}
	if owner, required := h.edgeRules.(EdgeOwnerPolicySnapshotter); required && owner.RequiresOwnerPolicySnapshot() {
		return ctx, nil
	}
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	hosts := make([]string, 0, len(pinned))
	for key := range pinned {
		if EdgeRuleCacheKeyHost(key) == key {
			hosts = append(hosts, key)
		}
	}
	slices.Sort(hosts)
	for _, host := range hosts {
		if _, done := PinnedHostPolicy(ctx, EdgeRuleCacheKey(host, appID)); done {
			continue
		}
		var err error
		if ctx, err = loader.PinHostPolicy(ctx, host); err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

func (h *Handler) writeTrafficPolicyUnavailable(w http.ResponseWriter, r *http.Request) {
	recordTrafficRefusal(r.Context(), "policy_unavailable")
	w.Header().Set("Retry-After", "1")
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeTrafficPolicyUnavailable,
		"Traffic policy unavailable", "Gregale could not verify this route's traffic policy. Retry shortly."))
}

func TrafficPolicyRevision(ctx context.Context) string {
	revision, _ := ctx.Value(effectiveTrafficPolicyKey{}).(string)
	return revision
}

func declaredRoutePolicyRevision(ctx context.Context) string {
	revision, _ := ctx.Value(declaredRoutePolicyRevisionKey{}).(string)
	return revision
}
