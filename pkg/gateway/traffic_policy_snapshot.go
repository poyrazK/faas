// adr: 570
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"

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
// closed instead of silently never matching.
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
			copied.Match = sf.Index(j).FieldByIndex(field.Index).Interface().(EdgeRuleCondition).Match
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

func policyDigest(encoded []byte) string {
	hash := sha256.Sum256(encoded)
	return "traffic-v1:" + hex.EncodeToString(hash[:])
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
func freezeTrafficApp(ctx context.Context, app App) (App, string, error) {
	encoded, err := json.Marshal(app)
	if err != nil {
		return App{}, "", fmt.Errorf("encode app policy: %w", err)
	}
	var frozen App
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return App{}, "", fmt.Errorf("copy app policy: %w", err)
	}
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	hosts := make(map[string]string, len(pinned))
	for host, entry := range pinned {
		hosts[host] = entry.revision
	}
	limits, _ := api.LimitsFor(app.Plan)
	effective, err := json.Marshal(struct {
		App            json.RawMessage
		Hosts          map[string]string
		Limits         api.Limits
		DeclaredRoutes string `json:",omitempty"`
	}{encoded, hosts, limits, declaredRoutePolicyRevision(ctx)})
	if err != nil {
		return App{}, "", fmt.Errorf("encode effective policy: %w", err)
	}
	return frozen, policyDigest(effective), nil
}

func (h *Handler) pinAppTrafficPolicy(w http.ResponseWriter, r *http.Request, app *App) bool {
	if _, productionSnapshot := h.edgeRules.(EdgePolicySnapshotter); !productionSnapshot && h.publicRoutingPolicy == nil {
		return false
	}
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	if h.pinResolvedOwnerPolicies(w, r, *app) {
		return true
	}
	*r = *r.WithContext(WithEdgeRuleApp(WithEdgeRuleOwner(r.Context(), app.AccountID), app.ID))
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
	frozen, revision, err := freezeTrafficApp(r.Context(), *app)
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
	loader, ok := h.edgeRules.(EdgePolicySnapshotter)
	if !ok || appID == "" {
		return false
	}
	if owner, required := h.edgeRules.(EdgeOwnerPolicySnapshotter); required && owner.RequiresOwnerPolicySnapshot() {
		return false
	}
	ctx := r.Context()
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
			h.writeTrafficPolicyUnavailable(w, r)
			return true
		}
	}
	*r = *r.WithContext(ctx)
	return false
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
