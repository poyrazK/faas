package socket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

const maxQuotaRecordBytes = 2 << 20
const quotaWindow = time.Minute

type TenantLimits struct {
	Connections          int   `json:"connections"`
	GrantsPerMinute      int   `json:"grants_per_minute"`
	InvocationsPerMinute int   `json:"invocations_per_minute"`
	RetainedBytes        int64 `json:"retained_bytes"`
	EntityBytes          int64 `json:"entity_bytes"`
}

func (l TenantLimits) valid() bool {
	return l.Connections > 0 && l.Connections <= 32 && l.GrantsPerMinute > 0 && l.GrantsPerMinute <= 256 && l.InvocationsPerMinute > 0 && l.InvocationsPerMinute <= 10000 && l.EntityBytes > 0 && l.EntityBytes <= 64<<20 && l.RetainedBytes >= l.EntityBytes && l.RetainedBytes <= 1<<30
}

// Quotas coordinates admission across nodes through a single CAS object per
// account/app/immutable environment/tenant. Entity reservations are conservative
// high-water caps, not provider billing or a scan of legacy data.
type Quotas struct {
	Store         durableentity.ObjectStore
	Limits        TenantLimits
	Now           func() time.Time
	RegistryCache *RegistryCache
}
type quotaScope struct{ Account, App, Environment, Tenant string }
type connectionLease struct {
	ID      string    `json:"id"`
	Expires time.Time `json:"expires"`
}
type entityReservation struct {
	Key   string `json:"key"`
	Bytes int64  `json:"bytes"`
}
type quotaRecord struct {
	Schema          int                 `json:"schema"`
	Scope           quotaScope          `json:"scope"`
	Limits          TenantLimits        `json:"limits"`
	Window          time.Time           `json:"window"`
	GrantsIssued    int                 `json:"grants_issued"`
	Invocations     int                 `json:"invocations"`
	Grants          []Grant             `json:"grants"`
	Connections     []connectionLease   `json:"connections"`
	Entities        []entityReservation `json:"entities"`
	Migration       *QuotaMigration     `json:"migration,omitempty"`
	PolicyRevision  uint64              `json:"policy_revision,omitempty"`
	PolicyChanges   []QuotaPolicyChange `json:"policy_changes,omitempty"`
	Retirements     []QuotaRetirement   `json:"retirements,omitempty"`
	Registry        *quotaRegistry      `json:"registry,omitempty"`
	RegistryCleanup *RegistryCleanup    `json:"registry_cleanup,omitempty"`
}
type QuotaError struct {
	Budget          string
	Limit, Observed int64
	RetryAfter      int
}

