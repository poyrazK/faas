package state

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A full environment clone copies the source's active API consumers, their
// active keys and their plan assignments into the stage (ADR-20261104120000
// amendment 2026-11-28). Keys stay active under the same prefix and secret,
// so a client's key works on the stage host; usage, billing and credits stay
// with the source. Consumers of a platform tenant stay in production: the
// tenant's lifecycle manages them. Revoking a production consumer or key
// revokes its stage copies (the revocation cascade triggers, and
// cascadeProductionConsumerRevocationLocked here).

// projectCloneConsumerKey is one active key of a captured consumer. The
// secret is not captured: the copy reads it from the source key, which must
// still be active then.
type projectCloneConsumerKey struct {
	SourceKeyID string     `json:"source_key_id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type projectCloneConsumerPlanAssignment struct {
	PlanID        string    `json:"plan_id"`
	EffectiveFrom time.Time `json:"effective_from"`
}

type projectCloneConsumer struct {
	// SourceConsumerID is the captured consumer; publication accepts a
	// revoked copy only once the source is revoked too.
	SourceConsumerID string                               `json:"source_consumer_id,omitempty"`
	ExternalRef      string                               `json:"external_ref"`
	Name             string                               `json:"name"`
	Keys             []projectCloneConsumerKey            `json:"keys"`
	Plans            []projectCloneConsumerPlanAssignment `json:"plans"`
}

// projectCloneConsumers is one workload's active consumers in the source
// environment. A snapshot without it predates consumer capture and copies
// none.
type projectCloneConsumers struct {
	AppID     string                 `json:"app_id"`
	Consumers []projectCloneConsumer `json:"consumers"`
}

func normalizeCloneConsumers(in projectCloneConsumers) (projectCloneConsumers, error) {
	out := projectCloneConsumers{AppID: in.AppID, Consumers: make([]projectCloneConsumer, 0, len(in.Consumers))}
	refs, prefixes, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range in.Consumers {
		if c.ExternalRef == "" || refs[c.ExternalRef] {
			return out, ErrConflict
		}
		refs[c.ExternalRef] = true
		c.Keys = slices.Clone(c.Keys)
		for i, k := range c.Keys {
			if k.Name == "" || k.Prefix == "" || prefixes[k.Prefix] || names[k.Name] {
				return out, ErrConflict
			}
			prefixes[k.Prefix], names[k.Name] = true, true
			k.Scopes = slices.Clone(k.Scopes)
			if k.Scopes == nil {
				k.Scopes = []string{}
			}
			if k.ExpiresAt != nil {
				at := k.ExpiresAt.UTC()
				k.ExpiresAt = &at
			}
			c.Keys[i] = k
		}
		sort.Slice(c.Keys, func(i, j int) bool { return c.Keys[i].Prefix < c.Keys[j].Prefix })
		c.Plans = slices.Clone(c.Plans)
		for i := range c.Plans {
			c.Plans[i].EffectiveFrom = c.Plans[i].EffectiveFrom.UTC()
		}
		sort.Slice(c.Plans, func(i, j int) bool {
			if !c.Plans[i].EffectiveFrom.Equal(c.Plans[j].EffectiveFrom) {
				return c.Plans[i].EffectiveFrom.Before(c.Plans[j].EffectiveFrom)
			}
			return c.Plans[i].PlanID < c.Plans[j].PlanID
		})
		if c.Keys == nil {
			c.Keys = []projectCloneConsumerKey{}
		}
		if c.Plans == nil {
			c.Plans = []projectCloneConsumerPlanAssignment{}
		}
		out.Consumers = append(out.Consumers, c)
	}
	sort.Slice(out.Consumers, func(i, j int) bool { return out.Consumers[i].ExternalRef < out.Consumers[j].ExternalRef })
	return out, nil
}

// capturedCloneConsumers returns each captured workload's consumers by app.
func capturedCloneConsumers(records []projectCloneWorkloadRecord) map[string]projectCloneConsumers {
	out := make(map[string]projectCloneConsumers, len(records))
	for _, record := range records {
		if record.snapshot.Consumers != nil {
			out[record.AppID] = *record.snapshot.Consumers
		}
	}
	return out
}

// cloneConsumerKeyCounts counts the keys a copy adds per app.
func cloneConsumerKeyCounts(consumers map[string]projectCloneConsumers) map[string]int {
	out := map[string]int{}
	for appID, captured := range consumers {
		for _, c := range captured.Consumers {
			out[appID] += len(c.Keys)
		}
	}
	return out
}

// checkCloneConsumerKeyQuota refuses a copy that takes an app or its billing
// subject past the plan's unrevoked key allowance. count returns an app's
// unrevoked keys now.
func checkCloneConsumerKeyQuota(added map[string]int, count func(appID string) (int, error), subjectKeys int, limits api.Limits) error {
	total := 0
	for _, appID := range slices.Sorted(maps.Keys(added)) {
		if added[appID] == 0 {
			continue
		}
		total += added[appID]
		n, err := count(appID)
		if err != nil {
			return err
		}
		if observed := n + added[appID]; observed > limits.ConsumerKeysPerApp {
			return &ConsumerKeyQuotaError{Scope: api.PlanQuotaScopeApp, Limit: limits.ConsumerKeysPerApp, Observed: observed}
		}
	}
	if total > 0 && subjectKeys+total > limits.ConsumerKeysPerAccount {
		return &ConsumerKeyQuotaError{Scope: api.PlanQuotaScopeAccount, Limit: limits.ConsumerKeysPerAccount, Observed: subjectKeys + total}
	}
	return nil
}

// cloneConsumerPublication is what a published stage holds for one app:
// each consumer's keys (with revocation) and plan assignments.
type cloneConsumerPublication struct {
	Consumer projectCloneConsumer
	Active   bool
	// KeyRevoked is keyed by prefix.
	KeyRevoked map[string]bool
}

// validateCloneConsumerPublication proves the target holds exactly the
// captured consumers and their plan assignments, and no key the capture did
// not hold. A captured key may be missing, or a copy revoked, only when the
// source key or its consumer has been revoked since; sourceActive answers
// whether a source consumer, or one of its keys, is still active.
func validateCloneConsumerPublication(record projectCloneWorkloadRecord, actual []cloneConsumerPublication, sourceActive func(consumerID, keyID string) (bool, error)) error {
	want := projectCloneConsumers{AppID: record.AppID}
	if record.snapshot.Consumers != nil {
		want.Consumers = record.snapshot.Consumers.Consumers
	}
	want, err := normalizeCloneConsumers(want)
	if err != nil {
		return err
	}
	differ := fmt.Errorf("clone workload %q API consumers differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	got := map[string]cloneConsumerPublication{}
	for _, c := range actual {
		got[c.Consumer.ExternalRef] = c
	}
	if len(got) != len(want.Consumers) || len(got) != len(actual) {
		return differ
	}
	for _, w := range want.Consumers {
		g, ok := got[w.ExternalRef]
		if !ok || g.Consumer.Name != w.Name || !slices.EqualFunc(w.Plans, normalizeClonePlans(g.Consumer.Plans), sameClonePlan) {
			return differ
		}
		if !g.Active {
			active, err := sourceActive(w.SourceConsumerID, "")
			if err != nil {
				return err
			}
			if active {
				return differ
			}
		}
		keys := map[string]projectCloneConsumerKey{}
		for _, k := range g.Consumer.Keys {
			keys[k.Prefix] = k
		}
		for _, k := range w.Keys {
			copied, ok := keys[k.Prefix]
			delete(keys, k.Prefix)
			if ok && copied.Name == k.Name && slices.Equal(normalizeCloneScopes(copied.Scopes), k.Scopes) && sameCloneTime(copied.ExpiresAt, k.ExpiresAt) &&
				g.Active && !g.KeyRevoked[k.Prefix] {
				continue
			}
			// A source revoked since capture is copied revoked or not at all.
			active, err := sourceActive(w.SourceConsumerID, k.SourceKeyID)
			if err != nil {
				return err
			}
			if active {
				return differ
			}
		}
		if len(keys) > 0 {
			return differ
		}
	}
	return nil
}

func normalizeClonePlans(in []projectCloneConsumerPlanAssignment) []projectCloneConsumerPlanAssignment {
	c, _ := normalizeCloneConsumers(projectCloneConsumers{Consumers: []projectCloneConsumer{{ExternalRef: "x", Plans: in}}})
	return c.Consumers[0].Plans
}

func normalizeCloneScopes(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func sameClonePlan(a, b projectCloneConsumerPlanAssignment) bool {
	return a.PlanID == b.PlanID && a.EffectiveFrom.Equal(b.EffectiveFrom)
}

func sameCloneTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (m *MemStore) captureCloneConsumersLocked(appID, environmentID string) (projectCloneConsumers, error) {
	captured := projectCloneConsumers{AppID: appID}
	for _, c := range m.apiConsumers {
		if !sameMemUUID(c.AppID, appID) || c.EnvironmentID != environmentID || !c.Active() || c.PlatformTenantID != "" {
			continue
		}
		consumer := projectCloneConsumer{SourceConsumerID: c.ID, ExternalRef: c.ExternalRef, Name: c.Name}
		now := time.Now()
		for _, k := range m.consumerKeys {
			if k.ConsumerID != c.ID || k.RevokedAt != nil || k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
				continue
			}
			consumer.Keys = append(consumer.Keys, projectCloneConsumerKey{SourceKeyID: k.ID, Name: k.Name, Prefix: k.Prefix,
				Scopes: slices.Clone(k.Scopes), ExpiresAt: k.ExpiresAt})
		}
		for _, a := range m.apiConsumerPlanAssignments {
			if a.ConsumerID == c.ID {
				consumer.Plans = append(consumer.Plans, projectCloneConsumerPlanAssignment{PlanID: a.PlanID, EffectiveFrom: a.EffectiveFrom})
			}
		}
		captured.Consumers = append(captured.Consumers, consumer)
	}
	return normalizeCloneConsumers(captured)
}

// cloneEnvironmentIDLocked is the consumer and alert rule environment of a
// project environment slug: "" for production, else the stage's ID.
func (m *MemStore) cloneEnvironmentIDLocked(projectID, slug string) string {
	if IsProductionScope(slug) {
		return ""
	}
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == slug {
			return env.ID
		}
	}
	return ""
}

// requireCloneConsumersCapturedLocked refuses a source whose environment
// holds active consumers on project apps outside the captured workloads.
func (m *MemStore) requireCloneConsumersCapturedLocked(projectID, environmentID string, captured map[string]bool) error {
	for _, c := range m.apiConsumers {
		app, ok := m.apps[c.AppID]
		if !ok || app.ProjectID != projectID || app.Status == AppDeleted || c.EnvironmentID != environmentID ||
			!c.Active() || c.PlatformTenantID != "" || captured[app.ID] {
			continue
		}
		return ErrProjectEnvironmentCloneConsumerCaptureUnavailable
	}
	return nil
}

// ErrProjectEnvironmentCloneConsumerCaptureUnavailable refuses a source
// whose API consumers the clone would not copy: consumers of an app that has
// no workload in the source environment.
var ErrProjectEnvironmentCloneConsumerCaptureUnavailable = fmt.Errorf("clone API consumer capture is unavailable: %w", ErrConflict)

func (m *MemStore) checkCloneConsumerKeyQuotaLocked(consumers map[string]projectCloneConsumers, limits api.Limits) error {
	added := cloneConsumerKeyCounts(consumers)
	var first string
	for _, appID := range slices.Sorted(maps.Keys(added)) {
		if added[appID] > 0 {
			first = appID
			break
		}
	}
	if first == "" {
		return nil
	}
	app := m.apps[first]
	subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
	perApp, subjectKeys := map[string]int{}, 0
	for _, k := range m.consumerKeys {
		if k.RevokedAt != nil || !m.countsTowardQuotaSubjectLocked(k.AccountID, k.AppID, subject) {
			continue
		}
		subjectKeys++
		perApp[k.AppID]++
	}
	return checkCloneConsumerKeyQuota(added, func(appID string) (int, error) { return perApp[appID], nil }, subjectKeys, limits)
}

func (m *MemStore) copyCapturedConsumersLocked(clone ProjectEnvironmentClone, target ProjectEnvironment, now time.Time, result *ProjectEnvironmentCloneResult) {
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedConsumers)) {
		app, ok := m.apps[appID]
		if !ok {
			continue
		}
		for _, c := range clone.capturedConsumers[appID].Consumers {
			consumer := APIConsumer{ID: uuid.NewString(), AccountID: app.AccountID, AppID: appID, ExternalRef: c.ExternalRef, Name: c.Name,
				Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now, EnvironmentID: target.ID}
			m.apiConsumers[consumer.ID] = consumer
			result.ConsumersCopied++
			for _, k := range c.Keys {
				source, ok := m.consumerKeys[k.SourceKeyID]
				if !ok || source.RevokedAt != nil {
					continue
				}
				key := ConsumerKey{ID: uuid.NewString(), AccountID: app.AccountID, AppID: appID, ConsumerID: consumer.ID, Name: k.Name,
					Prefix: k.Prefix, Hash: slices.Clone(source.Hash), Scopes: slices.Clone(k.Scopes), CreatedAt: now, ExpiresAt: k.ExpiresAt,
					EnvironmentID: target.ID}
				m.consumerKeys[key.ID] = key
				result.ConsumerKeysCopied++
			}
			for _, p := range c.Plans {
				id := uuid.NewString()
				m.apiConsumerPlanAssignments[id] = APIConsumerPlanAssignment{ID: id, AccountID: app.AccountID, AppID: appID,
					ConsumerID: consumer.ID, PlanID: p.PlanID, EffectiveFrom: p.EffectiveFrom, CreatedAt: now}
			}
		}
	}
}

func (m *MemStore) verifyCloneConsumerPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	environmentID := m.cloneEnvironmentIDLocked(op.ProjectID, op.TargetEnvironment)
	for _, record := range records {
		var actual []cloneConsumerPublication
		for _, c := range m.apiConsumers {
			if !sameMemUUID(c.AppID, record.AppID) || environmentID == "" || c.EnvironmentID != environmentID {
				continue
			}
			p := cloneConsumerPublication{Consumer: projectCloneConsumer{ExternalRef: c.ExternalRef, Name: c.Name}, Active: c.Active(), KeyRevoked: map[string]bool{}}
			for _, k := range m.consumerKeys {
				if k.ConsumerID == c.ID {
					p.Consumer.Keys = append(p.Consumer.Keys, projectCloneConsumerKey{Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, ExpiresAt: k.ExpiresAt})
					p.KeyRevoked[k.Prefix] = k.RevokedAt != nil
				}
			}
			for _, a := range m.apiConsumerPlanAssignments {
				if a.ConsumerID == c.ID {
					p.Consumer.Plans = append(p.Consumer.Plans, projectCloneConsumerPlanAssignment{PlanID: a.PlanID, EffectiveFrom: a.EffectiveFrom})
				}
			}
			actual = append(actual, p)
		}
		err := validateCloneConsumerPublication(record, actual, func(consumerID, keyID string) (bool, error) {
			c, ok := m.apiConsumers[consumerID]
			if !ok || !c.Active() {
				return false, nil
			}
			if keyID == "" {
				return true, nil
			}
			k, ok := m.consumerKeys[keyID]
			return ok && k.RevokedAt == nil, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// cascadeProductionConsumerRevocationLocked revokes the stage copies of a
// production consumer, as the api_consumer_revocation_cascade trigger does.
func (m *MemStore) cascadeProductionConsumerRevocationLocked(c APIConsumer) {
	if c.EnvironmentID != "" || c.RevokedAt == nil {
		return
	}
	for id, copied := range m.apiConsumers {
		if copied.AppID == c.AppID && copied.ExternalRef == c.ExternalRef && copied.EnvironmentID != "" && copied.RevokedAt == nil {
			at := *c.RevokedAt
			copied.Status, copied.RevokedAt, copied.UpdatedAt = APIConsumerStatusRevoked, &at, at
			m.apiConsumers[id] = copied
		}
	}
}

// cascadeProductionConsumerKeyRevocationLocked revokes the stage copies of a
// production key, as the consumer_key_revocation_cascade trigger does.
func (m *MemStore) cascadeProductionConsumerKeyRevocationLocked(k ConsumerKey) {
	if k.EnvironmentID != "" || k.RevokedAt == nil {
		return
	}
	for id, copied := range m.consumerKeys {
		if copied.AppID == k.AppID && copied.Prefix == k.Prefix && copied.EnvironmentID != "" && copied.RevokedAt == nil {
			at := *k.RevokedAt
			copied.RevokedAt = &at
			m.consumerKeys[id] = copied
		}
	}
}