func (e *QuotaError) Error() string { return fmt.Sprintf("durable tenant %s quota exceeded", e.Budget) }
func (q *Quotas) now() time.Time {
	if q.Now != nil {
		return q.Now().UTC()
	}
	return time.Now().UTC()
}
func scopeFor(id durableentity.ID) (quotaScope, error) {
	if !grantIdentity(id.AccountID) || !grantIdentity(id.AppID) || !grantIdentity(id.EnvironmentID) || (id.TenantID != "" && !grantIdentity(id.TenantID)) || !grantIdentity(id.Namespace) || !grantIdentity(id.Key) {
		return quotaScope{}, durableentity.ErrInvalid
	}
	return quotaScope{id.AccountID, id.AppID, id.EnvironmentID, id.TenantID}, nil
}
func scopeKey(scope quotaScope) string {
	body, _ := json.Marshal(scope)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func entityKey(id durableentity.ID) string {
	body, _ := json.Marshal(id)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func validScopeKey(key string) bool {
	b, e := hex.DecodeString(key)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == key
}
func stricterLimits(a, b TenantLimits) TenantLimits {
	return TenantLimits{min(a.Connections, b.Connections), min(a.GrantsPerMinute, b.GrantsPerMinute), min(a.InvocationsPerMinute, b.InvocationsPerMinute), min(a.RetainedBytes, b.RetainedBytes), min(a.EntityBytes, b.EntityBytes)}
}
func (q *Quotas) read(ctx context.Context, key string) (quotaRecord, string, error) {
	if !q.Limits.valid() || !validScopeKey(key) {
		return quotaRecord{}, "", durableentity.ErrInvalid
	}
	body, version, err := q.Store.Get(ctx, "tenant-quotas/v1/"+key, maxQuotaRecordBytes)
	if err != nil {
		return quotaRecord{}, "", err
	}
	var r quotaRecord
	if version == "" || len(body) > maxQuotaRecordBytes || json.Unmarshal(body, &r) != nil || (r.Schema < 1 || r.Schema > 6) || scopeKey(r.Scope) != key || !r.Limits.valid() || r.Window.IsZero() || r.GrantsIssued < 0 || r.GrantsIssued > 256 || r.Invocations < 0 || r.Invocations > 10000 || len(r.Grants) > 256 || len(r.Connections) > 32 || len(r.Entities) > entityCapacity(r) {
		return quotaRecord{}, "", durableentity.ErrCorrupt
	}
	if err := q.loadRegistry(ctx, key, &r); err != nil {
		return quotaRecord{}, "", err
	}
	if r.Migration != nil && !r.Migration.valid() {
		return quotaRecord{}, "", durableentity.ErrCorrupt
	}
	if !validQuotaPolicy(r) || !validQuotaRetirements(r) {
		return quotaRecord{}, "", durableentity.ErrCorrupt
	}
	seen := map[string]bool{}
	for _, g := range r.Grants {
		scope, err := scopeFor(g.Entity)
		if err != nil || scope != r.Scope || !validQuotaGrantID(g.ID, key) || g.ExpiresAt.IsZero() || seen[g.ID] {
			return quotaRecord{}, "", durableentity.ErrCorrupt
		}
		seen[g.ID] = true
	}
	seen = map[string]bool{}
	for _, c := range r.Connections {
		if !sessionUUID(c.ID) || c.Expires.IsZero() || seen[c.ID] {
			return quotaRecord{}, "", durableentity.ErrCorrupt
		}
		seen[c.ID] = true
	}
	seen = map[string]bool{}
	for _, e := range r.Entities {
		if !validScopeKey(e.Key) || e.Bytes <= 0 || e.Bytes > 64<<20 || seen[e.Key] {
			return quotaRecord{}, "", durableentity.ErrCorrupt
		}
		seen[e.Key] = true
	}
	return r, version, nil
}
func validQuotaGrantID(id, key string) bool {
	return len(id) == 101 && id[:64] == key && id[64] == '.' && sessionUUID(id[65:])
}
func prune(r *quotaRecord, now time.Time) {
	grants := r.Grants[:0]
	for _, g := range r.Grants {
		if now.Before(g.ExpiresAt) {
			grants = append(grants, g)
		}
	}
	r.Grants = grants
	connections := r.Connections[:0]
	for _, c := range r.Connections {
		if now.Before(c.Expires) {
			connections = append(connections, c)
		}
	}
	r.Connections = connections
	if !now.Before(r.Window.Add(quotaWindow)) {
		r.Window = now
		r.GrantsIssued = 0
		r.Invocations = 0
	}
}
func grantBytes(r quotaRecord) int64 {
	if len(r.Grants) == 0 {
		return 0
	}
	body, _ := json.Marshal(r.Grants)
	return int64(len(body))
}
func retainedBytes(r quotaRecord) int64 {
	n := grantBytes(r) + recoveryBytes(r)
	for _, e := range r.Entities {
		n += e.Bytes
	}
	return n
}
func quotaErr(budget string, limit, observed int64, retry int) error {
	return &QuotaError{budget, limit, observed, retry}
}
func retryWindow(r quotaRecord, now time.Time) int {
	return max(1, int(r.Window.Add(quotaWindow).Sub(now).Seconds())+1)
}
func (q *Quotas) mutate(ctx context.Context, key string, scope *quotaScope, change func(*quotaRecord, time.Time) error) error {
	return q.mutateMigration(ctx, key, scope, false, change)
}
func (q *Quotas) mutateMigration(ctx context.Context, key string, scope *quotaScope, migration bool, change func(*quotaRecord, time.Time) error) error {
	for attempt := 0; attempt < 8; attempt++ {
		r, version, err := q.read(ctx, key)
		now := q.now()
		if errors.Is(err, durableentity.ErrNotFound) && scope != nil {
			r = quotaRecord{Schema: 1, Scope: *scope, Limits: q.Limits, Window: now}
		} else if err != nil {
			return err
		}
		if scope != nil && r.Scope != *scope {
			return durableentity.ErrCorrupt
		}
		if r.Schema >= 2 {
			if r.Limits != q.Limits {
				return ErrPolicyMismatch
			}
		} else {
			r.Limits = stricterLimits(r.Limits, q.Limits)
		}
		if registryCleanupHoldsWrites(r) || migration && registryCleanupActive(r) {
			return ErrRegistryCleanupPending
		}
		prune(&r, now)
		if !migration && r.Migration != nil && r.Migration.Phase != "complete" {
			return ErrMigrationPending
		}
		if err = change(&r, now); err != nil {
			return err
		}
		body, err := q.marshalQuota(ctx, key, r)
		if err != nil {
			return err
		}
		if len(body) > maxQuotaRecordBytes {
			return durableentity.ErrLimit
		}
		writtenVersion, writeErr := q.Store.Put(ctx, "tenant-quotas/v1/"+key, body, version)
		err = writeErr
		if err == nil && writtenVersion == "" {
			err = durableentity.ErrUncertain
		}
		if errors.Is(err, durableentity.ErrConflict) {
			continue
		}
		// An uncertain write may have charged admission. Never automatically retry
		// it, dispatch work, return a credential or roll back another node's state.
		if err != nil {
			return errors.Join(durableentity.ErrUncertain, err)
		}
		return nil
	}
	return durableentity.ErrBusy
}
func (q *Quotas) mint(ctx context.Context, g Grant) (Grant, error) {
	scope, err := scopeFor(g.Entity)
	if err != nil {
		return Grant{}, err
	}
	key := scopeKey(scope)
	if !validQuotaGrantID(g.ID, key) {
		return Grant{}, durableentity.ErrInvalid
	}
	err = q.mutate(ctx, key, &scope, func(r *quotaRecord, now time.Time) error {
		if retired(*r, g.Entity) {
			return durableentity.ErrRetired
		}
		if !now.Before(g.ExpiresAt) {
			return ErrGrantDenied
		}
		if r.GrantsIssued >= r.Limits.GrantsPerMinute {
			return quotaErr("grant_issuance", int64(r.Limits.GrantsPerMinute), int64(r.GrantsIssued+1), retryWindow(*r, now))
		}
		if len(r.Grants) >= 256 {
			return quotaErr("grant_records", 256, int64(len(r.Grants)+1), 300)
		}
		r.Grants = append(r.Grants, g)
		if n := retainedBytes(*r); n > r.Limits.RetainedBytes {
			return quotaErr("retained_bytes", r.Limits.RetainedBytes, n, 0)
		}
		if n := grantBytes(*r); n > 512<<10 {
			return quotaErr("grant_record_bytes", 512<<10, n, 300)
		}
		r.GrantsIssued++
		return nil
	})
	return g, err
}
func (q *Quotas) readGrant(ctx context.Context, id string) (Grant, string, error) {
	if len(id) != 101 || !validScopeKey(id[:64]) || !validQuotaGrantID(id, id[:64]) {
		return Grant{}, "", ErrGrantDenied
	}
	r, version, err := q.read(ctx, id[:64])
	if errors.Is(err, durableentity.ErrNotFound) {
		return Grant{}, "", ErrGrantDenied
	}
	if err != nil {
		return Grant{}, "", err
	}
	for _, g := range r.Grants {
		if g.ID == id {
			return g, version, nil
		}
	}
	return Grant{}, "", ErrGrantDenied
}
func (q *Quotas) revoke(ctx context.Context, id, account, app string) error {
	g, _, err := q.readGrant(ctx, id)
	if err != nil {
		return err
	}
	if g.Entity.AccountID != account || g.Entity.AppID != app {
		return ErrGrantDenied
	}
	return q.mutate(ctx, id[:64], nil, func(r *quotaRecord, _ time.Time) error {
		for i := range r.Grants {
			if r.Grants[i].ID == id {
				r.Grants[i].Revoked = true
				return nil
			}
		}
		return ErrGrantDenied
	})
}
func (q *Quotas) AcquireConnection(ctx context.Context, id durableentity.ID) (string, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return "", err
	}
	key := scopeKey(scope)
	lease := uuid.NewString()
	err = q.mutate(ctx, key, &scope, func(r *quotaRecord, now time.Time) error {
		if retired(*r, id) {
			return durableentity.ErrRetired
		}
		if len(r.Connections) >= r.Limits.Connections {
			return quotaErr("connections", int64(r.Limits.Connections), int64(len(r.Connections)+1), 30)
		}
		r.Connections = append(r.Connections, connectionLease{lease, now.Add(Lifetime + api.DurableEntityInvokeTimeout + WriteTimeout + IdleTimeout)})
		return nil
	})
	if err != nil {
		return "", err
	}
	return lease, nil
}
func (q *Quotas) ReleaseConnection(ctx context.Context, id durableentity.ID, lease string) error {
	scope, err := scopeFor(id)
	if err != nil {
		return err
	}
	return q.mutate(ctx, scopeKey(scope), nil, func(r *quotaRecord, _ time.Time) error {
		out := r.Connections[:0]
		for _, c := range r.Connections {
			if c.ID != lease {
				out = append(out, c)
			}
		}
		r.Connections = out
		return nil
	})
}
func (q *Quotas) CheckConnection(ctx context.Context, id durableentity.ID, lease string) error {
	scope, err := scopeFor(id)
	if err != nil {
		return err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if errors.Is(err, durableentity.ErrNotFound) {
		return ErrGrantDenied
	}
	if err != nil {
		return err
	}
	if r.Migration != nil && r.Migration.Phase != "complete" {
		return ErrGrantDenied
	}
	if retired(r, id) {
		return ErrGrantDenied
	}
	if r.Schema >= 2 && r.Limits != q.Limits {
		return ErrGrantDenied
	}
	for _, c := range r.Connections {
		if c.ID == lease && q.now().Before(c.Expires) {
			return nil
		}
	}
	return ErrGrantDenied
}

// AdmitInvocation reserves each entity's configured retained-byte cap once.
// Reservations survive receipt GC and process restarts. Replays count as
// attempts, so retry traffic cannot bypass the request-rate quota.
func (q *Quotas) AdmitInvocation(ctx context.Context, id durableentity.ID) error {
	scope, err := scopeFor(id)
	if err != nil {
		return err
	}
	key := entityKey(id)
	return q.mutate(ctx, scopeKey(scope), &scope, func(r *quotaRecord, now time.Time) error {
		if retired(*r, id) {
			return durableentity.ErrRetired
		}
		if q.Limits.EntityBytes > r.Limits.EntityBytes {
			return durableentity.ErrUnsupported
		}
		if r.Invocations >= r.Limits.InvocationsPerMinute {
			return quotaErr("invocations", int64(r.Limits.InvocationsPerMinute), int64(r.Invocations+1), retryWindow(*r, now))
		}
		found := false
		grew := false
		for i := range r.Entities {
			if r.Entities[i].Key == key {
				found = true // Preserve a high-water reservation if nodes differ in caps.
				if r.Entities[i].Bytes < q.Limits.EntityBytes {
					r.Entities[i].Bytes = q.Limits.EntityBytes
					grew = true
				}
				break
			}
		}
		if !found {
			if len(r.Entities) >= entityCapacity(*r) {
				return quotaErr("entities", int64(entityCapacity(*r)), int64(len(r.Entities)+1), 0)
			}
			r.Entities = append(r.Entities, entityReservation{key, q.Limits.EntityBytes})
			grew = true
		}
		if n := retainedBytes(*r); grew && n > r.Limits.RetainedBytes {
			return quotaErr("retained_bytes", r.Limits.RetainedBytes, n, 0)
		}
		r.Invocations++
		return nil
	})
}

type TenantUsage struct {
	Limits             TenantLimits `json:"limits"`
	Connections        int          `json:"connections"`
	Grants             int          `json:"grants"`
	GrantBytes         int64        `json:"grant_bytes"`
	EntityReservations int          `json:"entity_reservations"`
	ReservedBytes      int64        `json:"reserved_bytes"`
	GrantsIssued       int          `json:"grants_issued"`
	Invocations        int          `json:"invocations"`
	WindowEnds         time.Time    `json:"window_ends"`
	StoredGrantBytes   int64        `json:"stored_grant_bytes"`
	ExpiredGrants      int          `json:"expired_grants"`
	RetiredEntities    int          `json:"retired_entities"`
	RecoveryBytes      int64        `json:"recovery_bytes"`
}

func (q *Quotas) Inspect(ctx context.Context, id durableentity.ID) (TenantUsage, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return TenantUsage{}, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if errors.Is(err, durableentity.ErrNotFound) {
		r = quotaRecord{Limits: q.Limits, Window: q.now()}
	} else if err != nil {
		return TenantUsage{}, err
	}
	if r.Schema < 2 {
		r.Limits = stricterLimits(r.Limits, q.Limits)
	}
	stored := grantBytes(r)
	expired := 0
	now := q.now()
	for _, g := range r.Grants {
		if !now.Before(g.ExpiresAt) {
			expired++
		}
	}
	prune(&r, now)
	return TenantUsage{r.Limits, len(r.Connections), len(r.Grants), grantBytes(r), len(r.Entities), retainedBytes(r), r.GrantsIssued, r.Invocations, r.Window.Add(quotaWindow), stored, expired, len(r.Retirements), recoveryBytes(r)}, nil
}
